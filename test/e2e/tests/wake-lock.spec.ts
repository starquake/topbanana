import { join } from 'node:path';

import { adminStatePath } from '../e2e-auth';
import { test, expect } from './fixtures';
import { seedQuiz, claimAndJoin, QUIZ_QUESTIONS, execSqlite } from './helpers';

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

// stubWakeLock installs a fake navigator.wakeLock BEFORE any page script runs,
// recording every request and release so the test can assert the lifecycle
// without depending on the real OS API (which Playwright does not grant a
// genuine screen lock for, and which would make the assertions environment
// dependent). The stub mirrors the real shape closely enough for the component:
// request('screen') resolves a sentinel with a release() that resolves and
// dispatches the 'release' event, and an OS-style auto-release is exposed via
// window.__wakeLock.osRelease() so the test can simulate the browser dropping
// the lock when the tab hides.
//
// __wakeLock.requests counts request() calls; .releases counts release() calls;
// .held reports whether a sentinel is currently un-released.
async function stubWakeLock(page: import('./fixtures').Page): Promise<void> {
  await page.addInitScript(() => {
    const state = {
      requests: 0,
      releases: 0,
      held: false,
      // True once the component has attached its 'release' listener to the
      // current sentinel (i.e. its acquireWakeLock .then has run). The test
      // waits on this before simulating an OS release so the listener is in
      // place to null the held sentinel - otherwise the re-acquire races the
      // pending request resolution.
      listenerReady: false,
      current: null as null | { dispatch: () => void },
    };
    (window as unknown as { __wakeLock: typeof state }).__wakeLock = state;

    const makeSentinel = () => {
      const listeners: Array<() => void> = [];
      let released = false;
      const sentinel = {
        released,
        type: 'screen',
        addEventListener(_type: string, cb: () => void) {
          listeners.push(cb);
          state.listenerReady = true;
        },
        removeEventListener() {
          // no-op
        },
        release() {
          if (!released) {
            released = true;
            state.releases += 1;
            state.held = false;
            if (state.current === record) state.current = null;
            listeners.forEach((cb) => cb());
          }
          return Promise.resolve();
        },
      };
      // record exposes a dispatch() the OS-release helper uses to fire the
      // 'release' event WITHOUT counting a deliberate component release.
      const record = {
        dispatch() {
          if (!released) {
            released = true;
            state.held = false;
            if (state.current === record) state.current = null;
            listeners.forEach((cb) => cb());
          }
        },
        sentinel,
      };
      return record;
    };

    // Chromium ships a real navigator.wakeLock as a read-only property, so a
    // plain assignment is silently dropped - define it so the stub actually
    // takes over on every engine.
    Object.defineProperty(navigator, 'wakeLock', {
      configurable: true,
      value: {
        request(type: string) {
          if (type !== 'screen') return Promise.reject(new Error('unsupported wake lock type'));
          state.requests += 1;
          state.held = true;
          state.listenerReady = false;
          const record = makeSentinel();
          state.current = record;
          return Promise.resolve(record.sentinel);
        },
      },
    });

    // osRelease simulates the browser auto-releasing the lock when the tab
    // hides: it fires the sentinel's 'release' event (which the component
    // listens for to null its held sentinel) without counting a component
    // release, so a later acquireWakeLock re-requests.
    (window as unknown as { __wakeLock: typeof state & { osRelease: () => void } }).__wakeLock.osRelease = () => {
      if (state.current) state.current.dispatch();
    };
  });
}

type WakeLockState = { requests: number; releases: number; held: boolean; listenerReady: boolean };

function readWakeLock(page: import('./fixtures').Page): Promise<WakeLockState> {
  return page.evaluate(() => {
    const s = (window as unknown as { __wakeLock: WakeLockState }).__wakeLock;
    return { requests: s.requests, releases: s.releases, held: s.held, listenerReady: s.listenerReady };
  });
}

