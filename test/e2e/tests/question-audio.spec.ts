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

// Slice 3 of #1059: audio PLAYBACK on the play surfaces. The upload/admin path
// is Slice 2, so these specs do not author a real sound. Instead they
// route-intercept the play endpoints to inject audioUrl on the question and
// serve a tiny real clip for /media/*, mirroring the deterministic
// route-interception approach the flash specs use. The behaviour under test is
// the playback chrome (the hidden <audio>, the mute toggle, the replay control)
// and the cross-surface contract that the phone stays answer-only.
test.use({ storageState: adminStatePath() });
// Several specs here host a live room as the shared admin, which runs one room
// at a time, so two host specs must not run against the same per-worker server
// at once. Serial mode keeps the file's specs from overlapping on a worker; the
// host specs also end their room on teardown so the host is free for the next.
test.describe.configure({ mode: 'serial' });

// AUDIO_SRC is the value the interceptor injects as the question's audioUrl. A
// self-contained data: URI for a minimal valid WAV (a 44-byte RIFF/WAVE header,
// no sample data): the <audio> element resolves a real, playable source with no
// network round-trip, so the server never logs a bogus /media id and media
// loader requests cannot escape Playwright's routing. The test asserts the
// element is wired (src + muted), not actual playback, which headless autoplay
// policy makes unreliable.
const AUDIO_SRC =
  'data:audio/wav;base64,UklGRiQAAABXQVZFZm10IBAAAAABAAEARKwAAIhYAQACABAAZGF0YQAAAAA=';

const SINGLE_QUESTION: readonly QuestionSpec[] = [
  { text: 'Question with a sound', options: ['Correct', 'Wrong', 'Nope', 'No'], correctIndices: [0] },
];

const TWO_QUESTIONS: readonly QuestionSpec[] = [
  { text: 'First sound question', options: ['Correct', 'Wrong', 'Nope', 'No'], correctIndices: [0] },
  { text: 'Second sound question', options: ['Correct', 'Wrong', 'Nope', 'No'], correctIndices: [0] },
];

// HELD_AUDIO_URL is a fake clip URL the loading-screen specs inject and then
// route to a request that never resolves, so the <audio> element's
// canplaythrough never fires. With the virtual clock installed the loading beat
// then proceeds via its ~5s timeout, which the spec drives with runFor - a fully
// deterministic loading screen with no real wall-clock wait (#1070).
const HELD_AUDIO_URL = '/media/held-audio-clip';

// injectSoloAudio rewrites the solo /next responses to carry audioUrl, so a
// quiz authored without a sound (Slice 2 is not merged) still exercises the
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

