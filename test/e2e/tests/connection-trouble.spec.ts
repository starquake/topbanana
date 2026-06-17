import { join } from 'node:path';

import { test, expect } from './fixtures';
import {
  seedQuiz,
  claimAndJoin,
  playerRow,
  execSqlite,
  registerForPending,
  markEmailVerified,
  markAdmin,
  login,
  setQuizMode,
} from './helpers';

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
    throw new Error(`makeQuizLive(${title}): could not resolve quiz id from sqlite output ${JSON.stringify(output)}`);
  }
  return id;
}

// #795: after several consecutive non-404 GET /state failures the player lobby
// surfaces a "Connection problem, retrying..." banner so the player knows why
// the roster looks frozen, while it keeps retrying underneath. The banner
// clears on the next good read, and a 404 still flips the closed view (not the
// trouble banner) - the existing room-gone signal is untouched.
test.describe('live client connection trouble', () => {
  test('player lobby surfaces the banner after repeated state failures and clears on success', async ({ page, hostSessions }) => {
    test.setTimeout(60_000);

    const quizTitle = `Conn Player ${Date.now()}`;
    const eve = `Eve-${Date.now()}`;

    const host = await hostSessions.adminHost();
    await seedQuiz(host, quizTitle);
    const quizID = makeQuizLive(quizTitle);
    const { joinCode } = await hostSessions.openViaApi(quizID);

    await page.goto(`/join/${joinCode}`);
    await page.getByTestId('join-name-input').fill(eve);
    await page.getByTestId('join-name-submit').click();
    await expect(page.getByTestId('lobby-view')).toBeVisible();
    await expect(page.getByTestId('connection-trouble')).toHaveCount(0);

    // Force GET /state to fail with a 500 (a non-404 server error). The leave
    // beacon and SSE endpoints are left alone so the lobby stays live; only the
    // authoritative state read fails.
    await page.route(`**/api/sessions/${joinCode}/state`, (route) =>
      route.fulfill({ status: 500, body: 'boom' }),
    );

    // Each return-to-foreground drives one refreshState. Three failures in a
    // row trip the banner (STATE_FAILURE_LIMIT). The roster stays on screen the
    // whole time - this is not the closed view.
    for (let i = 0; i < 3; i++) {
      await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')));
    }
    await expect(page.getByTestId('connection-trouble')).toBeVisible({ timeout: 10_000 });
    await expect(page.getByTestId('connection-trouble')).toContainText('Connection problem');
    // The lobby was NOT torn down: a non-404 failure is not the room-gone signal.
    await expect(page.getByTestId('lobby-closed')).toHaveCount(0);

    // Restore the real state endpoint; the next read clears the banner.
    await page.unroute(`**/api/sessions/${joinCode}/state`);
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')));
    await expect(page.getByTestId('connection-trouble')).toHaveCount(0, { timeout: 10_000 });
    await expect(page.getByTestId('lobby-view')).toBeVisible();
  });

  test('host TV surfaces the banner after repeated state failures and clears on success', async ({ page, hostSessions, browserName }) => {
    test.setTimeout(60_000);

    const displayName = `e2e-conn-host-${browserName}-${Date.now()}`;
    const quizTitle = `Conn Host ${browserName} ${Date.now()}`;
    const fred = `Fred-${browserName}-${Date.now()}`;

    // The host TV is server-rendered behind the host gate, so this test signs in
    // as an admin host (mirroring session-leave.spec.ts) and drives the lobby
    // page directly rather than the anonymous player surface.
    await registerForPending(page, displayName);
    markEmailVerified(displayName);
    markAdmin(displayName);
    await login(page, displayName);
    await expect(page).toHaveURL(/\/admin\/quizzes$/);

    await seedQuiz(page, quizTitle);
    setQuizMode(quizTitle, 'live');

    await page.goto('/admin/quizzes');
    await page.getByRole('link', { name: quizTitle }).click();
    await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
    await page.getByRole('button', { name: 'Host live' }).click();
    await expect(page).toHaveURL(/\/host\/[A-Z0-9]+$/);
    const code = page.url().split('/host/')[1];
    // page is this test's own freshly-registered admin host (not the shared
    // admin the factory opens), so register the room with the factory so
    // teardown ends it through page (#850).
    hostSessions.track(page, code);

    // A player joins so the roster is populated before the failure window.
    const playerContext = await hostSessions.newPlayerContext();
    await claimAndJoin(playerContext.request, code, fred);
    await expect(playerRow(page, fred)).toBeVisible();
    await expect(page.locator('[data-connection-trouble]')).toHaveCount(0);

    // Force GET /state to fail with a 500. The host refresh fires on each SSE
    // tick; dispatching the EventSource onmessage path is awkward, so drive the
    // refresh directly through the Alpine component a few times.
    await page.route(`**/api/sessions/${code}/state`, (route) =>
      route.fulfill({ status: 500, body: 'boom' }),
    );
    for (let i = 0; i < 3; i++) {
      await page.evaluate(() => {
        const root = document.querySelector('[x-data^="hostBigScreen"]');
        const cmp = (window as unknown as { Alpine: { $data: (el: Element) => { refresh: () => Promise<void> } } }).Alpine.$data(root!);
        return cmp.refresh();
      });
    }
    await expect(page.locator('[data-connection-trouble]')).toBeVisible({ timeout: 10_000 });
    await expect(page.locator('[data-connection-trouble]')).toContainText('Connection problem');

    // Restore the endpoint; the next refresh clears the banner.
    await page.unroute(`**/api/sessions/${code}/state`);
    await page.evaluate(() => {
      const root = document.querySelector('[x-data^="hostBigScreen"]');
      const cmp = (window as unknown as { Alpine: { $data: (el: Element) => { refresh: () => Promise<void> } } }).Alpine.$data(root!);
      return cmp.refresh();
    });
    await expect(page.locator('[data-connection-trouble]')).toHaveCount(0, { timeout: 10_000 });
  });

  // A 404 on GET /state means the session was ended/deleted out from under the
  // big screen. That is terminal, not a retryable connection fault, so it
  // surfaces a "Session ended" signal in the footer status area instead of
  // leaving the last phase frozen with no operator feedback.
  test('host TV surfaces a session-ended signal when state 404s', async ({ page, hostSessions, browserName }) => {
    test.setTimeout(60_000);

    const displayName = `e2e-gone-host-${browserName}-${Date.now()}`;
    const quizTitle = `Gone Host ${browserName} ${Date.now()}`;

    await registerForPending(page, displayName);
    markEmailVerified(displayName);
    markAdmin(displayName);
    await login(page, displayName);
    await expect(page).toHaveURL(/\/admin\/quizzes$/);

    await seedQuiz(page, quizTitle);
    setQuizMode(quizTitle, 'live');

    await page.goto('/admin/quizzes');
    await page.getByRole('link', { name: quizTitle }).click();
    await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
    await page.getByRole('button', { name: 'Host live' }).click();
    await expect(page).toHaveURL(/\/host\/[A-Z0-9]+$/);
    const code = page.url().split('/host/')[1];
    hostSessions.track(page, code);

    await expect(page.getByTestId('session-gone')).toHaveCount(0);

    // Force GET /state to 404 (the session is gone), then drive a refresh
    // through the Alpine component directly (the SSE onmessage path is awkward
    // to trigger from the test), mirroring the trouble-banner specs above.
    await page.route(`**/api/sessions/${code}/state`, (route) =>
      route.fulfill({ status: 404, body: 'not found' }),
    );
    await page.evaluate(() => {
      const root = document.querySelector('[x-data^="hostBigScreen"]');
      const cmp = (window as unknown as { Alpine: { $data: (el: Element) => { refresh: () => Promise<void> } } }).Alpine.$data(root!);
      return cmp.refresh();
    });

    await expect(page.getByTestId('session-gone')).toBeVisible({ timeout: 10_000 });
    await expect(page.getByTestId('session-gone')).toContainText('Session ended');
    // The terminal signal replaces the live/reconnecting dot and the trouble
    // banner, not sits alongside them.
    await expect(page.locator('[data-connection-trouble]')).toHaveCount(0);
  });
});
