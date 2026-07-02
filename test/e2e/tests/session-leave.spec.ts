import { join } from 'node:path';

import { test, expect } from './fixtures';
import {
  registerForPending,
  markEmailVerified,
  markAdmin,
  login,
  seedQuiz,
  setQuizMode,
  claimAndJoin,
  execSqlite,
  waitForHostRoom,
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

// MP-10 (#687): when a player leaves, their row drops out of the live roster
// on the host/TV surface at once. Two players join from separate anonymous
// contexts; one leaves and the TV roster, which updates off the SSE-tick ->
// GET /state refresh, drops by one. The leave is driven through the REST
// endpoint directly: navigator.sendBeacon fires only on tab unload, which is
// awkward to trigger deterministically in Playwright, and the endpoint is the
// exact request the beacon issues.
test('a player leaving drops out of the host roster live', async ({
  page,
  hostSessions,
  browserName,
}) => {
  const displayName = `e2e-leave-host-${browserName}`;
  const quizTitle = `E2E Session Leave ${browserName}`;

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

  const code = await waitForHostRoom(page);
  // page is this test's own freshly-registered admin host (not the shared admin
  // the factory opens), so register the room with the factory so teardown ends
  // it through page and the dashboard returns hostable for the next test (#850).
  hostSessions.track(page, code);

  // Player names are global on players.display_name now (#716), so use unique
  // names to avoid colliding with a parallel spec on the worker DB.
  const alice = `Alice-${browserName}-${Date.now()}`;
  const bob = `Bob-${browserName}-${Date.now()}`;
  const aliceContext = await hostSessions.newPlayerContext();
  const bobContext = await hostSessions.newPlayerContext();
  await claimAndJoin(aliceContext.request, code, alice);
  await claimAndJoin(bobContext.request, code, bob);

  // Both rows show on the TV once the join ticks land.
  const roster = page.locator('[data-player-row]');
  await expect(roster).toHaveCount(2);

  // Alice leaves; the endpoint accepts an empty body (the sendBeacon shape).
  const leaveResp = await aliceContext.request.post(`/api/sessions/${code}/leave`);
  expect(leaveResp.status()).toBe(204);

  // The TV roster drops to just Bob without a reload.
  await expect(roster).toHaveCount(1);
  await expect(roster.first()).toContainText(bob);
});

// #794: the leave beacon must fire on pagehide, not only beforeunload, because
// beforeunload is unreliable on mobile (a backgrounded tab the OS discards
// never raises it). A player joins via the page, then pagehide is dispatched;
// the component must sendBeacon to the leave endpoint. The guard against a
// double-send is pinned by dispatching pagehide twice and asserting the beacon
// went out exactly once.
test('the leave beacon fires once on pagehide', async ({ page, hostSessions }) => {
  test.setTimeout(60_000);

  const quizTitle = `Leave Pagehide ${Date.now()}`;
  const dana = `Dana-${Date.now()}`;

  // Spy on navigator.sendBeacon before any page script runs, recording every
  // URL it is called with so the test can assert the leave went out (and only
  // once). Keep the real send so the server-side leave still happens.
  await page.addInitScript(() => {
    const calls: string[] = [];
    (window as unknown as { __beacons: string[] }).__beacons = calls;
    const real = navigator.sendBeacon ? navigator.sendBeacon.bind(navigator) : null;
    navigator.sendBeacon = (url: string | URL, data?: BodyInit | null) => {
      calls.push(String(url));
      return real ? real(url, data ?? null) : true;
    };
  });

  const host = await hostSessions.adminHost();
  await seedQuiz(host, quizTitle);
  const quizID = makeQuizLive(quizTitle);
  const { joinCode } = await hostSessions.openViaApi(quizID);

  await page.goto(`/join/${joinCode}`);
  await page.getByTestId('join-name-input').fill(dana);
  await page.getByTestId('join-name-submit').click();
  await expect(page.getByTestId('lobby-roster').getByText(dana)).toBeVisible();

  // Dispatch pagehide twice. The leave beacon must fire exactly once: the
  // component's leftSent guard collapses the second event so the server gets a
  // single (idempotent) leave.
  await page.evaluate(() => {
    window.dispatchEvent(new Event('pagehide'));
    window.dispatchEvent(new Event('pagehide'));
  });

  const beacons = await page.evaluate(
    () => (window as unknown as { __beacons: string[] }).__beacons,
  );
  const leaveBeacons = beacons.filter((url) => url.includes(`/api/sessions/${joinCode}/leave`));
  expect(leaveBeacons).toHaveLength(1);
});
