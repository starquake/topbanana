import { join } from 'node:path';

import type { APIRequestContext } from '@playwright/test';

import { test, expect } from './fixtures';
import { importQuiz, claimAndJoin, csrfTokenPattern, execSqlite } from './helpers';

// #836 persistent live rooms: a host runs a live quiz to its between-games
// intermission, the player sees the intermission/standings view while staying
// joined, the host arms the next quiz, and the still-joined player is carried
// into game 2 without re-entering a code. A latecomer joining mid-game lands
// straight into the in-flight phase.
//
// The host setup (import + make live + open session + start + next-quiz) runs
// as the shared admin in a separate browser context so the player page stays
// anonymous. Phase transitions are driven server-side by the session runner;
// the player never advances them and only submits one answer per question.

// SINGLE_QUESTION is a one-round, one-question live quiz so a single player can
// drive the whole game to intermission by answering once (all active players
// in -> the runner closes, reveals, and ends the game into intermission).
function singleQuestionDoc(title: string, questionText: string, correct: string) {
  return {
    title,
    description: 'Persistent-rooms e2e quiz',
    rounds: [
      {
        title: 'Round 1',
        questions: [
          {
            text: questionText,
            options: [
              { text: correct, correct: true },
              { text: 'wrong-a', correct: false },
              { text: 'wrong-b', correct: false },
              { text: 'wrong-c', correct: false },
            ],
          },
        ],
      },
    ],
  };
}

// postNextQuiz arms the room's next game via the host next-quiz endpoint. It
// scrapes the CSRF token from the host lobby page (its start/end forms carry the
// hidden token) and posts to /host/{code}/next-quiz with the new quiz id, the
// re-arm API the host page uses. The handler 303-redirects back to the lobby;
// maxRedirects:0 keeps the redirect visible.
async function postNextQuiz(
  request: APIRequestContext,
  code: string,
  quizId: number,
): Promise<void> {
  const lobbyResp = await request.get(`/host/${code}`);
  expect(lobbyResp.ok(), `host lobby: ${lobbyResp.status()}`).toBeTruthy();
  const html = await lobbyResp.text();
  const match = csrfTokenPattern.exec(html);
  const csrfToken = match?.[1] ?? match?.[2];
  expect(csrfToken, 'host lobby should carry a csrf token').toBeTruthy();

  const resp = await request.post(`/host/${code}/next-quiz`, {
    form: { csrf_token: csrfToken!, quiz_id: String(quizId) },
    maxRedirects: 0,
  });
  expect(resp.status(), `next-quiz: ${resp.status()} ${await resp.text()}`).toBe(303);
  expect(resp.headers().location).toBe(`/host/${code}`);
}

