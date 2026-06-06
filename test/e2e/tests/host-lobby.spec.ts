import { test, expect } from './fixtures';
import {
  registerForPending,
  markEmailVerified,
  markAdmin,
  login,
  seedQuiz,
  setQuizMode,
  claimAndJoin,
} from './helpers';

// MP-3 (#680): the host puts a live quiz on a TV and gets a lobby with the
// join QR + room code, then watches players appear and ready up live. MP-4's
// player join UI does not exist yet, so the player side is driven through the
// REST API (POST /join, /ready) from a separate anonymous context; the test
// asserts the TV view updates off the SSE-tick -> GET /state refresh.
test('host lobby shows the room code, QR, and a joined player readying up live', async ({
  page,
  context,
  baseURL,
  browserName,
}) => {
  const displayName = `e2e-host-${browserName}`;
  const quizTitle = `E2E Host Lobby ${browserName}`;

  // Register, promote to admin explicitly (the worker DB already has the
  // seeded credentialled admin, so a fresh registrant stays a plain player
  // and would not reach the admin dashboard), then sign in.
  await registerForPending(page, displayName);
  markEmailVerified(displayName);
  markAdmin(displayName);
  await login(page, displayName);
  await expect(page).toHaveURL(/\/admin\/quizzes$/);

  await seedQuiz(page, quizTitle);
  // The importer only creates solo quizzes; a live quiz is hostable.
  setQuizMode(quizTitle, 'live');

  // Open the quiz view and click "Play live" to open a session. The button
  // posts to /host and the server redirects to the TV lobby at /host/{code}.
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  await page.getByRole('button', { name: 'Play live' }).click();

  await expect(page).toHaveURL(/\/host\/[A-Z0-9]+$/);
  const code = page.url().split('/host/')[1];
  expect(code).toMatch(/^[A-Z0-9]+$/);

  // Lobby chrome: the scan card, a server-rendered QR svg, and the big code.
  await expect(page.getByText('Scan to join')).toBeVisible();
  await expect(page.locator('svg[aria-label="Join QR code"]')).toBeVisible();
  await expect(page.getByText(code, { exact: true })).toBeVisible();
  await expect(page.getByText('Waiting for players to join...')).toBeVisible();

  // A player joins from a fresh anonymous context via the REST API (MP-4
  // owns the join UI). The context gets its own session cookie, so the
  // server mints a distinct anonymous player for it.
  // Player names are global on players.display_name now (#716), so use a
  // unique name to avoid colliding with a parallel spec on the worker DB.
  const casey = `Casey-${browserName}-${Date.now()}`;
  const playerContext = await context.browser()!.newContext({ storageState: undefined, baseURL });
  try {
    await claimAndJoin(playerContext.request, code, casey);

    // The TV roster updates live off the SSE tick -> GET /state refresh.
    const roster = page.locator('[data-player-row]');
    await expect(roster).toHaveCount(1);
    await expect(roster.first()).toContainText(casey);
    await expect(roster.first().locator('[data-ready-state]')).toHaveText('Not ready');

    // The player readies up; the TV reflects it without a reload.
    const readyResp = await playerContext.request.post(`/api/sessions/${code}/ready`, {
      data: { ready: true },
    });
    expect(readyResp.status()).toBe(204);

    await expect(roster.first().locator('[data-ready-state]')).toHaveText('Ready');
  } finally {
    await playerContext.close();
  }
});
