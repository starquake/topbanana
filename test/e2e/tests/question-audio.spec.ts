import type { Page } from '@playwright/test';

import { test, expect, Route } from './fixtures';
import {
  createQuizWithQuestions,
  endHostedSession,
  installPlaythroughClock,
  playerRow,
  setQuizMode,
  type QuestionSpec,
} from './helpers';
import { adminStatePath } from '../e2e-auth';

// Slice 3 of #1059 + #1088: audio PLAYBACK on the play surfaces. Playback is
// driven by Howler.js (#1088), which owns its own Web Audio nodes - there is no
// longer a persistent <audio data-testid="question-audio"> element. These specs
// therefore assert via the visible controls (mute / replay) and a spy on
// Howl.prototype (play / stop / mute) rather than a DOM media element.
//
// Headless browsers use a permissive autoplay policy, so these specs pin the
// WIRING - that the controller builds a Howl, plays it once per question, the
// repeat sequence loops the right number of times, mute flows through to the
// Howl, and the clip stops on advance - NOT the iOS autoplay policy the #1088
// change actually fixes (that needs a real device). The upload/admin path is
// Slice 2, so these specs do not author a real audio clip; they route-intercept
// the play endpoints to inject audioUrl on the question and serve a tiny real
// clip for /media/*, mirroring the deterministic route-interception the flash
// specs use.
test.use({ storageState: adminStatePath() });
// Block the page service worker so it can't serve /media ahead of Playwright's
// route interception, which intermittently fails the audio loading specs.
test.use({ serviceWorkers: 'block' });
// Several specs here host a live room as the shared admin, which runs one room
// at a time, so two host specs must not run against the same per-worker server
// at once. Serial mode keeps the file's specs from overlapping on a worker; the
// host specs also end their room on teardown so the host is free for the next.
test.describe.configure({ mode: 'serial' });

// AUDIO_SRC is the value the interceptor injects as the question's audioUrl: a
// self-contained data: URI for a minimal but non-empty WAV (a RIFF/WAVE header
// plus a short run of silent PCM samples). A data: URI lets Howler decode it
// through decodeAudioData with no network round-trip, so the server never logs a
// bogus /media id and the media loader cannot escape Playwright's routing. The
// non-empty data chunk keeps decodeAudioData from rejecting an empty buffer on
// the stricter browsers. The specs assert the controller's Howl is wired (built,
// played, muted), not actual audible output, which headless autoplay makes
// unreliable.
const AUDIO_SRC =
  'data:audio/wav;base64,UklGRkQAAABXQVZFZm10IBAAAAABAAEARKwAAIhYAQACABAAZGF0YSAAAAAA' +
  'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=';

const SINGLE_QUESTION: readonly QuestionSpec[] = [
  { text: 'Question with audio', options: ['Correct', 'Wrong', 'Nope', 'No'], correctIndices: [0] },
];

const TWO_QUESTIONS: readonly QuestionSpec[] = [
  { text: 'First audio question', options: ['Correct', 'Wrong', 'Nope', 'No'], correctIndices: [0] },
  { text: 'Second audio question', options: ['Correct', 'Wrong', 'Nope', 'No'], correctIndices: [0] },
];

// HELD_AUDIO_URL is a fake clip URL the loading-screen specs inject and then
// route to a held / aborted / hung request. Because it is NOT a data: URI,
// Howler loads it over XHR (its Web Audio path), so the request passes through
// Playwright's page.route and the test controls when (or whether) the probe
// Howl's 'load' fires.
const HELD_AUDIO_URL = '/media/held-audio-clip';

