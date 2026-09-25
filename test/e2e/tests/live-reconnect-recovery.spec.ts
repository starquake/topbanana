import { join } from 'node:path';

import { test, expect } from './fixtures';
import { seedQuiz, claimAndJoin, execSqlite, waitForAlpineComponent } from './helpers';

// makeQuizLive flips a seeded quiz to mode='live' (the importer lands quizzes
// on 'solo', and only live quizzes are hostable, MP-0 / #677) and returns its
// id so the test can open a session for it. Mirrors the sqlite3 shortcut the
// other live specs use.
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

// deleteSession removes a live session row by join code, the server-side way to
// make GET /state 404 for a still-joined player (the session is gone: ended,
// expired, or the player removed). GetSessionByJoinCode then returns
// ErrSessionNotFound, which the state handler maps to 404 and the client maps to
// the terminal closed view. Join codes are [A-Z0-9]{6} so safe to interpolate.
function deleteSession(joinCode: string): void {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; cannot delete a session');
  }
  const dbFile = join(dataDir, `e2e-${test.info().parallelIndex}.db`);
  const output = execSqlite(
    dbFile,
    `DELETE FROM sessions WHERE join_code = '${joinCode}'; SELECT changes();`,
  );
  const changed = Number.parseInt(output, 10);
  if (changed !== 1) {
    throw new Error(`deleteSession(${joinCode}): expected 1 row deleted, got ${changed}`);
  }
}

// dropStream closes the page's live SSE channel from inside the Alpine
// component, mimicking a mobile browser suspending the connection while the tab
// is backgrounded. A closed EventSource never reconnects on its own, so no
// further state reads fire and the roster goes stale until something forces a
// recovery. Reaches the eventSource handle directly (not a fix-only helper), the
// same hook visibility-reconnect.spec.ts uses.
async function dropStream(page: import('./fixtures').Page): Promise<boolean> {
  await waitForAlpineComponent(page, '[x-data="joinApp"]', 'eventSource');
  return page.evaluate(() => {
    const root = document.querySelector('[x-data="joinApp"]');
    const cmp = (window as unknown as {
      Alpine: { $data: (el: Element) => { eventSource: EventSource | null } };
    }).Alpine.$data(root!);
    const source = cmp.eventSource;
    if (!source) return false;
    source.close();
    return source.readyState === EventSource.CLOSED;
  });
}

// #1121: the live player lobby has two recovery dead-ends. A wedged connection
// surfaces the "Connection problem, retrying..." banner but offers no manual
// override, and a gone session surfaces "This game is no longer available." with
// no next action. These specs pin the two new controls: a "Reconnect now" button
// that forces an immediate re-subscribe + state re-read, and a "Back to join"
// link that returns the player to the entry screen.
// hardCloseStream reproduces a fatal SSE close (e.g. a proxy 502 during a
// deploy): EventSource ends CLOSED and raises the error event it fires when it
// gives up for good.
async function hardCloseStream(page: import('./fixtures').Page): Promise<boolean> {
  await waitForAlpineComponent(page, '[x-data="joinApp"]', 'eventSource');
  return page.evaluate(() => {
    const root = document.querySelector('[x-data="joinApp"]');
    const cmp = (window as unknown as {
      Alpine: { $data: (el: Element) => { eventSource: EventSource | null } };
    }).Alpine.$data(root!);
    const source = cmp.eventSource;
    if (!source) return false;
    source.close();
    source.dispatchEvent(new Event('error'));
    return source.readyState === EventSource.CLOSED;
  });
}

