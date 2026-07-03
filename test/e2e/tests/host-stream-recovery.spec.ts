import type { APIRequestContext, Page } from '@playwright/test';

import { test, expect } from './fixtures';
import { importQuiz, setQuizMode, claimAndJoin, playerRow } from './helpers';

// #1179: the host big screen had no recovery from a hard-closed SSE stream. When
// EventSource gives up for good (a fatal non-200, e.g. the server bouncing during
// a deploy) it stops retrying, so no tick ever fires refresh() again and the
// screen freezes on "Reconnecting..." forever. The player surface already solved
// this (#751 / #1121); these specs pin the host half: an auto-backoff
// re-subscribe on a hard close, and a visibility-return re-subscribe.
//
// Both drops are made deterministic by reaching into the Alpine component, the
// same hook the player specs (visibility-reconnect, live-reconnect-recovery) use.

// seedLiveQuiz imports a single-round quiz as the shared admin and flips it to
// mode='live' so it is hostable. A generous 120s answer window keeps the lobby /
// question stable across the injected stream drop instead of timing out.
async function seedLiveQuiz(host: Page, title: string): Promise<void> {
  await importQuiz(host, {
    title,
    description: 'E2E host stream recovery quiz',
    timeLimitSeconds: 120,
    questions: [
      {
        text: 'What is 2+2?',
        options: [
          { text: '4', correct: true },
          { text: 'wrong-a', correct: false },
          { text: 'wrong-b', correct: false },
          { text: 'wrong-c', correct: false },
        ],
      },
    ],
  });
  setQuizMode(title, 'live');
}

// closeHostStream closes the big screen's SSE channel from inside the component,
// mimicking a mobile/TV browser suspending the connection. A closed EventSource
// never reconnects on its own, so no further state reads fire until a recovery
// path re-subscribes.
async function closeHostStream(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const root = document.querySelector('[x-data^="hostBigScreen"]');
    const cmp = (window as unknown as {
      Alpine: { $data: (el: Element) => { source: EventSource | null } };
    }).Alpine.$data(root!);
    const source = cmp.source;
    if (!source) return false;
    source.close();
    return source.readyState === EventSource.CLOSED;
  });
}

// hardCloseHostStream reproduces a fatal SSE close: it closes the stream (so
// readyState is CLOSED) and then fires the error event EventSource raises when it
// gives up permanently. The component's onerror sees CLOSED and schedules the
// backoff re-subscribe - the exact path that used to be a dead end (#1179).
async function hardCloseHostStream(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const root = document.querySelector('[x-data^="hostBigScreen"]');
    const cmp = (window as unknown as {
      Alpine: { $data: (el: Element) => { source: EventSource | null } };
    }).Alpine.$data(root!);
    const source = cmp.source;
    if (!source) return false;
    source.close();
    source.dispatchEvent(new Event('error'));
    return source.readyState === EventSource.CLOSED;
  });
}

async function joinPlayer(request: APIRequestContext, code: string, displayName: string): Promise<void> {
  await claimAndJoin(request, code, displayName);
}

test.describe('host big screen stream recovery', () => {
  test('a hard-closed SSE stream recovers the roster via auto-backoff re-subscribe', async ({ hostSessions }) => {
    test.setTimeout(60_000);

    const stamp = Date.now();
    const quizTitle = `Host Hard Close ${stamp}`;
    // Player names are global on players.display_name (#716), so unique names
    // avoid colliding with a parallel spec on the worker DB.
    const ava = `Ava-${stamp}`;
    const ben = `Ben-${stamp}`;

    const host = await hostSessions.adminHost();
    await seedLiveQuiz(host, quizTitle);

    const { joinCode } = await hostSessions.hostLive(quizTitle);
    expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

    const avaContext = await hostSessions.newPlayerContext();
    await joinPlayer(avaContext.request, joinCode, ava);
    await expect(playerRow(host, ava)).toBeVisible({ timeout: 15_000 });

    // The drop: hard-close the stream so EventSource is done retrying. Without
    // the fix this is terminal - the screen never re-reads state again.
    expect(await hardCloseHostStream(host)).toBe(true);

    // A second player joins while the stream is dead. With no tick reaching the
    // page the roster cannot learn about them until the backoff re-subscribe
    // fires its refresh().
    const benContext = await hostSessions.newPlayerContext();
    await joinPlayer(benContext.request, joinCode, ben);

    // The backoff timer (base 1s) re-reads state and re-opens the stream, so the
    // second player appears - the recovery the unfixed screen never performed.
    await expect(playerRow(host, ben)).toBeVisible({ timeout: 20_000 });
    await expect(playerRow(host, ava)).toBeVisible();
  });

  test('returning to the foreground re-subscribes a dropped stream', async ({ hostSessions }) => {
    test.setTimeout(60_000);

    const stamp = Date.now();
    const quizTitle = `Host Visibility ${stamp}`;
    const ava = `Ada-${stamp}`;
    const ben = `Bex-${stamp}`;

    const host = await hostSessions.adminHost();
    await seedLiveQuiz(host, quizTitle);

    const { joinCode } = await hostSessions.hostLive(quizTitle);
    expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

    const avaContext = await hostSessions.newPlayerContext();
    await joinPlayer(avaContext.request, joinCode, ava);
    await expect(playerRow(host, ava)).toBeVisible({ timeout: 15_000 });

    // Cleanly close the stream (a backgrounded-tab drop, no error event) so only
    // the visibility-return handler can recover it.
    expect(await closeHostStream(host)).toBe(true);

    const benContext = await hostSessions.newPlayerContext();
    await joinPlayer(benContext.request, joinCode, ben);
    // With the stream dead the roster stays stale until the visibility handler runs.
    await expect(playerRow(host, ben)).toHaveCount(0);

    // Return to the foreground: the handler re-reads state and re-opens the
    // dropped stream, so the second player appears.
    await host.evaluate(() => document.dispatchEvent(new Event('visibilitychange')));

    await expect(playerRow(host, ben)).toBeVisible({ timeout: 20_000 });
    await expect(playerRow(host, ava)).toBeVisible();
  });
});
