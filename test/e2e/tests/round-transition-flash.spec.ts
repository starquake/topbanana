import { test, expect } from './fixtures';
import type { Page, Route } from './fixtures';
import { importQuiz, installPlaythroughClock } from './helpers';
import type { ImportRound } from './helpers';
import { adminStatePath } from '../e2e-auth';

// #1049 - advancing from a round-intro card to the first question of a NEW
// round used to flash the PREVIOUS round's last question for a frame. The
// Continue handler cleared roundItem synchronously while question still held
// the previous round's question, so the question template's
// `question && !roundItem` guard went true with stale data before the next
// /questions/next response landed. continueRound now leaves roundItem set
// until nextQuestion swaps roundItem and question together in one tick.
//
// This spec pins the fix deterministically: at the second round's intro card
// it holds the /questions/next response open, clicks Continue, and asserts the
// previous round's question text never paints while the fetch is pending - the
// intro card stays on screen instead. Releasing the response then shows the
// new round's question.
//
// The virtual clock (installPlaythroughClock) is the determinism anchor: the
// round-boundary auto-advance is a setInterval over a ~10s window driven by
// Date.now(), both of which the clock freezes. Time only moves where the test
// pumps it (the per-question reveal beat + feedback pause on a QUESTION
// screen), never while a boundary CARD is shown, so the auto-advance can never
// race the manual Continue and turn the test into a silent no-op.

test.use({ storageState: adminStatePath() });

// twoRoundQuiz builds a quiz with two single-question rounds, each carrying a
// summary so both round boundaries emit their intro and recap cards. The two
// questions have distinct option text so the playthrough can tell which round
// is on screen, and distinct question text so the stale-question assertion can
// anchor on the first round's prompt.
function twoRoundQuiz(): ImportRound[] {
  return [
    {
      title: 'Round 1',
      summary: 'First round ahead',
      questions: [
        {
          text: 'Round one question?',
          options: [
            { text: 'R1-correct', correct: true },
            { text: 'R1-wrong', correct: false },
          ],
        },
      ],
    },
    {
      title: 'Round 2',
      summary: 'Second round ahead',
      questions: [
        {
          text: 'Round two question?',
          options: [
            { text: 'R2-correct', correct: true },
            { text: 'R2-wrong', correct: false },
          ],
        },
      ],
    },
  ];
}

async function startQuiz(page: Page, quizTitle: string): Promise<void> {
  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  await page.getByRole('button', { name: 'Start Game' }).click();
}

// answerWhenReady pumps virtual time in small chunks until the option button is
// visible and enabled (clearing the reveal beat), clicks it, asserts the
// verdict, then pumps the feedback pause so the auto-advance reaches the round
// boundary. Mirrors helpers.answerRemainingQuestions but for a single named
// option, since this spec's two questions live in separate rounds.
async function answerWhenReady(page: Page, option: string): Promise<void> {
  const button = page.getByRole('button', { name: option });
  await expect(async () => {
    await page.clock.runFor(500);
    await expect(button).toBeVisible({ timeout: 100 });
    await expect(button).toBeEnabled({ timeout: 100 });
  }).toPass({ timeout: 10_000 });
  await button.click();
  await expect(page.getByTestId('reveal-verdict')).toHaveText('Correct!');
  await page.clock.runFor(3_500);
}

test('the previous round question does not flash when entering the next round', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  // Date.now() keeps the title unique per attempt: a Playwright retry reuses
  // the same per-worker DB, so a fixed title would 409 on re-import (#908).
  const quizTitle = `E2E Round Flash ${browserName} ${Date.now()}`;
  await importQuiz(page, {
    title: quizTitle,
    description: 'E2E round transition flash quiz',
    rounds: twoRoundQuiz(),
  });

  // Drop the admin cookie so the player session is anonymous, then install the
  // virtual clock before the SPA's first paint so every timer it arms is frozen
  // until the test pumps it.
  await page.context().clearCookies();
  await installPlaythroughClock(page);
  await startQuiz(page, quizTitle);

  // Round 1 intro -> Continue. The boundary auto-advance is frozen (no runFor
  // while a card is shown), so the manual click is the only way off the card.
  await expect(page.getByTestId('round-intro-card')).toBeVisible({ timeout: 10_000 });
  await expect(page.getByTestId('round-title')).toContainText('Round 1');
  await page.getByTestId('round-continue').click();

  // Round 1 question -> answer it. Pumps the reveal beat + feedback pause.
  await expect(page.getByTestId('question-text')).toHaveText('Round one question?', { timeout: 10_000 });
  await answerWhenReady(page, 'R1-correct');

  // Round 1 recap -> Continue -> round 2 intro.
  await expect(page.getByTestId('round-recap-card')).toBeVisible({ timeout: 10_000 });
  await page.getByTestId('round-continue').click();

  // Round 2 intro card. The previous round's question is still in component
  // state (this.question), so this is the boundary the flash regression hit.
  await expect(page.getByTestId('round-intro-card')).toBeVisible({ timeout: 10_000 });
  await expect(page.getByTestId('round-title')).toContainText('Round 2');

  // Hold the next /questions/next response so the window between the Continue
  // click and the new question landing is wide enough to assert against
  // deterministically. The route handler keeps the request pending until the
  // test releases it via the gate promise. times: 1 retires the route after the
  // single advance so a later request (the post-question finish fetch) is not
  // also held, and route.continue is guarded so a teardown-time stray request
  // can't reject unhandled.
  let releaseNext: () => void = () => {};
  const nextHeld = new Promise<void>((resolve) => {
    releaseNext = resolve;
  });
  let nextRequested = false;
  await page.route(
    '**/api/games/*/questions/next',
    async (route: Route) => {
      nextRequested = true;
      await nextHeld;
      try {
        await route.continue();
      } catch {
        // Context torn down before release; nothing to forward.
      }
    },
    { times: 1 },
  );

  // Click Continue on the round 2 intro. markRoundSeen resolves, then
  // nextQuestion fires the held /questions/next fetch.
  await page.getByTestId('round-continue').click();

  // Wait until the next-question request is actually in flight (the route
  // handler captured it) so the pending-state assertions are not racing the
  // markRoundSeen POST.
  await expect.poll(() => nextRequested, { timeout: 10_000 }).toBe(true);

  // While the next question is pending, the round 2 intro card stays mounted
  // and the previous round's question text never paints. The question <h2>
  // lives in <template x-if="question && !roundItem">, so with roundItem still
  // set it is absent from the DOM (count 0). With the bug, roundItem was
  // cleared here while question still held "Round one question?", flashing it
  // through the question template guard.
  await expect(page.getByTestId('round-intro-card')).toBeVisible();
  await expect(page.getByTestId('question-text')).toHaveCount(0);

  // Release the held response; round 2's question now mounts with its own
  // prompt and options, and the intro card is gone.
  releaseNext();
  await expect(page.getByTestId('question-text')).toHaveText('Round two question?', { timeout: 10_000 });
  await expect(page.getByRole('button', { name: 'R2-correct' })).toBeVisible();
  await expect(page.getByTestId('round-intro-card')).toBeHidden();
});
