import type { APIRequestContext, BrowserContext, Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';

import { adminStatePath } from '../e2e-auth';
import { test, expect } from './fixtures';
import { importQuiz } from './helpers';

// MP-9 (#686): the between-rounds standings bar graph on the round_results /
// finished screens, on BOTH the host TV surface and the player join surface.
// The graph animates each player's round points onto their pre-round total and
// rests the rows in rank order (best-first). The animation is reduced-motion
// safe, so this spec runs the contexts under prefers-reduced-motion: reduce and
// asserts the final settled DOM (rendered order + numeric totals) - which also
// exercises the reduced-motion jump-to-final path.
//
// The runner drives every phase transition on its 500ms e2e beat, so the
// round_results window is brief; the assertions use Playwright's retrying
// web-first matchers, and the terminal finished phase (stable) is asserted in
// full on both surfaces.

// makeQuizLiveByTitle flips a quiz to mode='live' (the importer lands quizzes
// on 'solo', and only live quizzes are hostable) and returns its id. Mirrors
// the sqlite3 shortcut the other live-session specs use.
function makeQuizLiveByTitle(title: string): number {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; cannot mark a quiz live');
  }
  const dbFile = join(dataDir, `e2e-${test.info().parallelIndex}.db`);
  const escapedTitle = title.replace(/'/g, "''");
  const output = execFileSync('sqlite3', [
    dbFile,
    `UPDATE quizzes SET mode = 'live' WHERE title = '${escapedTitle}'; SELECT id FROM quizzes WHERE title = '${escapedTitle}';`,
  ], { encoding: 'utf8' });
  const lines = output.trim().split('\n');
  const id = Number.parseInt(lines[lines.length - 1], 10);
  if (!Number.isInteger(id)) {
    throw new Error(`makeQuizLiveByTitle(${title}): could not resolve quiz id from ${JSON.stringify(output)}`);
  }
  return id;
}

type SessionState = {
  phase: string;
  question: { id: number; options: { id: number; text: string }[] } | null;
};

// answerOverApi resolves the option id whose text matches off the participant's
// GET /state and POSTs it, so an API-only player can answer a known choice with
// no UI. Returns silently when the session is not currently in the question
// phase (the runner may have advanced past it).
async function answerOverApi(
  request: APIRequestContext,
  code: string,
  text: string,
): Promise<void> {
  const resp = await request.get(`/api/sessions/${code}/state`);
  if (!resp.ok()) return;
  const state = (await resp.json()) as SessionState;
  if (state.phase !== 'question' || !state.question) return;
  const option = state.question.options.find((o) => o.text === text);
  if (!option) return;
  await request.post(`/api/sessions/${code}/answer`, { data: { optionId: option.id } });
}

// answerOnPage clicks the option with the given text on the player surface once
// the question view is shown. Tolerates the runner having already advanced.
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
    // The window may have closed (the runner early-closes once everyone is in);
    // the answer is not required for the standings assertions.
  }
}

// readStandingsRows reads the rendered standings rows in DOM order, returning
// each row's rank, the displayed name, and the displayed total. DOM order is
// the on-screen order, so a best-first assertion checks both the data and the
// sort.
async function readStandingsRows(scope: Page): Promise<{ rank: string; name: string; total: string }[]> {
  const rows = scope.locator('[data-testid="standings-bars"] [data-standings-row]');
  const count = await rows.count();
  const out: { rank: string; name: string; total: string }[] = [];
  for (let i = 0; i < count; i++) {
    const row = rows.nth(i);
    out.push({
      rank: (await row.getAttribute('data-rank')) ?? '',
      name: (await row.locator('[data-standings-name]').innerText()).trim(),
      total: (await row.locator('[data-standings-total]').innerText()).trim(),
    });
  }
  return out;
}