// installHowlSpy patches Howl.prototype (play / stop / mute) BEFORE any Howl is
// constructed, so it captures the controller's first autoplay (which fires during
// the loading beat, before the question paints). It records:
//   __howlPlayCount  - how many times the controller asked a clip to play
//   __howlStopCount  - how many times a clip was stopped/unloaded on advance
//   __howlMuted      - the last value passed to mute() (the controller mirrors
//                      the persisted mute preference onto the Howl)
// It also re-fires Howler's 'end' event after each play() so the repeat loop
// advances deterministically: the silent data: URI has ~0 duration and headless
// playback is unreliable, so 'end' may never fire on its own. A getter argument
// to mute() (Howler returns state) is ignored - only setter calls (a boolean
// argument) update __howlMuted.
async function installHowlSpy(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const w = window as unknown as {
      __howlPlayCount: number;
      __howlStopCount: number;
      __howlMuted: boolean | null;
      Howl?: unknown;
    };
    w.__howlPlayCount = 0;
    w.__howlStopCount = 0;
    w.__howlMuted = null;
    const patch = (Howl: { prototype: Record<string, unknown> }) => {
      const proto = Howl.prototype as {
        play: (...args: unknown[]) => unknown;
        stop: (...args: unknown[]) => unknown;
        mute: (...args: unknown[]) => unknown;
        _emit?: (event: string) => unknown;
        __tbSpied?: boolean;
      };
      if (proto.__tbSpied) return;
      proto.__tbSpied = true;
      const origPlay = proto.play;
      const origStop = proto.stop;
      const origMute = proto.mute;
      proto.play = function patchedPlay(this: typeof proto, ...args: unknown[]) {
        w.__howlPlayCount += 1;
        const result = origPlay.apply(this, args);
        // Fire 'end' on the next microtask so the controller's listener (added
        // during start) runs after play() returns, advancing any repeat loop.
        Promise.resolve().then(() => {
          if (typeof this._emit === 'function') this._emit('end');
        });
        return result;
      };
      proto.stop = function patchedStop(this: typeof proto, ...args: unknown[]) {
        w.__howlStopCount += 1;
        return origStop.apply(this, args);
      };
      proto.mute = function patchedMute(this: typeof proto, ...args: unknown[]) {
        if (args.length > 0 && typeof args[0] === 'boolean') {
          w.__howlMuted = args[0] as boolean;
        }
        return origMute.apply(this, args);
      };
    };
    // The init script runs before the Howler vendor <script>, so window.Howl is
    // not defined yet (and the page clock is faked, so a polling setInterval
    // would never fire on its own). Intercept the assignment with an accessor:
    // when the vendor script does `window.Howl = Howl`, patch the prototype and
    // store it, so the spy is in place for the very first Howl ever built.
    let stored: { prototype: Record<string, unknown> } | undefined;
    Object.defineProperty(window, 'Howl', {
      configurable: true,
      get() {
        return stored;
      },
      set(value: { prototype: Record<string, unknown> }) {
        stored = value;
        if (value) patch(value);
      },
    });
  });
}

