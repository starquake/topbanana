import type { APIRequestContext, BrowserContext, Page } from '@playwright/test';
import { join } from 'node:path';

import { adminStatePath } from '../e2e-auth';
import { test, expect } from './fixtures';
import { importQuiz, claimAndJoin, execSqlite } from './helpers';

// Motion-ON capture of the standings animation (the #729 grow + the #730 row
// slide) so the real motion can actually be eyeballed. The standings-bargraph
// spec runs under prefers-reduced-motion and only asserts the settled DOM, so
// the animation itself is never recorded there. This spec runs with motion on
// and records a video.
//
// KEPT OUT OF CI (per request): it is a manual capture tool, not a regression
// gate, and the timing-sensitive motion would be flaky as a CI assertion. The
// test self-skips when CI is set. Run it locally from test/e2e:
//
//   npx playwright test standings-swap-video --project=chromium
//
// The recording lands at test/e2e/test-results/<test-name>/video.webm.
//
// Why the finished screen: the runner advances every other phase on its short
// e2e beat (round_results is only ~500ms), but the finished phase is terminal
// and stable, so the full grow + slide play out there. The game is driven so
// the recorded player's row starts 2nd after round one and overtakes to 1st at
// the finish, so the finished screen shows that row sliding up past the other
// while its bar grows - the swap.
//
// test.use must be top-level: { video } forces a dedicated worker, which
// Playwright forbids inside a describe group.
test.use({ reducedMotion: 'no-preference', video: 'on' });

// makeQuizLiveByTitle flips a quiz to mode='live' via the sqlite3 shortcut the
// live-session specs use (the importer lands quizzes on 'solo'). Tied to the
// worker DB by parallelIndex, like the other live specs.
function makeQuizLiveByTitle(title: string): number {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; cannot mark a quiz live');
  }
  const dbFile = join(dataDir, `e2e-${test.info().parallelIndex}.db`);
  const escaped = title.replace(/'/g, "''");
  const output = execSqlite(
    dbFile,
    `UPDATE quizzes SET mode = 'live' WHERE title = '${escaped}'; SELECT id FROM quizzes WHERE title = '${escaped}';`,
  );
  const id = Number.parseInt(output.split('\n').pop() ?? '', 10);
  if (!Number.isInteger(id)) {
    throw new Error(`makeQuizLiveByTitle(${title}): could not resolve id from ${JSON.stringify(output)}`);
  }
  return id;
}

type SessionState = {
  phase: string;
  serverNow: string;
  question: { id: number; startedAt: string | null; options: { id: number; text: string }[] } | null;
  standings: { displayName: string; totalScore: number; rank: number }[] | null;
};

// answerOverApi waits through round_intro / the read beat for the answer window
// to open, then resolves the option whose text matches off GET /state and POSTs
// it. It only gives up once the question is over (round_results/finished) or the
// deadline passes - so it can be called the instant the game starts and still
// land the round-1 answer (unlike a one-shot check that bails during the intro).
async function answerOverApi(request: APIRequestContext, code: string, text: string): Promise<void> {
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    const resp = await request.get(`/api/sessions/${code}/state`);
    if (resp.ok()) {
      const state = (await resp.json()) as SessionState;
      if (state.phase === 'round_results' || state.phase === 'finished') return;
      if (state.phase === 'question' && state.question) {
        const open = state.question.startedAt
          ? Date.parse(state.serverNow) >= Date.parse(state.question.startedAt)
          : true;
        if (open) {
          const option = state.question.options.find((o) => o.text === text);
          if (option) await request.post(`/api/sessions/${code}/answer`, { data: { optionId: option.id } });
          return;
        }
      }
    }
    await new Promise((r) => setTimeout(r, 50));
  }
}

// answerOnPage clicks the option with the given text on the player surface.
async function answerOnPage(page: Page, text: string): Promise<void> {
  const view = page.getByTestId('question-view');
  try {
    await expect(view).toBeVisible({ timeout: 15_000 });
  } catch {
    return;
  }
  const button = page.getByTestId('question-options').getByRole('button', { name: text, exact: true });
  try {
    await expect(button).toBeEnabled({ timeout: 5_000 });
    await button.click();
  } catch {
    // The window may have closed once everyone answered; tolerate it.
  }
}