// #760 (QoL 2): the live player's screen is kept awake via the Screen Wake Lock
// API while they are in a game. The lock is acquired when they enter the lobby
// off a user gesture, re-acquired when they return to a backgrounded tab (the
// OS auto-releases it on hide), and released when the game finishes. The real
// API is feature-detected and best-effort, so these assertions run against a
// stubbed navigator.wakeLock rather than the genuine OS lock.
test.describe('live player screen wake lock', () => {
  test('acquires the wake lock on entering the lobby and re-acquires on return to foreground', async ({ page, baseURL }) => {
    test.setTimeout(60_000);

    const quizTitle = `Wake Lobby ${Date.now()}`;
    const ada = `Ada-${Date.now()}`;

    await stubWakeLock(page);

    const hostContext = await page.context().browser()!.newContext({ storageState: adminStatePath(), baseURL });
    const host = await hostContext.newPage();
    await seedQuiz(host, quizTitle);
    const quizID = makeQuizLive(quizTitle);
    const createResp = await host.request.post('/api/sessions', { data: { quizId: quizID } });
    expect(createResp.status(), `create session: ${createResp.status()} ${await createResp.text()}`).toBe(201);
    const { joinCode } = await createResp.json() as { joinCode: string };

    // No lock yet on the name form: the request only fires once the player has
    // entered the lobby (the join/ready gesture).
    await page.goto(`/join/${joinCode}`);
    await expect(page.getByTestId('join-name-input')).toBeVisible();
    expect((await readWakeLock(page)).requests).toBe(0);

    // Joining lands the player in the lobby and acquires the lock. Wait until
    // the component has taken ownership of the sentinel (its release listener
    // is attached) so the simulated OS release below can null the held sentinel
    // instead of racing the pending request resolution.
    await page.getByTestId('join-name-input').fill(ada);
    await page.getByTestId('join-name-submit').click();
    await expect(page.getByTestId('lobby-roster').getByText(ada)).toBeVisible();
    await expect.poll(async () => (await readWakeLock(page)).requests).toBe(1);
    await expect.poll(async () => (await readWakeLock(page)).listenerReady).toBe(true);
    expect((await readWakeLock(page)).held).toBe(true);

    // Simulate the OS auto-releasing the lock while the tab is backgrounded,
    // then return to the foreground. handleVisible re-acquires it, so the
    // request count climbs and the lock is held again. The deliberate-release
    // counter stays at 0 - the OS drop is not a component release.
    await page.evaluate(() => {
      (window as unknown as { __wakeLock: { osRelease: () => void } }).__wakeLock.osRelease();
    });
    await expect.poll(async () => (await readWakeLock(page)).held).toBe(false);

    await page.evaluate(() => {
      document.dispatchEvent(new Event('visibilitychange'));
    });
    await expect.poll(async () => (await readWakeLock(page)).requests).toBe(2);
    expect((await readWakeLock(page)).held).toBe(true);
    expect((await readWakeLock(page)).releases).toBe(0);

    await hostContext.close();
  });

  test('releases the wake lock when the game reaches the end-of-game intermission', async ({ page, baseURL }) => {
    test.setTimeout(90_000);

    const quizTitle = `Wake Finish ${Date.now()}`;
    const ada = `Adi-${Date.now()}`;
    const ben = `Bex-${Date.now()}`;

    await stubWakeLock(page);

    const hostContext = await page.context().browser()!.newContext({ storageState: adminStatePath(), baseURL });
    const host = await hostContext.newPage();
    await seedQuiz(host, quizTitle);
    const quizID = makeQuizLive(quizTitle);
    const createResp = await host.request.post('/api/sessions', { data: { quizId: quizID } });
    expect(createResp.status(), `create session: ${createResp.status()} ${await createResp.text()}`).toBe(201);
    const { joinCode } = await createResp.json() as { joinCode: string };

    // A second API-only player answers each question alongside the page player
    // so the runner closes every question and the game reaches the end-of-game
    // intermission (#836).
    const otherContext = await page.context().browser()!.newContext({ storageState: undefined, baseURL });
    await claimAndJoin(otherContext.request, joinCode, ben);

    await page.goto(`/join/${joinCode}`);
    await page.getByTestId('join-name-input').fill(ada);
    await page.getByTestId('join-name-submit').click();
    await expect(page.getByTestId('lobby-roster').getByText(ada)).toBeVisible();
    await expect.poll(async () => (await readWakeLock(page)).requests).toBe(1);

    const startResp = await host.request.post(`/api/sessions/${joinCode}/start`);
    expect(startResp.status(), `start session: ${startResp.status()} ${await startResp.text()}`).toBe(204);

    // Drive the game to completion: for each question, the page player answers
    // the first visible option and the API player answers via the state read so
    // the runner advances. The page player's own answers keep it on the live
    // surface; the lock stays held until finished.
    for (let i = 0; i < QUIZ_QUESTIONS.length; i++) {
      await expect(page.getByTestId('question-options')).toBeVisible({ timeout: 20_000 });
      const firstButton = page.locator('[data-testid="question-options"] button').first();
      await expect(firstButton).toBeEnabled({ timeout: 10_000 });
      await firstButton.click();

      // The API player answers the same question so every active player is in
      // and the runner closes it.
      const stateResp = await otherContext.request.get(`/api/sessions/${joinCode}/state`);
      expect(stateResp.ok()).toBeTruthy();
      const state = await stateResp.json() as { question: { options: { id: number }[] } | null };
      if (state.question) {
        await otherContext.request.post(`/api/sessions/${joinCode}/answer`, {
          data: { optionId: state.question.options[0].id },
        });
      }
      // Wait out the reveal before the next question's options mount.
      await expect(page.getByTestId('reveal-view')).toBeVisible({ timeout: 20_000 });
      if (i < QUIZ_QUESTIONS.length - 1) {
        await expect(page.getByTestId('reveal-view')).toBeHidden({ timeout: 20_000 });
      }
    }

    // The session reaches the end-of-game intermission standings (#836), and the
    // wake lock is released: no answer window keeps the screen busy while the
    // player waits between games.
    await expect(page.getByTestId('intermission-view')).toBeVisible({ timeout: 20_000 });
    await expect.poll(async () => (await readWakeLock(page)).held, { timeout: 10_000 }).toBe(false);
    expect((await readWakeLock(page)).releases).toBeGreaterThanOrEqual(1);

    await otherContext.close();
    await hostContext.close();
  });
});