// installHowlSpyNow patches Howl.prototype AFTER the page has loaded Howler and
// the question has painted, then zeroes the play counter. Patching the prototype
// is live (existing Howl instances dispatch play() through the prototype), so the
// controller's already-built Howl is captured. Used by the repeat specs, which
// want a clean, deterministic play count from the user-gesture replay click.
//
// It first flushes two animation frames: the controller defers the first
// question's autoplay start() through Alpine's $nextTick (runAudioLoadingBeat),
// so under the virtual clock + async data:-URI decode that deferred autoplay can
// otherwise fire AFTER this spy zeroes the counter and the replay click runs,
// making the spy count both the late autoplay and the replay. Draining the rAF
// queue here guarantees the autoplay has already played before the counter is
// reset, so the count reflects only the replay sequence.
//
// The patched play() does NOT call the real Howler play(): it just counts and
// then fires the controller's 'end' listener once on the next microtask. This
// fully decouples the repeat loop from Howler's real Web Audio playback, whose
// 'end' fires on the AudioContext clock (real time, not the virtual page clock)
// and would otherwise race the synthetic one and double-count. The controller's
// repeat handler reschedules the next play on the (virtual) page clock, so the
// spec drives the whole sequence with page.clock.runFor. It counts only calls
// the controller makes (play() with no args); Howler's internal recursive
// self.play(id) / self.play(id, true) pass an argument, so they never inflate
// the count even if the patched path is later changed to call real playback.
async function installHowlSpyNow(page: Page): Promise<void> {
  // Drain the rAF queue so the $nextTick-deferred autoplay start() has run.
  await page.evaluate(
    () =>
      new Promise<void>((resolve) => {
        requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
      }),
  );
  await page.evaluate(() => {
    const w = window as unknown as {
      __howlPlayCount: number;
      Howl?: { prototype: Record<string, unknown> };
    };
    w.__howlPlayCount = 0;
    const Howl = w.Howl;
    if (!Howl) return;
    const proto = Howl.prototype as {
      play: (...args: unknown[]) => unknown;
      _emit?: (event: string) => unknown;
      __tbSpiedNow?: boolean;
    };
    if (proto.__tbSpiedNow) return;
    proto.__tbSpiedNow = true;
    proto.play = function patchedPlay(this: typeof proto, ...args: unknown[]) {
      // Count only the controller's own play() (no args); Howler's internal
      // recursive self.play(id[, true]) carries an argument and is skipped.
      if (args.length === 0) w.__howlPlayCount += 1;
      // Fire 'end' on the next microtask so the controller's repeat listener
      // (added during start) advances the loop, with no real Howler playback so
      // its real 'end' cannot also fire and double the count.
      Promise.resolve().then(() => {
        if (typeof this._emit === 'function') this._emit('end');
      });
      return undefined;
    };
  });
}

function howlPlayCount(page: Page): Promise<number> {
  return page.evaluate(() => (window as unknown as { __howlPlayCount: number }).__howlPlayCount);
}

function howlStopCount(page: Page): Promise<number> {
  return page.evaluate(() => (window as unknown as { __howlStopCount: number }).__howlStopCount);
}

function howlMuted(page: Page): Promise<boolean | null> {
  return page.evaluate(() => (window as unknown as { __howlMuted: boolean | null }).__howlMuted);
}

// injectSoloAudio rewrites the solo /next responses to carry audioUrl, so a
// quiz authored without audio (Slice 2 is not merged) still exercises the
// playback chrome. Other endpoints pass through untouched.
async function injectSoloAudio(page: Page): Promise<void> {
  await page.route('**/api/games/*/questions/next', async (route: Route) => {
    const response = await route.fetch();
    if (!response.ok()) {
      await route.fulfill({ response });
      return;
    }
    const body = await response.json();
    if (body && body.type === 'question') {
      body.audioUrl = AUDIO_SRC;
    }
    await route.fulfill({ response, json: body });
  });
}

// injectSoloAudioRepeat is injectSoloAudio plus the per-question audioRepeat
// flag, so the playback controller runs (or skips) its repeat sequence (#1073).
async function injectSoloAudioRepeat(page: Page, audioRepeat: boolean): Promise<void> {
  await page.route('**/api/games/*/questions/next', async (route: Route) => {
    const response = await route.fetch();
    if (!response.ok()) {
      await route.fulfill({ response });
      return;
    }
    const body = await response.json();
    if (body && body.type === 'question') {
      body.audioUrl = AUDIO_SRC;
      body.audioRepeat = audioRepeat;
    }
    await route.fulfill({ response, json: body });
  });
}