test.describe('live reconnect and recovery', () => {
  // #1342: a hard-closed stream used to leave the live view frozen with no banner.
  test('a hard-closed stream shows the banner and recovers via backoff re-subscribe', async ({ page, hostSessions }) => {
    test.setTimeout(60_000);

    const stamp = Date.now();
    const quizTitle = `Hard Close ${stamp}`;
    const ava = `Ava-${stamp}`;
    const ben = `Ben-${stamp}`;

    const host = await hostSessions.adminHost();
    await seedQuiz(host, quizTitle);
    const quizID = makeQuizLive(quizTitle);
    const { joinCode } = await hostSessions.openViaApi(quizID);

    await page.goto(`/join/${joinCode}`);
    await page.getByTestId('join-name-input').fill(ava);
    await page.getByTestId('join-name-submit').click();
    await expect(page.getByTestId('lobby-view')).toBeVisible();
    const roster = page.getByTestId('lobby-roster');
    await expect(roster.getByText(ava)).toBeVisible();

    // Hold GET /state failing so the banner stays up until the stream is back.
    await page.route(`**/api/sessions/${joinCode}/state`, (route) =>
      route.fulfill({ status: 502, body: 'bad gateway' }),
    );
    expect(await hardCloseStream(page)).toBe(true);
    await expect(page.getByTestId('connection-trouble')).toBeVisible();

    const benContext = await hostSessions.newPlayerContext();
    await claimAndJoin(benContext.request, joinCode, ben);
    await page.unroute(`**/api/sessions/${joinCode}/state`);

    // The backoff timer re-opens the stream and re-reads state with no
    // foreground return or tap, so the second player appears.
    await expect(roster.getByText(ben)).toBeVisible({ timeout: 20_000 });
    await expect(page.getByTestId('connection-trouble')).toHaveCount(0, { timeout: 10_000 });
    await expect.poll(() => page.evaluate(() => {
      const root = document.querySelector('[x-data="joinApp"]');
      const cmp = (window as unknown as {
        Alpine: { $data: (el: Element) => { eventSource: EventSource | null } };
      }).Alpine.$data(root!);
      return cmp.eventSource !== null && cmp.eventSource.readyState !== EventSource.CLOSED;
    })).toBe(true);
  });

  test('the Reconnect now control recovers the live view after the stream drops', async ({ page, hostSessions }) => {
    test.setTimeout(60_000);

    const quizTitle = `Reconnect Now ${Date.now()}`;
    // Player names are global on players.display_name (#716), so unique names
    // avoid colliding with a parallel spec on the worker DB.
    const ava = `Ava-${Date.now()}`;
    const ben = `Ben-${Date.now()}`;

    const host = await hostSessions.adminHost();
    await seedQuiz(host, quizTitle);
    const quizID = makeQuizLive(quizTitle);
    const { joinCode } = await hostSessions.openViaApi(quizID);
    expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

    // Page player joins via the deep link and lands in the lobby.
    await page.goto(`/join/${joinCode}`);
    await page.getByTestId('join-name-input').fill(ava);
    await page.getByTestId('join-name-submit').click();
    await expect(page.getByTestId('lobby-view')).toBeVisible();
    const roster = page.getByTestId('lobby-roster');
    await expect(roster.getByText(ava)).toBeVisible();
    await expect(page.getByTestId('connection-trouble')).toHaveCount(0);

    // Fail GET /state with a 500 (a non-404 server error). Each return-to-
    // foreground drives one refreshState; three failures in a row trip the
    // trouble banner (STATE_FAILURE_LIMIT). The leave/SSE endpoints are left
    // alone so only the authoritative read fails.
    await page.route(`**/api/sessions/${joinCode}/state`, (route) =>
      route.fulfill({ status: 500, body: 'boom' }),
    );
    // Wait out each read: overlapping reads coalesce, so a burst counts as fewer failures.
    for (let i = 0; i < 3; i++) {
      await page.evaluate(async () => {
        document.dispatchEvent(new Event('visibilitychange'));
        const root = document.querySelector('[x-data="joinApp"]');
        const cmp = (window as unknown as {
          Alpine: { $data: (el: Element) => { stateRead: Promise<void> | null } };
        }).Alpine.$data(root!);
        if (cmp.stateRead) await cmp.stateRead;
      });
    }
    await expect(page.getByTestId('connection-trouble')).toBeVisible({ timeout: 10_000 });
    // A non-404 failure is the connection-trouble signal, not the room-gone one.
    await expect(page.getByTestId('lobby-closed')).toHaveCount(0);

    // Drop the SSE stream so no automatic tick can recover the view: from here
    // only the manual "Reconnect now" control re-subscribes and re-reads. This
    // is what pins the new button - without it the roster never updates.
    expect(await dropStream(page)).toBe(true);

    // A second player joins while the page is disconnected. With the stream dead
    // and /state failing, the page cannot learn about them - the roster stays
    // stale.
    const benContext = await hostSessions.newPlayerContext();
    await claimAndJoin(benContext.request, joinCode, ben);
    await expect(roster.getByText(ben)).toHaveCount(0);

    // Restore the real state endpoint, then force the recovery manually. The
    // button re-subscribes and re-reads, so the new player appears and the
    // banner clears.
    await page.unroute(`**/api/sessions/${joinCode}/state`);
    await expect(page.getByTestId('reconnect-now')).toBeVisible();
    await page.getByTestId('reconnect-now').click();

    await expect(roster.getByText(ben)).toBeVisible({ timeout: 15_000 });
    await expect(roster.getByText(ava)).toBeVisible();
    await expect(page.getByTestId('connection-trouble')).toHaveCount(0, { timeout: 10_000 });
  });

  test('a gone session lands the player on an actionable closed state', async ({ page, hostSessions }) => {
    test.setTimeout(60_000);

    const quizTitle = `Closed Recovery ${Date.now()}`;
    const cleo = `Cleo-${Date.now()}`;

    const host = await hostSessions.adminHost();
    await seedQuiz(host, quizTitle);
    const quizID = makeQuizLive(quizTitle);
    const { joinCode } = await hostSessions.openViaApi(quizID);
    expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

    await page.goto(`/join/${joinCode}`);
    await page.getByTestId('join-name-input').fill(cleo);
    await page.getByTestId('join-name-submit').click();
    await expect(page.getByTestId('lobby-view')).toBeVisible();
    await expect(page.getByTestId('lobby-closed')).toHaveCount(0);

    // Remove the session server-side. The next authoritative read 404s, which
    // is the room-gone signal that flips the terminal closed view.
    deleteSession(joinCode);
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')));

    // The player lands on the closed view with a clear next action, not a dead
    // banner.
    await expect(page.getByTestId('lobby-closed')).toBeVisible({ timeout: 10_000 });
    await expect(page.getByTestId('lobby-closed')).toContainText('no longer available');
    const back = page.getByTestId('lobby-closed-back');
    await expect(back).toBeVisible();

    // Back to join returns the player to the enter-code screen, where they can
    // join a live room (the remembered session was cleared on close, so the
    // entry form shows fresh).
    await back.click();
    // The bare entry canonicalizes to /join/ (trailing slash); the enter-code
    // form is what proves the player landed back on the entry screen.
    await expect(page).toHaveURL(/\/join\/?$/);
    await expect(page.getByTestId('join-code-input')).toBeVisible();
  });
});