test.describe('persistent live rooms', () => {
  test('carries a still-joined player from game 1 intermission into game 2', async ({ page, hostSessions }) => {
    test.setTimeout(90_000);

    const stamp = Date.now();
    const quiz1Title = `Persistent G1 ${stamp}`;
    const quiz2Title = `Persistent G2 ${stamp}`;
    const player = `Pat-${stamp}`;

    const host = await hostSessions.adminHost();

    // Two single-question live quizzes: game 1 the room opens on, game 2 the
    // host arms at intermission. Importing live + flipping the mode (a no-op
    // re-assert) resolves each id off the worker DB.
    await importQuiz(host, singleQuestionDoc(quiz1Title, 'Game 1: what is 1+1?', 'two'), 'live');
    await importQuiz(host, singleQuestionDoc(quiz2Title, 'Game 2: what is 2+2?', 'four'), 'live');
    const quiz1Id = makeLiveAndResolveId(quiz1Title);
    const quiz2Id = makeLiveAndResolveId(quiz2Title);

    // Open the room on game 1.
    const { joinCode } = await hostSessions.openViaApi(quiz1Id);
    expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

    // The page-driven player joins via the deep link and lands in the lobby.
    await page.goto(`/join/${joinCode}`);
    await page.getByTestId('join-name-input').fill(player);
    await page.getByTestId('join-name-submit').click();
    await expect(page.getByTestId('lobby-roster').getByText(player)).toBeVisible();

    // Host starts game 1; the runner drives round_intro -> question.
    const startResp = await host.request.post(`/api/sessions/${joinCode}/start`);
    expect(startResp.status(), `start: ${startResp.status()} ${await startResp.text()}`).toBe(204);

    await expect(page.getByTestId('question-view')).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId('question-text')).toHaveText('Game 1: what is 1+1?');

    // The page player answers game 1's only question via the page UI. With
    // the single active player in, the runner closes the question, reveals,
    // and ends the game into intermission.
    await expect(page.getByTestId('question-options')).toBeVisible({ timeout: 10_000 });
    await page.getByTestId('question-options').getByRole('button', { name: 'two' }).click();
    await expect(page.getByTestId('answered-waiting')).toBeVisible();

    // Game 1 ends into intermission: the player sees the intermission view
    // (final standings + the waiting message), staying joined - no re-join,
    // no enter-code form.
    await expect(page.getByTestId('intermission-view')).toBeVisible({ timeout: 20_000 });
    await expect(page.getByTestId('intermission-waiting')).toContainText('Waiting for the host');
    await expect(page.getByTestId('standings-bars').locator('[data-standings-row]')).toHaveCount(1);
    await expect(page.getByTestId('standings-bars').getByText(player)).toBeVisible();
    // Still joined: the enter-code form is not shown.
    await expect(page.getByTestId('join-code-input')).toBeHidden();

    // The host arms game 2. The room re-arms onto quiz 2 and the runner drives
    // the new game; the player is carried in off the SSE -> GET state loop.
    await postNextQuiz(host.request, joinCode, quiz2Id);

    // Game 2 plays for the still-joined player: they reach the new question
    // without re-entering a code, and no stale game-1 state leaks (the new
    // question text is shown, options are fresh).
    await expect(page.getByTestId('question-view')).toBeVisible({ timeout: 20_000 });
    await expect(page.getByTestId('question-text')).toHaveText('Game 2: what is 2+2?');
    await expect(page.getByTestId('question-options')).toBeVisible({ timeout: 10_000 });
    // No stale pick from game 1: the answered/waiting state is gone and the
    // options are tappable again.
    await expect(page.getByTestId('answered-waiting')).toBeHidden();
    const game2Correct = page.getByTestId('question-options').getByRole('button', { name: 'four' });
    await expect(game2Correct).toBeEnabled();
  });

  test('a latecomer joins a live room mid-game and lands in the question', async ({ page, hostSessions }) => {
    test.setTimeout(90_000);

    const stamp = Date.now();
    const quizTitle = `Persistent Latecomer ${stamp}`;
    const early = `Early-${stamp}`;
    const late = `Late-${stamp}`;

    const host = await hostSessions.adminHost();

    await importQuiz(host, singleQuestionDoc(quizTitle, 'Latecomer: what is 3+3?', 'six'), 'live');
    const quizId = makeLiveAndResolveId(quizTitle);

    const { joinCode } = await hostSessions.openViaApi(quizId);

    // An early player joins via the API and readies so the game can start.
    // They deliberately do NOT answer, so the question phase stays open long
    // enough for the latecomer to land in it.
    const earlyCtx = await hostSessions.newPlayerContext();
    await claimAndJoin(earlyCtx.request, joinCode, early);
    const readyResp = await earlyCtx.request.post(`/api/sessions/${joinCode}/ready`, { data: { ready: true } });
    expect(readyResp.status()).toBe(204);

    const startResp = await host.request.post(`/api/sessions/${joinCode}/start`);
    expect(startResp.status(), `start: ${startResp.status()} ${await startResp.text()}`).toBe(204);

    // Wait until the room is in the question phase (the early player holds
    // their answer, so it stays open).
    await expect(async () => {
      const resp = await earlyCtx.request.get(`/api/sessions/${joinCode}/state`);
      expect(resp.ok()).toBeTruthy();
      const state = await resp.json() as { phase: string };
      expect(state.phase).toBe('question');
    }).toPass({ timeout: 15_000 });

    // The latecomer joins mid-game via the page UI: they land directly in the
    // in-flight question phase, not a lobby, and render the question.
    await page.goto(`/join/${joinCode}`);
    await page.getByTestId('join-name-input').fill(late);
    await page.getByTestId('join-name-submit').click();

    await expect(page.getByTestId('question-view')).toBeVisible({ timeout: 20_000 });
    await expect(page.getByTestId('question-text')).toHaveText('Latecomer: what is 3+3?');
  });
});

// makeLiveAndResolveId flips a seeded quiz to mode='live' and returns its id in
// one sqlite round-trip, mirroring makeQuizLive in play-live.spec.ts: the
// importer lands quizzes on 'solo' and only live quizzes are hostable, and the
// importer does not return the id over the API, so flip + resolve the id off
// the worker DB.
function makeLiveAndResolveId(title: string): number {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; cannot mark a quiz live');
  }
  const dbFile = join(dataDir, `e2e-${test.info().parallelIndex}.db`);
  const escapedTitle = title.replace(/'/g, "''");
  const output = execSqlite(
    dbFile,
    `UPDATE quizzes SET mode = 'live' WHERE title = '${escapedTitle}'; SELECT id FROM quizzes WHERE title = '${escapedTitle}';`,
  );
  const lines = output.split('\n');
  const id = Number.parseInt(lines[lines.length - 1], 10);
  if (!Number.isInteger(id)) {
    throw new Error(`makeLiveAndResolveId(${title}): could not resolve id from ${JSON.stringify(output)}`);
  }
  return id;
}