test('the solo player gets mute / replay controls and a wired Howl for a question audio clip', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Audio Solo ${browserName}`;

  await createQuizWithQuestions(page, quizTitle, SINGLE_QUESTION);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  // Play anonymously with the injected audio and a fixed clock so the reveal
  // beat can be fast-forwarded. The Howl spy is installed before navigation.
  await page.context().clearCookies();
  await installHowlSpy(page);
  await installPlaythroughClock(page);
  await injectSoloAudio(page);

  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  await page.getByRole('button', { name: 'Start Game' }).click();

  // Fast-forward the reveal beat so the question view is fully painted.
  await page.clock.runFor(3_500);

  // The controller built a Howl and played the clip (no DOM <audio> element any
  // more; the wiring is the assertion).
  await expect(page.getByTestId('question-text')).toHaveText(SINGLE_QUESTION[0].text);
  await expect.poll(() => howlPlayCount(page)).toBeGreaterThan(0);

  // The mute control toggles its label and flows the mute state through to the
  // Howl's mute().
  const mute = page.getByTestId('audio-mute');
  await expect(mute).toBeVisible();
  await expect(mute).toHaveText('Mute');
  await mute.click();
  await expect(mute).toHaveText('Unmute');
  await expect.poll(() => howlMuted(page)).toBe(true);
  await mute.click();
  await expect(mute).toHaveText('Mute');
  await expect.poll(() => howlMuted(page)).toBe(false);

  // The replay control is present so the player can restart the clip.
  await expect(page.getByTestId('audio-replay')).toBeVisible();
});

// #1085 / #1088: an audio question must autoplay, not fall back to the manual
// "Play audio" button. The original regression was a mount/timing race on the
// per-question <audio> element; #1088 removes that element entirely and routes
// playback through Howler, whose autoUnlock frees later plays after the Start
// gesture. The invariant this pins is "the controller built a Howl and called
// play() on the FIRST audio question". A Howl.prototype.play spy installed before
// navigation captures that first autoplay regardless of timing. Asserting a
// "Replay audio" label would depend on Howler's playback actually resolving,
// which headless autoplay makes unreliable; a positive play count is the precise,
// browser-policy-independent signal that the autoplay ran.
test('the FIRST solo audio question autoplays through Howler (#1088)', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Audio First Autoplay ${browserName}`;

  await createQuizWithQuestions(page, quizTitle, SINGLE_QUESTION);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await page.context().clearCookies();
  await installHowlSpy(page);
  await installPlaythroughClock(page);
  await injectSoloAudio(page);

  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  await page.getByRole('button', { name: 'Start Game' }).click();

  // The data: URI decodes instantly, so the loading beat resolves on Howler's
  // 'load'; pump the reveal beat so the first question paints.
  await page.clock.runFor(3_500);
  await expect(page.getByTestId('question-text')).toHaveText(SINGLE_QUESTION[0].text);

  // start() built the Howl and called play() on the FIRST audio question: the
  // autoplay ran rather than dropping into the blocked branch.
  await expect.poll(() => howlPlayCount(page)).toBeGreaterThan(0);
});