test('standings swap video: a player row slides up to first at the finish', async ({ page, baseURL, browserName }) => {
  test.skip(!!process.env.CI, 'manual animation capture tool; run locally, not in CI');
  test.setTimeout(120_000);

  const quizTitle = `Swap video ${browserName} ${Date.now()}`;
  const suffix = `${browserName}-${Date.now()}`;
  const climber = `Cam-${suffix}`;  // the recorded player: 2nd after round 1, 1st at the finish
  const leader = `Lee-${suffix}`;   // API rival: leads round 1, falls to 2nd

  // The host (shared admin) seeds a two-round quiz, makes it live, and opens a
  // session. The recorded `page` is the climber's player surface.
  const hostContext: BrowserContext = await page.context().browser()!.newContext({
    storageState: adminStatePath(),
    baseURL,
  });
  const host = await hostContext.newPage();

  await importQuiz(host, {
    title: quizTitle,
    description: 'Standings swap video',
    rounds: [
      {
        title: 'Round one',
        questions: [
          { text: 'What is 2+2?', options: [
            { text: '3', correct: false },
            { text: '4', correct: true },
            { text: '5', correct: false },
            { text: '6', correct: false },
          ] },
        ],
      },
      {
        title: 'Round two',
        questions: [
          { text: 'What is 3+3?', options: [
            { text: '5', correct: false },
            { text: '6', correct: true },
            { text: '7', correct: false },
            { text: '8', correct: false },
          ] },
        ],
      },
    ],
  });
  const quizID = makeQuizLiveByTitle(quizTitle);

  const createResp = await host.request.post('/api/sessions', { data: { quizId: quizID } });
  expect(createResp.status(), `create session: ${createResp.status()} ${await createResp.text()}`).toBe(201);
  const { joinCode } = await createResp.json() as { joinCode: string };

  // The API rival joins; the recorded player joins via the deep link.
  const leaderContext: BrowserContext = await page.context().browser()!.newContext({ storageState: undefined, baseURL });
  await claimAndJoin(leaderContext.request, joinCode, leader);

  await page.goto(`/join/${joinCode}`);
  await page.getByTestId('join-name-input').fill(climber);
  await page.getByTestId('join-name-submit').click();
  await expect(page.getByTestId('lobby-roster').getByText(climber)).toBeVisible();

  const startResp = await host.request.post(`/api/sessions/${joinCode}/start`);
  expect(startResp.status(), `start: ${startResp.status()} ${await startResp.text()}`).toBe(204);

  // Round 1: the rival answers correctly as soon as the window opens (fast);
  // the climber also answers correctly but ~1.5s later (UI click), so its
  // smaller speed bonus leaves it 2nd. Deterministic order: leader 1st, 2nd.
  await answerOverApi(leaderContext.request, joinCode, '4');
  await page.waitForTimeout(1_500);
  await answerOnPage(page, '4');

  // Round 1 standings render on the climber's surface (it sits 2nd, briefly).
  await expect(page.locator('[data-testid="round-results"] [data-standings-row]').first())
    .toBeVisible({ timeout: 15_000 });

  // Round 2: only the climber answers correctly, overtaking the rival on the
  // cumulative total.
  await answerOnPage(page, '6');
  await answerOverApi(leaderContext.request, joinCode, '5');

  // Finished (terminal, stable): the climber's row slides up to 1st while its
  // bar grows. Assert via the authoritative /state that this was a real
  // overtake - the leader actually scored (so it led round 1) and the climber
  // finished ahead of it - so the recording can never be a non-swap again.
  const finished = page.locator('[data-testid="finished-view"]');
  const rows = finished.locator('[data-standings-row]');
  await expect(rows.first()).toBeVisible({ timeout: 20_000 });

  // Empirically prove the animation actually RUNS (not an instant snap): sample
  // the climber's own row (aria-current) as the finished screen plays. A real
  // grow steps the displayed total through many intermediate values; a real
  // slide steps the row's Y position through many. An instant snap would show
  // ~1 of each. This is the motion coverage the reduced-motion specs can't give.
  const motion = await page.evaluate(async () => {
    const view = document.querySelector('[data-testid="finished-view"]');
    const mine = () => view && view.querySelector('[data-standings-row][aria-current="true"]');
    const totals: string[] = [];
    const tops: number[] = [];
    for (let i = 0; i < 30; i++) {
      const row = mine();
      const totalEl = row && row.querySelector('[data-standings-total]');
      if (totalEl && totalEl.textContent) totals.push(totalEl.textContent.trim());
      if (row) tops.push(Math.round(row.getBoundingClientRect().top));
      await new Promise((r) => setTimeout(r, 40));
    }
    return { totals: [...new Set(totals)], tops: [...new Set(tops)] };
  });
  expect(motion.totals.length, `finished bars should grow through intermediate totals, saw ${JSON.stringify(motion.totals)}`).toBeGreaterThan(3);
  expect(motion.tops.length, `climber row should slide through intermediate positions, saw ${JSON.stringify(motion.tops)}`).toBeGreaterThan(1);

  const stateResp = await page.request.get(`/api/sessions/${joinCode}/state`);
  expect(stateResp.ok(), `state: ${stateResp.status()} ${await stateResp.text()}`).toBeTruthy();
  const state = (await stateResp.json()) as SessionState;
  expect(state.phase).toBe('finished');
  const climberStanding = (state.standings ?? []).find((s) => s.displayName === climber);
  const leaderStanding = (state.standings ?? []).find((s) => s.displayName === leader);
  expect(climberStanding, 'climber finished standing').toBeTruthy();
  expect(leaderStanding, 'leader finished standing').toBeTruthy();
  expect(leaderStanding!.totalScore, 'leader scored in round 1 (a real contest)').toBeGreaterThan(0);
  expect(climberStanding!.totalScore, 'climber overtook the leader').toBeGreaterThan(leaderStanding!.totalScore);

  // The DOM settles with the climber sliding up into 1st.
  await expect(async () => {
    await expect(rows.first().locator('[data-standings-name]')).toHaveText(climber);
    await expect(rows.first()).toHaveAttribute('data-rank', '1');
  }).toPass({ timeout: 15_000 });

  // Dwell so the recording includes the full settle.
  await page.waitForTimeout(3_000);

  await leaderContext.close();
  await hostContext.close();
});
