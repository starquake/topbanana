import { adminStatePath } from '../e2e-auth';
import { test, expect } from './fixtures';
import { seedQuiz, setQuizMode, endHostedSession } from './helpers';
import type { Page } from '@playwright/test';

// session-first live hosting (#836, #851): a host opens a live room BEFORE
// choosing a quiz. From the admin dashboard the host clicks "Host a session",
// lands on the empty-room lobby (no quiz armed), a player joins and sees the
// staging waiting hint, then the host follows the lobby's pick-a-live-quiz link
// to the filtered quiz list, opens the seeded quiz, and "Host live" arms it back
// in that same empty room (one room per host) so the game runs. A second test
// covers the host ending the room from the lobby.
//
// The whole host flow is driven through the real browser (admin storageState
// passes the host gate) so the dashboard entry, the list-driven pick, and the
// End session control are all exercised end-to-end; the player page stays
// anonymous. Phase transitions are server-driven by the runner.

// seedLiveQuiz seeds a quiz as the shared admin and flips it to mode='live' so
// it is hostable (the importer only creates solo quizzes). The single-question
// shape lets one player drive the game to completion by answering once.
async function seedLiveQuiz(host: Page, title: string, questionText: string, correct: string): Promise<void> {
  await seedQuiz(host, title, [
    { text: questionText, options: [correct, 'wrong-a', 'wrong-b', 'wrong-c'], correctIndices: [0] },
  ]);
  setQuizMode(title, 'live');
}

// hostASession drives the dashboard "Host a session" entry through the browser:
// it opens the admin dashboard and clicks the empty-room control, landing on the
// host lobby. It returns the host page and the room's join code read off the
// lobby URL. The room is empty (no quiz armed) at this point.
//
// The dashboard host control is a single adaptive slot (#850): with no active
// room it is the "Host a session" submit; with one already open it becomes a
// "Resume session" link instead. Each session-hosting spec now ends its own
// room in cleanup (endHostedSession), so the shared seed admin's dashboard
// stays hostable between tests and this just clicks the submit. The one
// defensive end below covers only the rare case a prior test crashed before its
// cleanup ran and left a stray room open.
async function hostASession(page: Page, baseURL: string | undefined): Promise<{
  host: Page;
  joinCode: string;
  close: () => Promise<void>;
}> {
  const hostContext = await page.context().browser()!.newContext({ storageState: adminStatePath(), baseURL });
  const host = await hostContext.newPage();

  await host.goto('/admin');
  // Defensive single end for a stray room a crashed prior test left open (its
  // cleanup never ran): the dashboard then shows "Resume session" instead of the
  // submit. End that one room, then reload so the submit is offered. Not a drain
  // loop - one stray room is all an interrupted test can leave.
  const resume = host.getByTestId('resume-hosting');
  if (await resume.count() > 0) {
    const strayCode = (await resume.getAttribute('href'))?.split('/host/')[1] ?? '';
    if (strayCode) await endHostedSession(host, strayCode);
    await host.goto('/admin');
  }

  await expect(host.getByTestId('host-session-submit')).toBeVisible();
  await host.getByTestId('host-session-submit').click();
  await expect(host).toHaveURL(/\/host\/[A-Z0-9]{6}$/);
  const joinCode = host.url().split('/host/')[1];
  expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

  return {
    host,
    joinCode,
    close: async () => {
      await endHostedSession(host, joinCode);
      await hostContext.close();
    },
  };
}

// joinAsPlayer lands the anonymous page in the lobby via the deep link.
async function joinAsPlayer(page: Page, joinCode: string, name: string): Promise<void> {
  await page.goto(`/join/${joinCode}`);
  await page.getByTestId('join-name-input').fill(name);
  await page.getByTestId('join-name-submit').click();
  await expect(page.getByTestId('lobby-roster').getByText(name)).toBeVisible();
}

test.describe('session-first live hosting', () => {
  test('host opens an empty room, a player joins, then the host picks the first quiz and it runs', async ({ page, baseURL }) => {
    test.setTimeout(90_000);

    const stamp = Date.now();
    const quizTitle = `Session-first ${stamp}`;
    const player = `Pat-${stamp}`;

    const { host, joinCode, close } = await hostASession(page, baseURL);
    try {
      // The quiz the host will host. Seeded via the API (the host page stays on
      // the lobby), after the room is open, to prove the room can stage before a
      // quiz exists. It shows up when the host follows the pick link to the
      // live-filtered quiz list, which is fetched fresh on navigation.
      await seedLiveQuiz(host, quizTitle, 'What is 1+1?', 'two');

      // The empty room shows the pick-a-live-quiz link, not the Start controls.
      await expect(host.getByTestId('pick-quiz-link')).toBeVisible();
      await expect(host.getByTestId('start-now')).toBeHidden();

      // A player joins the empty room and sees the staging waiting hint (no quiz
      // picked yet) rather than a broken screen.
      await joinAsPlayer(page, joinCode, player);
      await expect(page.getByTestId('waiting-hint')).toContainText('start a quiz');

      // The host follows the lobby link to the live-filtered quiz list, opens the
      // seeded quiz, and hits "Host live". Because the host already has this empty
      // staging room open, "Host live" arms+starts THAT room (one room per host),
      // so the still-joined player is carried straight into the game.
      await host.getByTestId('pick-quiz-link').locator('a').click();
      await expect(host).toHaveURL(/\/admin\/quizzes\?mode=live$/);
      await host.getByRole('link', { name: quizTitle }).click();
      await host.getByRole('button', { name: 'Host live' }).click();

      // The still-joined player is carried into the game without re-entering a
      // code: they reach the question and the room is no longer empty.
      await expect(page.getByTestId('question-view')).toBeVisible({ timeout: 20_000 });
      await expect(page.getByTestId('question-text')).toHaveText('What is 1+1?');
      await expect(page.getByTestId('question-options')).toBeVisible({ timeout: 10_000 });
      await expect(
        page.getByTestId('question-options').getByRole('button', { name: 'two' }),
      ).toBeEnabled();
    } finally {
      await close();
    }
  });

  test('the host ends the session and the room closes for the joined player', async ({ page, baseURL }) => {
    test.setTimeout(90_000);

    const stamp = Date.now();
    const player = `Quinn-${stamp}`;

    const { host, joinCode, close } = await hostASession(page, baseURL);
    try {
      // A player joins the empty room.
      await joinAsPlayer(page, joinCode, player);
      await expect(page.getByTestId('waiting-hint')).toBeVisible();

      // The End session control carries a confirm guard; accept it so the submit
      // proceeds.
      host.once('dialog', (dialog) => dialog.accept());
      await host.getByTestId('end-session').click();

      // The room is terminally closed: the still-joined player drops into the
      // finished view (the room ended out from under them).
      await expect(page.getByTestId('finished-view')).toBeVisible({ timeout: 20_000 });
    } finally {
      await close();
    }
  });
});