test('the standings bar graph shows final order and totals on the TV and player surfaces', async ({
  page,
  baseURL,
  browserName,
}) => {
  test.setTimeout(90_000);

  const quizTitle = `MP9 Standings ${browserName} ${Date.now()}`;

  // Host context (shared admin) seeds a two-round quiz, makes it live, opens a
  // session, and watches the TV. The player surface (page) stays anonymous.
  const hostContext = await page.context().browser()!.newContext({
    storageState: adminStatePath(),
    baseURL,
    reducedMotion: 'reduce',
  });
  const host = await hostContext.newPage();

  // Two rounds, one question each. The page player answers the correct option
  // every time and the API player answers a wrong one, so the page player leads
  // throughout - a deterministic best-first order with distinct totals.
  await importQuiz(host, {
    title: quizTitle,
    description: 'MP-9 standings bar graph spec',
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

  // The host opens the TV surface.
  await host.goto(`/host/${joinCode}`);

  // An API-only second player joins from its own anonymous context.
  const otherContext: BrowserContext = await page.context().browser()!.newContext({
    storageState: undefined,
    baseURL,
    reducedMotion: 'reduce',
  });
  const other = otherContext.request;
  const otherJoin = await other.post(`/api/sessions/${joinCode}/join`, { data: { displayName: 'Robin' } });
  expect(otherJoin.status()).toBe(200);

  // The page player joins via the deep link and lands in the lobby.
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto(`/join/${joinCode}`);
  await page.getByTestId('join-name-input').fill('Quincy');
  await page.getByTestId('join-name-submit').click();
  await expect(page.getByTestId('lobby-roster').getByText('Quincy')).toBeVisible();

  // Host starts the game; the runner drives the phases on its own beat.
  const startResp = await host.request.post(`/api/sessions/${joinCode}/start`);
  expect(startResp.status(), `start session: ${startResp.status()} ${await startResp.text()}`).toBe(204);

  // Round 1: Quincy answers '4' (correct), Robin answers '3' (wrong).
  await answerOnPage(page, '4');
  await answerOverApi(other, joinCode, '3');

  // Round 1 round_results: both surfaces show Quincy on top (she scored, Robin
  // did not). The window is brief, so use retrying matchers; reduced motion
  // means the final state is rendered on the first paint.
  await expect(page.locator('[data-testid="round-results"] [data-standings-row]').first())
    .toBeVisible({ timeout: 15_000 });
  await expect(async () => {
    const rows = await readStandingsRows(page);
    expect(rows.length).toBe(2);
    expect(rows[0].name).toBe('Quincy');
    expect(rows[0].rank).toBe('1');
    expect(Number(rows[0].total)).toBeGreaterThan(0);
    expect(rows[1].name).toBe('Robin');
    expect(rows[1].rank).toBe('2');
    expect(Number(rows[1].total)).toBe(0);
  }).toPass({ timeout: 10_000 });

  // The player's own row is highlighted (aria-current).
  await expect(
    page.locator('[data-testid="round-results"] [data-standings-row]').first(),
  ).toHaveAttribute('aria-current', 'true');

  // Round 2: Quincy answers '6' (correct), Robin answers '5' (wrong).
  await answerOnPage(page, '6');
  await answerOverApi(other, joinCode, '5');

  // Finished: the terminal phase is stable. Both surfaces show the final
  // standings bar graph with Quincy first (two correct answers) and Robin
  // second on zero. Assert the host TV and the player surface independently.
  await expect(host.locator('[data-phase-results] [data-standings-row]').first())
    .toBeVisible({ timeout: 20_000 });
  const tvRows = await readStandingsRows(host);
  expect(tvRows.length).toBe(2);
  expect(tvRows[0].name).toBe('Quincy');
  expect(tvRows[0].rank).toBe('1');
  expect(Number(tvRows[0].total)).toBeGreaterThan(0);
  expect(tvRows[1].name).toBe('Robin');
  expect(tvRows[1].rank).toBe('2');
  expect(Number(tvRows[1].total)).toBe(0);

  await expect(page.getByTestId('finished-view').locator('[data-standings-row]').first())
    .toBeVisible({ timeout: 20_000 });
  const playerRows = await readStandingsRows(page);
  expect(playerRows.length).toBe(2);
  expect(playerRows[0].name).toBe('Quincy');
  expect(playerRows[0].rank).toBe('1');
  expect(Number(playerRows[0].total)).toBeGreaterThan(0);
  expect(playerRows[1].name).toBe('Robin');
  expect(playerRows[1].rank).toBe('2');
  expect(Number(playerRows[1].total)).toBe(0);

  await otherContext.close();
  await hostContext.close();
});
