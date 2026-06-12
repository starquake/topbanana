import { join } from 'node:path';

import { test, expect } from './fixtures';
import { seedQuiz, execSqlite } from './helpers';

// Polish bundle for the player session screen (#888):
//   - The lobby quiz name renders as the visual anchor (a large heading at
//     the top of the card) and the e2e check pins it by data-testid so the
//     class drift catches as a test failure rather than a silent regression.
//   - The home-page CTA flips from "Join a live game" to "Resume session"
//     when localStorage carries a remembered session, and exposes a small
//     "or exit" link that clears the entry without re-entering the lobby.
//   - The in-session "Exit session" link opens a confirm modal; Cancel
//     keeps the player in the room, Confirm POSTs leave and routes back to
//     the bare /join entry-code screen.
//   - The host big-screen QR sits inside a `data-testid` box bumped to a
//     larger clamp range so the scan code reads from across a room.
//
// All checks are tightly scoped (class presence + DOM hooks) rather than
// pixel measurements so the spec stays robust across rendering engines.

// makeQuizLive flips a seeded quiz to mode='live' and returns its id, mirroring
// the sqlite3 shortcut the other live specs use.
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
    throw new Error(`makeQuizLive(${title}): could not resolve quiz id from ${JSON.stringify(output)}`);
  }
  return id;
}

test('the lobby renders the quiz name as the visual anchor', async ({ page, hostSessions }) => {
  const quizTitle = `Polish Anchor ${Date.now()}`;
  const player = `Aria-${Date.now()}`;

  const host = await hostSessions.adminHost();
  await seedQuiz(host, quizTitle);
  const quizID = makeQuizLive(quizTitle);
  const { joinCode } = await hostSessions.openViaApi(quizID);

  await page.goto(`/join/${joinCode}`);
  await page.getByTestId('join-name-input').fill(player);
  await page.getByTestId('join-name-submit').click();

  // The dedicated lobby quiz-title hook carries the quiz title text and is
  // styled as the prominent header (font-display + extrabold) — a class drift
  // back to the old text-2xl secondary heading fails this assertion.
  const heading = page.getByTestId('lobby-quiz-title');
  await expect(heading).toBeVisible();
  await expect(heading).toContainText(quizTitle);
  await expect(heading).toHaveClass(/font-display/);
  await expect(heading).toHaveClass(/font-extrabold/);
});

test('the home page flips the join CTA to Resume session when a remembered session exists', async ({ page }) => {
  // Seed the remembered-session entry directly: the home page reads
  // localStorage on Alpine init and chooses the CTA off it. Mirrors the
  // shape JoinApp.rememberSession writes (`topbanana.session` -> { code }).
  await page.goto('/');
  await page.evaluate(() => {
    window.localStorage.setItem('topbanana.session', JSON.stringify({ code: 'ABC123' }));
  });
  await page.reload();

  const resume = page.getByTestId('home-resume-session');
  await expect(resume).toBeVisible();
  await expect(resume).toHaveAttribute('href', '/join/ABC123');
  // The Join CTA stays in the DOM as a JS-off fallback (#888): when a
  // remembered session is present Alpine x-show hides it, so assert hidden
  // rather than absent.
  await expect(page.getByTestId('home-join-live')).toBeHidden();

  // The companion "or exit" link clears the entry and falls back to the
  // default "Join a live game" CTA without a navigation.
  await page.getByTestId('home-exit-session').click();
  await expect(page.getByTestId('home-join-live')).toBeVisible();
  await expect(page.getByTestId('home-resume-session')).toBeHidden();
  const remaining = await page.evaluate(() => window.localStorage.getItem('topbanana.session'));
  expect(remaining).toBeNull();
});

test('with no remembered session the home CTA shows the default Join wording', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByTestId('home-join-live')).toBeVisible();
  // Resume branch is mounted but hidden by x-show when no remembered code exists.
  await expect(page.getByTestId('home-resume-session')).toBeHidden();
});

test('the in-session Exit link prompts a confirm modal that the player can cancel', async ({ page, hostSessions }) => {
  const quizTitle = `Polish Exit Cancel ${Date.now()}`;
  const player = `Brin-${Date.now()}`;

  const host = await hostSessions.adminHost();
  await seedQuiz(host, quizTitle);
  const quizID = makeQuizLive(quizTitle);
  const { joinCode } = await hostSessions.openViaApi(quizID);

  await page.goto(`/join/${joinCode}`);
  await page.getByTestId('join-name-input').fill(player);
  await page.getByTestId('join-name-submit').click();

  await expect(page.getByTestId('lobby-view')).toBeVisible();
  // The modal is mounted (Alpine x-show toggles it) but hidden until the
  // exit link opens it.
  await expect(page.getByTestId('exit-session-modal')).toBeHidden();

  await page.getByTestId('exit-session-open').click();
  await expect(page.getByTestId('exit-session-modal')).toBeVisible();

  // Cancel keeps the player in the lobby and closes the modal.
  await page.getByRole('button', { name: 'Cancel' }).click();
  await expect(page.getByTestId('exit-session-modal')).toBeHidden();
  await expect(page.getByTestId('lobby-view')).toBeVisible();
});

test('the in-session Exit confirm drops the player and routes them to the join entry-code screen', async ({ page, hostSessions }) => {
  const quizTitle = `Polish Exit ${Date.now()}`;
  const player = `Cade-${Date.now()}`;

  const host = await hostSessions.adminHost();
  await seedQuiz(host, quizTitle);
  const quizID = makeQuizLive(quizTitle);
  const { joinCode } = await hostSessions.openViaApi(quizID);

  await page.goto(`/join/${joinCode}`);
  await page.getByTestId('join-name-input').fill(player);
  await page.getByTestId('join-name-submit').click();
  await expect(page.getByTestId('lobby-view')).toBeVisible();
  // The remembered-session entry was written on a successful join; it must
  // be cleared by the explicit exit so a subsequent visit doesn't auto-resume.
  const writtenCode = await page.evaluate(() => {
    const raw = window.localStorage.getItem('topbanana.session');
    return raw ? JSON.parse(raw).code : null;
  });
  expect(writtenCode).toBe(joinCode);

  await page.getByTestId('exit-session-open').click();
  await page.getByTestId('exit-session-confirm').click();

  // Land back on the bare enter-code form (no remembered code, so the join
  // app falls through to the typed-code phase).
  await expect(page).toHaveURL(/\/join\/?$/);
  await expect(page.getByTestId('join-code-input')).toBeVisible();

  const cleared = await page.evaluate(() => window.localStorage.getItem('topbanana.session'));
  expect(cleared).toBeNull();

  // The leave POST removed the player's roster row server-side: a fresh
  // GET /state from a new player context shows the lobby without them.
  const verifyResp = await host.request.get(`/api/sessions/${joinCode}/state`);
  expect(verifyResp.ok()).toBeTruthy();
  const state = await verifyResp.json() as { players: { displayName: string }[] };
  expect(state.players.some((p) => p.displayName === player)).toBe(false);
});