test('the solo player gets a hidden audio element and mute / replay controls for a question sound', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Audio Solo ${browserName}`;

  await createQuizWithQuestions(page, quizTitle, SINGLE_QUESTION);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  // Play anonymously with the injected audio and a fixed clock so the reveal
  // beat can be fast-forwarded.
  await page.context().clearCookies();
  await installPlaythroughClock(page);
  await injectSoloAudio(page);

  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  await page.getByRole('button', { name: 'Start Game' }).click();

  // Fast-forward the reveal beat so the question view is fully painted.
  await page.clock.runFor(3_500);

  // The hidden <audio> carries the injected source.
  const audio = page.getByTestId('question-audio');
  await expect(audio).toHaveAttribute('src', AUDIO_SRC);
  await expect(audio).toBeHidden();

  // The mute control toggles its label and the audio element's muted state.
  const mute = page.getByTestId('audio-mute');
  await expect(mute).toBeVisible();
  await expect(mute).toHaveText('Mute');
  await mute.click();
  await expect(mute).toHaveText('Unmute');
  await expect(audio).toHaveJSProperty('muted', true);
  await mute.click();
  await expect(mute).toHaveText('Mute');
  await expect(audio).toHaveJSProperty('muted', false);

  // The replay control is present so the player can restart the clip.
  await expect(page.getByTestId('audio-replay')).toBeVisible();
});

// injectStateAudio rewrites the live session state reads to carry audioUrl on
// the current question, so the big screen exercises the playback chrome without
// the Slice 2 upload path. The url defaults to the instant-buffering data: URI;
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
// PCM samples) so the browser fires canplaythrough once the clip is served.
const WAV_BYTES = Buffer.from(
  'UklGRkQAAABXQVZFZm10IBAAAAABAAEARKwAAIhYAQACABAAZGF0YSAAAAAA' +
    'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=',
  'base64',
);

// failAudioRoute answers the clip URL by aborting it, so the <audio> element's
// onerror fires and the loading beat ends on its error branch (no clock needed).
async function failAudioRoute(page: Page, audioUrl: string): Promise<void> {
  await page.route(`**${audioUrl}`, (route: Route) => route.abort());
}

// gatedAudioRoute holds the clip's bytes until the returned release() is called,
// then answers with a real WAV so canplaythrough fires. The test controls when
// the clip arrives, so the loading spinner stays up deterministically (no
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
  test.use({ serviceWorkers: 'block' });

  test('the host big screen shows the audio loading screen, then reveals the question and plays the clip', async ({
    page,
    context,
    baseURL,
    browserName,
  }) => {
    test.setTimeout(60_000);

    // Gate the clip's bytes so the loading spinner stays up until the test
    // releases them; releasing serves a real WAV so canplaythrough fires and the
    // beat ends. The test owns the timing, so the spinner does not race the
    // beat's completion.
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

      // Release the clip: canplaythrough ends the beat, so the question reveals
      // and the clip is wired to play (the <audio> carries the src).
      clip.release();

      await expect(page.getByTestId('audio-loading')).toBeHidden({ timeout: 15_000 });
      await expect(page.locator('[data-question-text]')).toHaveText(SINGLE_QUESTION[0].text);
      await expect(page.getByTestId('question-audio')).toHaveAttribute('src', HELD_AUDIO_URL);
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

test('the host big screen gets the audio element and a mute control during a question, and the phone stays silent', async ({
  page,
  context,
  baseURL,
  browserName,
}) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Audio Live ${browserName}`;

  await createQuizWithQuestions(page, quizTitle, SINGLE_QUESTION);
  setQuizMode(quizTitle, 'live');

  // The big screen injects audioUrl into its own state reads.
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

    // ---- Question phase on the TV: the audio element + mute control render.
    const questionView = page.locator('[data-phase-question]');
    await expect(questionView).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('[data-question-text]')).toHaveText(SINGLE_QUESTION[0].text);

    const audio = page.getByTestId('question-audio');
    await expect(audio).toHaveAttribute('src', AUDIO_SRC);
    await expect(audio).toBeHidden();

    const mute = page.getByTestId('audio-mute');
    await expect(mute).toBeVisible();
    await expect(mute).toHaveText('Mute');
    await mute.click();
    await expect(mute).toHaveText('Unmute');
    await expect(audio).toHaveJSProperty('muted', true);

    // The replay/play control gives the host a recovery affordance if autoplay
    // was blocked.
    await expect(page.getByTestId('audio-replay')).toBeVisible();

    // ---- The phone (answer pad) carries NO audio element or controls even
    // though its state read carries audioUrl: the live phone is answer-only.
    await expect(caseyPage.getByTestId('question-view')).toBeVisible({ timeout: 15_000 });
    await expect(caseyPage.getByTestId('question-audio')).toHaveCount(0);
    await expect(caseyPage.getByTestId('audio-mute')).toHaveCount(0);
    await expect(caseyPage.getByTestId('audio-replay')).toHaveCount(0);
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
// so the <audio> element's canplaythrough never fires and the loading beat falls
// through to its timeout. The returned hold is registered on the page route so a
// later runFor drives the timeout deterministically.
async function holdAudioRoute(page: Page, audioUrl: string): Promise<void> {
  await page.route(`**${audioUrl}`, async () => {
    // Never call route.fulfill / route.fetch: the request hangs, so the clip
    // never buffers and the loading beat must rely on its timeout.
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
  await installPlaythroughClock(page);
  await injectSoloAudio(page);

  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  await page.getByRole('button', { name: 'Start Game' }).click();

  // The data: URI buffers instantly, so the loading beat resolves on
  // canplaythrough; pump the reveal beat so the first question's answers paint.
  await page.clock.runFor(3_500);
  await expect(page.getByTestId('question-text')).toHaveText(TWO_QUESTIONS[0].text);
  const audio = page.getByTestId('question-audio');
  await expect(audio).toHaveAttribute('src', AUDIO_SRC);

  // Spy on the element's pause() method so the assertion does not depend on the
  // element ever actually playing (headless autoplay differs by browser). The
  // app calls pause() in nextQuestion before loading the next clip, so the
  // counter is non-zero by the time the second question loads - proving the stop
  // runs on the advance path.
  await audio.evaluate((el: HTMLAudioElement) => {
    const w = window as unknown as { __pauseCount: number };
    w.__pauseCount = 0;
    const original = el.pause.bind(el);
    el.pause = () => { w.__pauseCount += 1; original(); };
  });

  // Answer the first question, then drive the feedback pause + advance so
  // nextQuestion stops the current clip and loads the second question.
  await page.getByRole('button', { name: 'Correct' }).click();
  await page.clock.runFor(6_000);
  await expect(page.getByTestId('question-text')).toHaveText(TWO_QUESTIONS[1].text);

  const paused = await page.evaluate(
    () => (window as unknown as { __pauseCount: number }).__pauseCount,
  );
  expect(paused).toBeGreaterThan(0);
});