// #1088: the SECOND consecutive audio question must also autoplay. This was the
// reported iOS regression: Q1 played (its clip lands right after the Start
// gesture) but Q2 was silent because HTMLAudioElement.play() needs a fresh
// gesture each time on iOS. Routing playback through Howler (Web Audio,
// autoUnlock on the first gesture) lets Q2 play with no further tap. Headless
// autoplay is permissive, so this pins the auto-start invariant (play() is called
// again on the SECOND question) rather than reproducing a real-iOS block.
test('the SECOND consecutive solo audio question also autoplays through Howler (#1088)', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Audio Second Autoplay ${browserName}`;

  await createQuizWithQuestions(page, quizTitle, TWO_QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await page.context().clearCookies();
  await installHowlSpy(page);
  await installPlaythroughClock(page);
  await injectSoloAudio(page);

  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  await page.getByRole('button', { name: 'Start Game' }).click();

  // Q1 paints and autoplays.
  await page.clock.runFor(3_500);
  await expect(page.getByTestId('question-text')).toHaveText(TWO_QUESTIONS[0].text);
  await expect.poll(() => howlPlayCount(page)).toBeGreaterThan(0);
  const afterFirst = await howlPlayCount(page);

  // Answer Q1, then drive the feedback pause + advance so the loading beat for Q2
  // runs and the second clip autoplays.
  await page.getByRole('button', { name: 'Correct' }).click();
  await page.clock.runFor(6_000);
  await expect(page.getByTestId('question-text')).toHaveText(TWO_QUESTIONS[1].text);

  // Q2's start() built a Howl and called play() again: the second question's
  // autoplay ran rather than going silent.
  await expect.poll(() => howlPlayCount(page)).toBeGreaterThan(afterFirst);
});

// injectStateAudio rewrites the live session state reads to carry audioUrl on
// the current question, so the big screen exercises the playback chrome without
// the Slice 2 upload path. The url defaults to the instant-decoding data: URI;
// the loading-screen specs pass HELD_AUDIO_URL so the clip fetch can be held.
async function injectStateAudio(page: Page, audioUrl: string = AUDIO_SRC): Promise<void> {
  await page.route('**/api/sessions/*/state', async (route: Route) => {
    const response = await route.fetch();
    if (!response.ok()) {
      await route.fulfill({ response });
      return;
    }
    const body = await response.json();
    if (body && body.question) {
      body.question.audioUrl = audioUrl;
    }
    await route.fulfill({ response, json: body });
  });
}

// A minimal but non-empty WAV (44-byte RIFF/WAVE header + a short run of silent
// PCM samples) so Howler's decodeAudioData succeeds once the clip is served and
// the probe Howl fires 'load'.
const WAV_BYTES = Buffer.from(
  'UklGRkQAAABXQVZFZm10IBAAAAABAAEARKwAAIhYAQACABAAZGF0YSAAAAAA' +
    'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=',
  'base64',
);

// failAudioRoute answers the clip URL by aborting it, so the probe Howl's
// loaderror fires and the loading beat ends on its error branch (no clock needed).
async function failAudioRoute(page: Page, audioUrl: string): Promise<void> {
  await page.route(`**${audioUrl}`, (route: Route) => route.abort());
}

// gatedAudioRoute holds the clip's bytes until the returned release() is called,
// then answers with a real WAV so the probe Howl fires 'load'. The test controls
// when the clip arrives, so the loading spinner stays up deterministically (no
// dependence on the browser's media-stall timing) until the test releases it.
function gatedAudioRoute(page: Page, audioUrl: string): { install: () => Promise<void>; release: () => void } {
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  return {
    install: () =>
      page.route(`**${audioUrl}`, async (route: Route) => {
        await gate;
        await route.fulfill({ status: 200, contentType: 'audio/wav', body: WAV_BYTES });
      }),
    release,
  };
}

// startLiveAudioSession creates a live quiz, hosts it, joins + readies one
// player, and starts the game so the host page advances into the question phase.
// The caller installs the state + clip routes on the host page BEFORE calling so
// the loading beat's first fetch is intercepted. The returned cleanup closes the
// joined player's context AND ends the hosted room: the shared admin hosts only
// one room at a time (#957), so a left-open room blocks the next spec's host.
async function startLiveAudioSession(
  page: Page,
  context: import('@playwright/test').BrowserContext,
  baseURL: string | undefined,
  browserName: string,
  quizTitle: string,
): Promise<() => Promise<void>> {
  await createQuizWithQuestions(page, quizTitle, SINGLE_QUESTION);
  setQuizMode(quizTitle, 'live');

  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  await page.getByRole('button', { name: 'Host live' }).click();
  await expect(page).toHaveURL(/\/host\/[A-Z0-9]+$/);
  const code = page.url().split('/host/')[1];

  const casey = `Casey-${browserName}-${Date.now()}`;
  const caseyCtx = await context.browser()!.newContext({ storageState: undefined, baseURL });
  const caseyPage = await caseyCtx.newPage();
  await caseyPage.goto(`/join/${code}`);
  await caseyPage.getByTestId('join-name-input').fill(casey);
  await caseyPage.getByTestId('join-name-submit').click();
  await expect(caseyPage.getByTestId('lobby-roster').getByText(casey)).toBeVisible();
  const readyResp = await caseyCtx.request.post(`/api/sessions/${code}/ready`, { data: { ready: true } });
  expect(readyResp.status()).toBe(204);

  await expect(playerRow(page, casey)).toBeVisible();
  await page.getByRole('button', { name: 'Start now' }).click();

  return async () => {
    await caseyCtx.close();
    await endHostedSession(page, code).catch(() => undefined);
  };
}

// The loading-beat specs route the clip's bytes (a /media URL), but the page's
// service worker serves /media from its cache-or-network handler and that
// fetch() does NOT pass through Playwright's page.route - so an already-active SW
// makes the injected clip 404 against the real server. Blocking the SW for these
// specs keeps the /media route deterministic on both a cold and a warm SW.
test.describe('host big screen audio loading beat', () => {
  test('the host big screen shows the audio loading screen, then reveals the question and plays the clip', async ({
    page,
    context,
    baseURL,
    browserName,
  }) => {
    test.setTimeout(60_000);

    // Gate the clip's bytes so the loading spinner stays up until the test
    // releases them; releasing serves a real WAV so the probe Howl fires 'load'
    // and the beat ends. The test owns the timing, so the spinner does not race
    // the beat's completion.
    const clip = gatedAudioRoute(page, HELD_AUDIO_URL);
    await injectStateAudio(page, HELD_AUDIO_URL);
    await clip.install();

    const cleanup = await startLiveAudioSession(
      page,
      context,
      baseURL,
      browserName,
      `E2E Audio Load Live ${browserName}`,
    );
    try {
      // The loading screen is on the big screen while the clip is gated; the
      // question stays hidden behind it.
      await expect(page.getByTestId('audio-loading')).toBeVisible({ timeout: 15_000 });
      await expect(page.locator('[data-question-text]')).toBeHidden();

      // Release the clip: the probe Howl's 'load' ends the beat, so the question
      // reveals and the controller plays the clip.
      clip.release();

      await expect(page.getByTestId('audio-loading')).toBeHidden({ timeout: 15_000 });
      await expect(page.locator('[data-question-text]')).toHaveText(SINGLE_QUESTION[0].text);
    } finally {
      clip.release();
      await cleanup();
    }
  });

  test('the host big screen leaves the audio loading screen and surfaces the play control when the clip fails', async ({
    page,
    context,
    baseURL,
    browserName,
  }) => {
    test.setTimeout(60_000);

    // Fail the clip fetch so the loading beat ends on its error branch (the beat
    // proceeds without the clip): the question reveals and the manual play
    // control stays up so the host can start the clip after a failed autoload.
    await injectStateAudio(page, HELD_AUDIO_URL);
    await failAudioRoute(page, HELD_AUDIO_URL);

    const cleanup = await startLiveAudioSession(
      page,
      context,
      baseURL,
      browserName,
      `E2E Audio Fail Live ${browserName}`,
    );
    try {
      await expect(page.getByTestId('audio-loading')).toBeHidden({ timeout: 15_000 });
      await expect(page.locator('[data-question-text]')).toHaveText(SINGLE_QUESTION[0].text);
      await expect(page.getByTestId('audio-replay')).toBeVisible();
    } finally {
      await cleanup();
    }
  });
});

test('the host big screen plays a wired Howl during a question, with a mute control, and the phone stays silent', async ({
  page,
  context,
  baseURL,
  browserName,
}) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Audio Live ${browserName}`;

  await createQuizWithQuestions(page, quizTitle, SINGLE_QUESTION);
  setQuizMode(quizTitle, 'live');

  // The big screen injects audioUrl into its own state reads, and the Howl spy
  // is installed before the host page navigates.
  await installHowlSpy(page);
  await injectStateAudio(page);

  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  await page.getByRole('button', { name: 'Host live' }).click();
  await expect(page).toHaveURL(/\/host\/[A-Z0-9]+$/);
  const code = page.url().split('/host/')[1];

  // One player joins and readies from a fresh anonymous context so the host
  // start has a non-empty, all-ready roster. The player context also injects
  // audioUrl into its OWN state reads to prove the phone stays answer-only even
  // when the wire carries the field.
  const casey = `Casey-${browserName}-${Date.now()}`;
  const caseyCtx = await context.browser()!.newContext({ storageState: undefined, baseURL });
  try {
    const caseyPage = await caseyCtx.newPage();
    await caseyPage.route('**/api/sessions/*/state', async (route: Route) => {
      const response = await route.fetch();
      if (!response.ok()) {
        await route.fulfill({ response });
        return;
      }
      const body = await response.json();
      if (body && body.question) {
        body.question.audioUrl = AUDIO_SRC;
      }
      await route.fulfill({ response, json: body });
    });

    await caseyPage.goto(`/join/${code}`);
    await caseyPage.getByTestId('join-name-input').fill(casey);
    await caseyPage.getByTestId('join-name-submit').click();
    await expect(caseyPage.getByTestId('lobby-roster').getByText(casey)).toBeVisible();
    const readyResp = await caseyCtx.request.post(`/api/sessions/${code}/ready`, { data: { ready: true } });
    expect(readyResp.status()).toBe(204);

    await expect(playerRow(page, casey)).toBeVisible();
    await page.getByRole('button', { name: 'Start now' }).click();

    // ---- Question phase on the TV: the controller plays a Howl and the mute
    // control renders.
    const questionView = page.locator('[data-phase-question]');
    await expect(questionView).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('[data-question-text]')).toHaveText(SINGLE_QUESTION[0].text);
    await expect.poll(() => howlPlayCount(page)).toBeGreaterThan(0);

    const mute = page.getByTestId('audio-mute');
    await expect(mute).toBeVisible();
    await expect(mute).toHaveText('Mute');
    await mute.click();
    await expect(mute).toHaveText('Unmute');
    await expect.poll(() => howlMuted(page)).toBe(true);

    // The replay/play control gives the host a recovery affordance if autoplay
    // was blocked.
    await expect(page.getByTestId('audio-replay')).toBeVisible();

    // ---- The phone (answer pad) carries NO audio controls even though its
    // state read carries audioUrl: the live phone is answer-only, and the join
    // shell does not even load Howler.
    await expect(caseyPage.getByTestId('question-view')).toBeVisible({ timeout: 15_000 });
    await expect(caseyPage.getByTestId('audio-mute')).toHaveCount(0);
    await expect(caseyPage.getByTestId('audio-replay')).toHaveCount(0);
    await expect(
      caseyPage.evaluate(() => typeof (window as unknown as { Howl?: unknown }).Howl),
    ).resolves.toBe('undefined');
  } finally {
    await caseyCtx.close();
  }
});

