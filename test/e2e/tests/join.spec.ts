import { join } from 'node:path';

import { adminStatePath } from '../e2e-auth';
import { test, expect } from './fixtures';
import { seedQuiz, execSqlite } from './helpers';

// makeQuizLive flips a seeded quiz to mode='live' (the importer always lands
// quizzes on 'solo', and only live quizzes are hostable, MP-0 / #677) and
// returns its id so the test can open a session for it. Mirrors the sqlite3
// shortcut the role/verify helpers use rather than driving an admin UI that
// has no live-mode toggle in the seeded-import path.
function makeQuizLive(title: string): number {
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
    throw new Error(`makeQuizLive(${title}): could not resolve quiz id from sqlite output ${JSON.stringify(output)}`);
  }
  return id;
}

// The host setup (seed quiz, make it live, open a session) runs as the shared
// admin so POST /api/sessions passes the host gate. The player join flow then
// runs in the default anonymous context. test.use scopes the storageState to
// this file's host-side request only; the page-driven join is anonymous.
test.describe('player join + lobby', () => {
  test('joins via typed code, enters a name, lands in the lobby, and toggles ready', async ({ page }) => {
    const quizTitle = `Live Quiz ${Date.now()}`;
    // Player names are global on players.display_name now (#716), so a unique
    // name avoids a collision with a parallel spec sharing the worker DB.
    const alice = `Alice-${Date.now()}`;

    // Seed + host setup as the admin (storageState) in a separate browser
    // context so the player page itself stays anonymous. seedQuiz drives the
    // admin importer, which needs the admin cookie jar.
    const hostContext = await page.context().browser()!.newContext({ storageState: adminStatePath() });
    const host = await hostContext.newPage();

    await seedQuiz(host, quizTitle);
    const quizID = makeQuizLive(quizTitle);

    const createResp = await host.request.post('/api/sessions', {
      data: { quizId: quizID },
    });
    expect(createResp.status(), `create session: ${createResp.status()} ${await createResp.text()}`).toBe(201);
    const { joinCode } = await createResp.json() as { joinCode: string };
    expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

    // Player flow: anonymous page, typed code -> name -> lobby. GET /join
    // serves the enter-code form, which is the page the host lobby's
    // typed-code guidance points players at (#750).
    await page.goto('/join');
    await expect(page.getByTestId('join-code-input')).toBeVisible();
    await expect(page.getByTestId('join-code-submit')).toBeVisible();
    await page.getByTestId('join-code-input').fill(joinCode.toLowerCase());
    await page.getByTestId('join-code-submit').click();

    await page.getByTestId('join-name-input').fill(alice);
    await page.getByTestId('join-name-submit').click();

    // Lands in the lobby: the roster shows the player, not-ready by default.
    const roster = page.getByTestId('lobby-roster');
    await expect(roster).toBeVisible();
    await expect(roster.getByText(alice)).toBeVisible();
    const aliceRow = roster.locator('li', { hasText: alice });
    await expect(aliceRow).toHaveAttribute('data-ready', 'false');

    // Toggle ready and confirm it reflects on the player's own row.
    await page.getByTestId('ready-toggle').click();
    await expect(aliceRow).toHaveAttribute('data-ready', 'true');
    await expect(page.getByTestId('ready-toggle')).toHaveAttribute('data-ready', 'true');

    // The authoritative GET /state agrees the player is ready - assert against
    // the API directly (the TV surface lives in MP-3, not this worktree).
    const stateResp = await page.request.get(`/api/sessions/${joinCode}/state`);
    expect(stateResp.ok()).toBeTruthy();
    const state = await stateResp.json() as { players: { displayName: string; isReady: boolean }[] };
    const aliceState = state.players.find((p) => p.displayName === alice);
    expect(aliceState?.isReady).toBe(true);

    // Toggle back to not-ready and confirm the round-trip the other way.
    await page.getByTestId('ready-toggle').click();
    await expect(aliceRow).toHaveAttribute('data-ready', 'false');

    // While no last-call countdown is armed the lobby shows the static waiting
    // hint (#735). When the host arms the countdown the live "Starting in M:SS"
    // replaces it, driven off the server clock and pushed by the SSE tick.
    await expect(page.getByTestId('waiting-hint')).toBeVisible();
    const armResp = await host.request.post(`/api/sessions/${joinCode}/arm-start`);
    expect(armResp.status(), `arm-start: ${armResp.status()} ${await armResp.text()}`).toBe(204);
    await expect(page.getByTestId('start-countdown')).toContainText('Starting in');
    await expect(page.getByTestId('waiting-hint')).toHaveCount(0);

    await hostContext.close();
  });

  test('a deep-linked /join/{code} skips straight to the name form', async ({ page }) => {
    const quizTitle = `Live Deep ${Date.now()}`;
    const bob = `Bob-${Date.now()}`;

    const hostContext = await page.context().browser()!.newContext({ storageState: adminStatePath() });
    const host = await hostContext.newPage();
    await seedQuiz(host, quizTitle);
    const quizID = makeQuizLive(quizTitle);
    const createResp = await host.request.post('/api/sessions', { data: { quizId: quizID } });
    expect(createResp.status()).toBe(201);
    const { joinCode } = await createResp.json() as { joinCode: string };

    await page.goto(`/join/${joinCode}`);
    // No enter-code step: the name input is shown immediately, with the code
    // echoed in the heading.
    await expect(page.getByTestId('join-name-input')).toBeVisible();
    await expect(page.getByText(joinCode)).toBeVisible();

    await page.getByTestId('join-name-input').fill(bob);
    await page.getByTestId('join-name-submit').click();
    await expect(page.getByTestId('lobby-roster').getByText(bob)).toBeVisible();

    await hostContext.close();
  });
});
