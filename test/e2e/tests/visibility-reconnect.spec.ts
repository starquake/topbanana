import { join } from 'node:path';

import { adminStatePath } from '../e2e-auth';
import { test, expect } from './fixtures';
import { seedQuiz, claimAndJoin, QUIZ_QUESTIONS, execSqlite, endHostedSession } from './helpers';

// makeQuizLive flips a seeded quiz to mode='live' (the importer lands quizzes
// on 'solo', and only live quizzes are hostable, MP-0 / #677) and returns its
// id so the test can open a session for it. Mirrors the sqlite3 shortcut the
// other live specs use rather than driving an admin live-mode toggle that does
// not exist in the seeded-import path.
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

// #751: a mobile player who backgrounds the tab and returns finds the SSE
// channel suspended/closed, so no further state reads fire and the roster goes
// stale. On returning to the foreground the lobby must re-read state and
// re-open the dropped stream so the roster repopulates.
//
// This reproduces the drop deterministically: the test reaches into the Alpine
// component, closes the EventSource (the same dead-socket state a backgrounded
// mobile tab lands in - a closed EventSource never reconnects on its own), adds
// a new player via the API while the stream is dead (so no SSE tick reaches the
// page and the roster cannot update), confirms the roster is stale, then fires
// the visibilitychange the browser raises on return. The fix's handler re-reads
// state and re-subscribes, so the new player appears.
test.describe('lobby visibility reconnect', () => {
  test('returning to a backgrounded tab repopulates a stale roster', async ({ page, baseURL }) => {
    test.setTimeout(60_000);

    const quizTitle = `Visibility Live ${Date.now()}`;
    // Player names are global on players.display_name (#716), so unique names
    // avoid colliding with a parallel spec on the worker DB.
    const ava = `Ava-${Date.now()}`;
    const ben = `Ben-${Date.now()}`;
    const cleo = `Leo-${Date.now()}`;

    // Host side: seed the quiz, make it live, open a session as the admin in
    // its own context so the player page stays anonymous.
    const hostContext = await page.context().browser()!.newContext({ storageState: adminStatePath(), baseURL });
    const host = await hostContext.newPage();

    await seedQuiz(host, quizTitle);
    const quizID = makeQuizLive(quizTitle);

    const createResp = await host.request.post('/api/sessions', { data: { quizId: quizID } });
    expect(createResp.status(), `create session: ${createResp.status()} ${await createResp.text()}`).toBe(201);
    const { joinCode } = await createResp.json() as { joinCode: string };
    expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

    // A second player (API-only, own anonymous context) is already in the lobby
    // before the page player joins, so the page roster starts with two names.
    const benContext = await page.context().browser()!.newContext({ storageState: undefined, baseURL });
    await claimAndJoin(benContext.request, joinCode, ben);

    // Page player joins via the deep link and lands in the lobby.
    await page.goto(`/join/${joinCode}`);
    await page.getByTestId('join-name-input').fill(ava);
    await page.getByTestId('join-name-submit').click();

    const roster = page.getByTestId('lobby-roster');
    await expect(roster.getByText(ava)).toBeVisible();
    await expect(roster.getByText(ben)).toBeVisible();

    // Close the live SSE channel from inside the component, mimicking a mobile
    // browser suspending the connection while the tab is backgrounded. A closed
    // EventSource never reconnects on its own, so no further ticks arrive. This
    // reaches the eventSource handle directly (not a fix-only helper) so the
    // assertions below fail against the unfixed code, pinning the regression.
    const dropped = await page.evaluate(() => {
      const root = document.querySelector('[x-data="joinApp"]');
      // window.Alpine is the vendored global; $data returns the component.
      const cmp = (window as unknown as { Alpine: { $data: (el: Element) => { eventSource: EventSource | null } } }).Alpine.$data(root!);
      const source = cmp.eventSource;
      if (!source) return false;
      source.close();
      return source.readyState === EventSource.CLOSED;
    });
    expect(dropped).toBe(true);

    // A third player joins while the stream is dead. With no SSE tick reaching
    // the page, the roster cannot learn about them - it stays stale.
    const cleoContext = await page.context().browser()!.newContext({ storageState: undefined, baseURL });
    await claimAndJoin(cleoContext.request, joinCode, cleo);

    await expect(roster.getByText(cleo)).toHaveCount(0);

    // Return to the foreground: dispatch the visibilitychange the browser
    // raises on tab refocus (the page is already visible in Playwright, so the
    // handler's visible-state guard passes). The recovery re-reads state and
    // re-opens the stream, so the third player appears.
    await page.evaluate(() => {
      document.dispatchEvent(new Event('visibilitychange'));
    });

    await expect(roster.getByText(cleo)).toBeVisible({ timeout: 15_000 });
    await expect(roster.getByText(ava)).toBeVisible();
    await expect(roster.getByText(ben)).toBeVisible();

    await endHostedSession(host, joinCode);
    await cleoContext.close();
    await benContext.close();
    await hostContext.close();
  });

  // #751 (same root cause): a player who backgrounds the tab in the lobby and
  // returns AFTER the host has started misses the round_intro -> question SSE
  // ticks, so without recovery they stay stuck on the lobby roster. The
  // visibility handler re-reads state through the same refreshState the live
  // tick uses, which is what drives the phase view AND re-arms the per-question
  // countdown off the server deadline - so the recovered player must land on
  // the live question, not the lobby.
  //
  // The drop is made deterministic the same way as the roster test: close the
  // EventSource from inside the component (a closed EventSource never
  // reconnects on its own) BEFORE the host starts, so the page genuinely misses
  // every transition tick and is still rendering the lobby. The
  // visibilitychange the browser raises on return then drives the recovery.
  test('returning after the host starts advances off the lobby to the live question', async ({ page, baseURL }) => {
    test.setTimeout(60_000);

    const quizTitle = `Visibility Start ${Date.now()}`;
    // Player names are global on players.display_name (#716), so unique names
    // avoid colliding with a parallel spec on the worker DB.
    const ava = `Ada-${Date.now()}`;
    const ben = `Bex-${Date.now()}`;

    // Host side: seed the quiz, make it live, open a session as the admin in
    // its own context so the player page stays anonymous.
    const hostContext = await page.context().browser()!.newContext({ storageState: adminStatePath(), baseURL });
    const host = await hostContext.newPage();

    await seedQuiz(host, quizTitle);
    const quizID = makeQuizLive(quizTitle);

    const createResp = await host.request.post('/api/sessions', { data: { quizId: quizID } });
    expect(createResp.status(), `create session: ${createResp.status()} ${await createResp.text()}`).toBe(201);
    const { joinCode } = await createResp.json() as { joinCode: string };
    expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

    // A second player (API-only, own anonymous context) keeps the question open
    // after the runner issues it: the runner early-closes only once every
    // active player has answered, and this player holds their answer, so the
    // returning page player still finds the question phase live on recovery.
    const benContext = await page.context().browser()!.newContext({ storageState: undefined, baseURL });
    await claimAndJoin(benContext.request, joinCode, ben);

    // Page player joins via the deep link and lands in the lobby.
    await page.goto(`/join/${joinCode}`);
    await page.getByTestId('join-name-input').fill(ava);
    await page.getByTestId('join-name-submit').click();
    await expect(page.getByTestId('lobby-view')).toBeVisible();
    await expect(page.getByTestId('lobby-roster').getByText(ava)).toBeVisible();

    // Background the tab: close the live SSE channel from inside the component
    // BEFORE the host starts, so the page misses the round_intro -> question
    // ticks entirely. A closed EventSource never reconnects, so no further
    // state reads fire. This reaches the eventSource handle directly (not a
    // fix-only helper) so the assertion below fails against unfixed code.
    const dropped = await page.evaluate(() => {
      const root = document.querySelector('[x-data="joinApp"]');
      // window.Alpine is the vendored global; $data returns the component.
      const cmp = (window as unknown as { Alpine: { $data: (el: Element) => { eventSource: EventSource | null } } }).Alpine.$data(root!);
      const source = cmp.eventSource;
      if (!source) return false;
      source.close();
      return source.readyState === EventSource.CLOSED;
    });
    expect(dropped).toBe(true);

    // Host starts the session. The runner drives round_intro -> question on its
    // own beat, but with the stream dead the page receives none of it.
    const startResp = await host.request.post(`/api/sessions/${joinCode}/start`);
    expect(startResp.status(), `start session: ${startResp.status()} ${await startResp.text()}`).toBe(204);

    // With no SSE tick reaching the page it is still stuck on the lobby roster:
    // the live question has not appeared. This is the symptom the owner hit.
    await expect(page.getByTestId('lobby-view')).toBeVisible();
    await expect(page.getByTestId('round-intro')).toHaveCount(0);
    await expect(page.getByTestId('question-view')).toHaveCount(0);

    // Return to the foreground: dispatch the visibilitychange the browser
    // raises on tab refocus (the page is already visible in Playwright, so the
    // handler's visible-state guard passes). The recovery re-reads state - the
    // same path the live tick uses - so the phase advances off the lobby and
    // the per-question countdown re-arms off the server deadline.
    await page.evaluate(() => {
      document.dispatchEvent(new Event('visibilitychange'));
    });

    // The player advances off the lobby to the live question (the runner has
    // already moved past round_intro by the time recovery lands; the question
    // view is the destination). The lobby roster is gone.
    await expect(page.getByTestId('question-view')).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId('question-text')).toHaveText(QUIZ_QUESTIONS[0].text);
    await expect(page.getByTestId('lobby-view')).toHaveCount(0);

    await endHostedSession(host, joinCode);
    await benContext.close();
    await hostContext.close();
  });
});