// injectSoloAudioUrl rewrites the solo /next responses to carry a given
// audioUrl, so the loading-screen specs can point the clip at a held route.
async function injectSoloAudioUrl(page: Page, audioUrl: string): Promise<void> {
  await page.route('**/api/games/*/questions/next', async (route: Route) => {
    const response = await route.fetch();
    if (!response.ok()) {
      await route.fulfill({ response });
      return;
    }
    const body = await response.json();
    if (body && body.type === 'question') {
      body.audioUrl = audioUrl;
    }
    await route.fulfill({ response, json: body });
  });
}

// holdAudioRoute answers the held clip URL with a request that never resolves,
// so the probe Howl's 'load' never fires and the loading beat falls through to
// its timeout. The returned hold is registered on the page route so a later
// runFor drives the timeout deterministically.
async function holdAudioRoute(page: Page, audioUrl: string): Promise<void> {
  await page.route(`**${audioUrl}`, async () => {
    // Never call route.fulfill / route.fetch: the request hangs, so the clip
    // never loads and the loading beat must rely on its timeout.
    await new Promise(() => {});
  });
}

test('the solo player sees an audio loading screen before the question, then the question reveals', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Audio Loading ${browserName}`;

  await createQuizWithQuestions(page, quizTitle, SINGLE_QUESTION);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await page.context().clearCookies();
  await installPlaythroughClock(page);
  await injectSoloAudioUrl(page, HELD_AUDIO_URL);
  await holdAudioRoute(page, HELD_AUDIO_URL);

  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  await page.getByRole('button', { name: 'Start Game' }).click();

  // The loading screen is on screen and the question text is not yet revealed.
  await expect(page.getByTestId('audio-loading')).toBeVisible();
  await expect(page.getByTestId('question-text')).toBeHidden();

  // Drive past the loading beat's ~5s timeout; the question then reveals.
  await page.clock.runFor(6_000);
  await expect(page.getByTestId('audio-loading')).toBeHidden();
  await expect(page.getByTestId('question-text')).toBeVisible();
  await expect(page.getByTestId('question-text')).toHaveText(SINGLE_QUESTION[0].text);
});

test('the solo audio stops when the player advances to the next question', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Audio Advance ${browserName}`;

  await createQuizWithQuestions(page, quizTitle, TWO_QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await page.context().clearCookies();
  await installHowlSpy(page);
  await installPlaythroughClock(page);
  await injectSoloAudio(page);

  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  await page.getByRole('button', { name: 'Start Game' }).click();

  // The data: URI decodes instantly, so the loading beat resolves on 'load';
  // pump the reveal beat so the first question's answers paint and its clip plays.
  await page.clock.runFor(3_500);
  await expect(page.getByTestId('question-text')).toHaveText(TWO_QUESTIONS[0].text);
  await expect.poll(() => howlPlayCount(page)).toBeGreaterThan(0);
  const stopsBefore = await howlStopCount(page);

  // Answer the first question, then drive the feedback pause + advance so
  // nextQuestion stops the current clip (the controller unloads its Howl, which
  // stops it) and loads the second question.
  await page.getByRole('button', { name: 'Correct' }).click();
  await page.clock.runFor(6_000);
  await expect(page.getByTestId('question-text')).toHaveText(TWO_QUESTIONS[1].text);

  // The advance path stopped the prior question's Howl (stop count climbed).
  await expect.poll(() => howlStopCount(page)).toBeGreaterThan(stopsBefore);
});

test('the solo audio repeats a repeat-flagged clip three times then stops', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Audio Repeat ${browserName}`;

  await createQuizWithQuestions(page, quizTitle, SINGLE_QUESTION);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await page.context().clearCookies();
  await installPlaythroughClock(page);
  await injectSoloAudioRepeat(page, true);

  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  await page.getByRole('button', { name: 'Start Game' }).click();

  // The data: URI decodes instantly; pump the reveal beat so the question paints
  // and the controller's Howl is built before the spy is installed.
  await page.clock.runFor(3_500);
  await expect(page.getByTestId('question-text')).toHaveText(SINGLE_QUESTION[0].text);

  // Spy on play() now (post-paint, count from 0) and re-arm the controller
  // through the user-gesture replay control so the spy sees the full repeat
  // sequence cleanly. The spy re-fires 'end' after each play(), advancing the
  // controller's repeat loop.
  await installHowlSpyNow(page);
  await page.getByTestId('audio-replay').click();

  // The controller schedules each repeat after a 1000ms gap; drive the clock past
  // all of the inter-repeat gaps so the queued replays fire.
  await page.clock.runFor(5_000);

  // A repeat clip plays three times total, then stops: no fourth play.
  await expect.poll(() => howlPlayCount(page)).toBe(3);
  await page.clock.runFor(5_000);
  expect(await howlPlayCount(page)).toBe(3);
});

test('the solo audio plays a normal clip once with no repeats', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Audio NoRepeat ${browserName}`;

  await createQuizWithQuestions(page, quizTitle, SINGLE_QUESTION);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await page.context().clearCookies();
  await installPlaythroughClock(page);
  await injectSoloAudioRepeat(page, false);

  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  await page.getByRole('button', { name: 'Start Game' }).click();

  await page.clock.runFor(3_500);
  await expect(page.getByTestId('question-text')).toHaveText(SINGLE_QUESTION[0].text);

  // Spy on play() now (post-paint, count from 0) and replay through the
  // user-gesture control.
  await installHowlSpyNow(page);
  await page.getByTestId('audio-replay').click();

  // A normal clip plays once; the 'end' event does not schedule a replay even
  // after the clock advances past the repeat gap.
  await expect.poll(() => howlPlayCount(page)).toBe(1);
  await page.clock.runFor(5_000);
  expect(await howlPlayCount(page)).toBe(1);
});
