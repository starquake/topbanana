import type { Page } from '@playwright/test';

import { test, expect, Route } from './fixtures';
import {
  attachQuizAudio,
  createQuizWithQuestions,
  endHostedSession,
  installPlaythroughClock,
  playerRow,
  setQuizMode,
  type QuestionSpec,
} from './helpers';
import { adminStatePath } from '../e2e-auth';

// #1088: quiz audio + game sound effects play through Howler.js for reliable iOS
// playback. The strategy under test:
//   - SFX are preloaded + decoded on page load (before any gesture).
//   - The Start gesture synchronously resumes the AudioContext, starts the iOS
//     keep-alive, and plays the round-start SFX (the gesture-bound play that
//     unlocks iOS output).
//   - Every question clip is preloaded at game start, so each question plays an
//     already-decoded Howl with no per-question decode race.
//
// These specs pin the WIRING, not the iOS autoplay policy: headless autoplay is
// permissive, so a clip plays even without the real iOS unlock. We spy on
// Howl.prototype.play (recording the played source so SFX and clips are told
// apart) and on AudioContext.prototype.resume to assert the engine asked the
// right sounds to play at the right transitions. The #1088 core ("a second
// consecutive question autoplays with no new gesture") is asserted by counting
// clip plays across an advance with no extra tap.
//
// Audio is stamped onto the quiz's questions directly in the DB (attachQuizAudio)
// so the server's audio-manifest endpoint and the /next (or /state) payloads
// carry the SAME /media/{id} URL for the SAME questionId. /media/{id} is then
// route-served as a real WAV, so the preloaded Howls load and the manifest, the
// question audioUrl, and playClip(question.id) all stay in lockstep.
test.use({ storageState: adminStatePath() });
// Block the page service worker so it can't serve /media ahead of Playwright's
// route interception.
test.use({ serviceWorkers: 'block' });
// Several specs host a live room as the shared admin, which runs one room at a
// time, so two host specs must not run against the same per-worker server at
// once. Serial mode keeps the file's specs from overlapping on a worker; the
// host specs end their room on teardown so the host is free for the next.
test.describe.configure({ mode: 'serial' });

// A minimal but non-empty WAV (44-byte RIFF/WAVE header + a short run of silent
// PCM samples) so Howler's Web Audio decode succeeds and onload fires.
const WAV_BYTES = Buffer.from(
  'UklGRkQAAABXQVZFZm10IBAAAAABAAEARKwAAIhYAQACABAAZGF0YSAAAAAA' +
    'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=',
  'base64',
);

const SINGLE_QUESTION: readonly QuestionSpec[] = [
  { text: 'Question with audio', options: ['Correct', 'Wrong', 'Nope', 'No'], correctIndices: [0] },
];

const TWO_QUESTIONS: readonly QuestionSpec[] = [
  { text: 'First audio question', options: ['Correct', 'Wrong', 'Nope', 'No'], correctIndices: [0] },
  { text: 'Second audio question', options: ['Correct', 'Wrong', 'Nope', 'No'], correctIndices: [0] },
];

const ROUND_START_SFX = 'round-start';
const QUESTION_SHOW_SFX = 'question-show';
const ANSWERS_SHOW_SFX = 'answers-show';
const ANSWER_CORRECT_SFX = 'answer-correct';
const ANSWER_WRONG_SFX = 'answer-wrong';
const ANSWER_REVEAL_SFX = 'answer-reveal';
// The clip URLs the DB stamps and /media serves; '/media/' tells a question clip
// from an SFX in the play spy (the SFX live under /static/audio/sfx/).
const CLIP_FRAGMENT = '/media/';

// serveClips answers every /media clip URL with a real WAV so the preloaded
// Howls load (Howler's Web Audio decode fires onload).
async function serveClips(page: Page): Promise<void> {
  await page.route('**/media/**', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'audio/wav', body: WAV_BYTES }),
  );
}

// installAudioSpies patches Howl.prototype.play and AudioContext.prototype.resume
// before any page script runs. play() records the played source so SFX (by file
// name) and question clips (by /media URL) are counted separately; it also
// re-fires Howler's 'end' event so the engine's repeat loop advances
// deterministically without depending on a real clip finishing.
async function installAudioSpies(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const w = window as unknown as {
      __audio: { resumeCount: number; plays: string[]; stops: string[]; endFire: boolean };
    };
    w.__audio = { resumeCount: 0, plays: [], stops: [], endFire: true };

    const flattenSrc = (src: unknown): string =>
      Array.isArray(src) ? src.map((s) => String(s)).join(',') : String(src);

    const patchResume = (Ctor: typeof AudioContext | undefined) => {
      if (!Ctor || !Ctor.prototype) return;
      const proto = Ctor.prototype as unknown as { resume: () => Promise<void> };
      const original = proto.resume;
      proto.resume = function patchedResume(this: AudioContext) {
        w.__audio.resumeCount += 1;
        return original.apply(this);
      };
    };
    patchResume(window.AudioContext);
    patchResume((window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext);

    const patchHowl = (): boolean => {
      const Howl = (window as unknown as {
        Howl?: { prototype: { play: (...a: unknown[]) => unknown; __patched?: boolean } };
      }).Howl;
      if (!Howl || (Howl.prototype as { __patched?: boolean }).__patched) return !!Howl;
      const proto = Howl.prototype as unknown as {
        play: (...a: unknown[]) => unknown;
        stop: (...a: unknown[]) => unknown;
        _emit: (e: string, id?: unknown) => unknown;
        __patched?: boolean;
      };
      const original = proto.play;
      proto.play = function patchedPlay(
        this: { _src?: unknown; _emit: (e: string, id?: unknown) => unknown },
        ...args: unknown[]
      ) {
        w.__audio.plays.push(flattenSrc(this._src));
        const id = original.apply(this, args);
        if (w.__audio.endFire) {
          Promise.resolve().then(() => {
            try {
              this._emit('end', id);
            } catch {
              /* ignore */
            }
          });
        }
        return id;
      };
      const originalStop = proto.stop;
      proto.stop = function patchedStop(this: { _src?: unknown }, ...args: unknown[]) {
        w.__audio.stops.push(flattenSrc(this._src));
        return originalStop.apply(this, args);
      };
      proto.__patched = true;
      return true;
    };
    if (!patchHowl()) {
      const t = setInterval(() => {
        if (patchHowl()) clearInterval(t);
      }, 10);
    }
  });
}

// playsMatching counts recorded plays whose source contains the given substring
// (an SFX file name like 'round-start' or the '/media/' clip fragment).
async function playsMatching(page: Page, fragment: string): Promise<number> {
  return page.evaluate((frag) => {
    const w = window as unknown as { __audio: { plays: string[] } };
    return w.__audio.plays.filter((s) => s.includes(frag)).length;
  }, fragment);
}

// stopsMatching counts recorded Howl.stop calls whose source contains the given
// substring -- used to assert a clip's Howl was stopped (e.g. on a question
// advance, when the engine's stopClip halts the prior clip).
async function stopsMatching(page: Page, fragment: string): Promise<number> {
  return page.evaluate((frag) => {
    const w = window as unknown as { __audio: { stops: string[] } };
    return w.__audio.stops.filter((s) => s.includes(frag)).length;
  }, fragment);
}

async function resumeCount(page: Page): Promise<number> {
  return page.evaluate(
    () => (window as unknown as { __audio: { resumeCount: number } }).__audio.resumeCount,
  );
}

// uniqueTitle builds a per-run quiz title so parallel browser projects and
// Playwright retries never collide on the quizzes.title uniqueness constraint.
function uniqueTitle(label: string, browserName: string): string {
  return `E2E Audio ${label} ${browserName}-${Date.now()}`;
}

// startSoloAudioQuiz authors a solo quiz with a unique title, stamps audio on its
// questions, installs the spies + clip route + virtual clock, and navigates to
// the play deep link (anonymous). Leaves the page on the start screen with the
// Start button ready.
async function startSoloAudioQuiz(
  page: Page,
  label: string,
  browserName: string,
  questions: readonly QuestionSpec[],
  opts: { audioRepeat?: boolean } = {},
): Promise<void> {
  const quizTitle = uniqueTitle(label, browserName);
  await createQuizWithQuestions(page, quizTitle, questions);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  attachQuizAudio(quizTitle, { audioRepeat: opts.audioRepeat });

  await page.context().clearCookies();
  await installAudioSpies(page);
  await serveClips(page);
  await installPlaythroughClock(page);

  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
}

test('the solo Start tap resumes the AudioContext and plays the round-start SFX, and exposes mute / replay controls', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);
  await startSoloAudioQuiz(page, 'Solo', browserName, SINGLE_QUESTION);

  await page.getByRole('button', { name: 'Start Game' }).click();

  // The Start gesture resumed the AudioContext and played the round-start sting.
  await expect.poll(() => resumeCount(page)).toBeGreaterThan(0);
  await expect.poll(() => playsMatching(page, ROUND_START_SFX)).toBeGreaterThan(0);

  // Fast-forward the reveal beat so the question view is fully painted.
  await page.clock.runFor(3_500);
  await expect(page.getByTestId('question-text')).toHaveText(SINGLE_QUESTION[0].text);

  // The mute control toggles its label and persists the preference.
  const mute = page.getByTestId('audio-mute');
  await expect(mute).toBeVisible();
  await expect(mute).toHaveText('Mute');
  await mute.click();
  await expect(mute).toHaveText('Unmute');
  await mute.click();
  await expect(mute).toHaveText('Mute');

  // The replay control is present so the player can restart the clip.
  await expect(page.getByTestId('audio-replay')).toBeVisible();
});

test('the solo player preloads every question clip at game start and autoplays the first clip', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);

  // Capture the manifest fetch so the spec can assert every clip was requested
  // for preload at game start.
  let manifestClipCount = 0;
  await page.route('**/api/games/*/audio', async (route: Route) => {
    const response = await route.fetch();
    const body = await response.json();
    manifestClipCount = Array.isArray(body.clips) ? body.clips.length : 0;
    await route.fulfill({ response, json: body });
  });

  await startSoloAudioQuiz(page, 'Preload', browserName, TWO_QUESTIONS);

  await page.getByRole('button', { name: 'Start Game' }).click();
  await page.clock.runFor(3_500);
  await expect(page.getByTestId('question-text')).toHaveText(TWO_QUESTIONS[0].text);

  // The manifest carried both audio-bearing questions.
  expect(manifestClipCount).toBe(2);

  // The first question's quiz clip autoplayed (a /media clip play landed).
  await expect.poll(() => playsMatching(page, CLIP_FRAGMENT)).toBeGreaterThan(0);
});

// #1088 CORE: the SECOND consecutive audio question must also autoplay with NO
// new gesture. With the old <audio> approach Q1 played but Q2 went silent on
// iOS; preloading every clip + keeping the context hot fixes it. Headless
// autoplay is permissive, so this pins the wiring (a second /media clip play
// lands after the advance) rather than the iOS policy itself.
test('a second consecutive solo audio question autoplays with no new gesture (#1088)', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);
  await startSoloAudioQuiz(page, 'Second', browserName, TWO_QUESTIONS);

  await page.getByRole('button', { name: 'Start Game' }).click();
  await page.clock.runFor(3_500);
  await expect(page.getByTestId('question-text')).toHaveText(TWO_QUESTIONS[0].text);
  await expect.poll(() => playsMatching(page, CLIP_FRAGMENT)).toBeGreaterThan(0);
  const afterFirst = await playsMatching(page, CLIP_FRAGMENT);

  // Answer Q1, then drive the feedback pause + advance so Q2 loads. No new tap
  // on any audio control happens here.
  await page.getByRole('button', { name: 'Correct' }).click();
  await page.clock.runFor(6_000);
  await expect(page.getByTestId('question-text')).toHaveText(TWO_QUESTIONS[1].text);

  // Q2's clip autoplayed too: the clip-play count climbed again after the
  // advance, with no extra gesture.
  await expect.poll(() => playsMatching(page, CLIP_FRAGMENT)).toBeGreaterThan(afterFirst);
});

// The advance must halt the prior question's clip so it cannot bleed into the
// next beat (engine.stopClip). The deleted <audio>-era spec pinned this; here we
// assert the clip's Howl is stopped when the player advances to the next
// question.
test('advancing to the next question stops the previous question clip (#1088)', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);
  await startSoloAudioQuiz(page, 'AdvanceStop', browserName, TWO_QUESTIONS);

  await page.getByRole('button', { name: 'Start Game' }).click();
  await page.clock.runFor(3_500);
  await expect(page.getByTestId('question-text')).toHaveText(TWO_QUESTIONS[0].text);
  await expect.poll(() => playsMatching(page, CLIP_FRAGMENT)).toBeGreaterThan(0);
  const stopsBefore = await stopsMatching(page, CLIP_FRAGMENT);

  // Answer Q1 and drive the feedback pause + advance to Q2 (no audio-control tap).
  await page.getByRole('button', { name: 'Correct' }).click();
  await page.clock.runFor(6_000);
  await expect(page.getByTestId('question-text')).toHaveText(TWO_QUESTIONS[1].text);

  // stopClip halted Q1's clip on the advance, so its Howl saw an extra stop().
  await expect.poll(() => stopsMatching(page, CLIP_FRAGMENT)).toBeGreaterThan(stopsBefore);
});

test('the solo player plays the question-show SFX on reveal and the answers-show SFX when the options appear', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);
  await startSoloAudioQuiz(page, 'Stings', browserName, SINGLE_QUESTION);

  await page.getByRole('button', { name: 'Start Game' }).click();

  // The question-show sting fires as the read beat starts (question text shown).
  await expect.poll(() => playsMatching(page, QUESTION_SHOW_SFX)).toBeGreaterThan(0);

  // The answers-show sting fires when the read beat ends and the options appear.
  await page.clock.runFor(3_500);
  await expect(page.getByRole('button', { name: 'Correct' })).toBeVisible();
  await expect.poll(() => playsMatching(page, ANSWERS_SHOW_SFX)).toBeGreaterThan(0);
});

test('the solo player plays the answer-correct SFX on a right pick and answer-wrong on a wrong pick', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);
  await startSoloAudioQuiz(page, 'Pick', browserName, TWO_QUESTIONS);

  await page.getByRole('button', { name: 'Start Game' }).click();
  await page.clock.runFor(3_500);
  await expect(page.getByRole('button', { name: 'Correct' })).toBeVisible();

  // A correct pick plays the answer-correct sting.
  await page.getByRole('button', { name: 'Correct' }).click();
  await expect.poll(() => playsMatching(page, ANSWER_CORRECT_SFX)).toBeGreaterThan(0);
  expect(await playsMatching(page, ANSWER_WRONG_SFX)).toBe(0);

  // Advance to Q2 and pick a wrong option: the answer-wrong sting plays.
  await page.clock.runFor(6_000);
  await expect(page.getByTestId('question-text')).toHaveText(TWO_QUESTIONS[1].text);
  await page.getByRole('button', { name: 'Wrong' }).click();
  await expect.poll(() => playsMatching(page, ANSWER_WRONG_SFX)).toBeGreaterThan(0);
});

test('the solo audio repeats a repeat-flagged clip three times then stops', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);
  await startSoloAudioQuiz(page, 'Repeat', browserName, SINGLE_QUESTION, {
    audioRepeat: true,
  });

  await page.getByRole('button', { name: 'Start Game' }).click();
  await page.clock.runFor(3_500);
  await expect(page.getByTestId('question-text')).toHaveText(SINGLE_QUESTION[0].text);

  // Replay through the user-gesture control so the spy sees the full sequence
  // from a known start (the autoplay may have run before, but counting plays of
  // the clip across the replay + repeat gaps gives the total play count).
  const before = await playsMatching(page, CLIP_FRAGMENT);
  await page.getByTestId('audio-replay').click();

  // The controller schedules each repeat after a 1000ms gap; drive the clock
  // past all the inter-repeat gaps so the queued replays fire.
  await page.clock.runFor(5_000);

  // A repeat clip plays three times from the replay, then stops: no fourth play.
  await expect.poll(async () => (await playsMatching(page, CLIP_FRAGMENT)) - before).toBe(3);
  await page.clock.runFor(5_000);
  expect((await playsMatching(page, CLIP_FRAGMENT)) - before).toBe(3);
});

test('the solo audio plays a normal clip once with no repeats', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);
  await startSoloAudioQuiz(page, 'NoRepeat', browserName, SINGLE_QUESTION, {
    audioRepeat: false,
  });

  await page.getByRole('button', { name: 'Start Game' }).click();
  await page.clock.runFor(3_500);
  await expect(page.getByTestId('question-text')).toHaveText(SINGLE_QUESTION[0].text);

  const before = await playsMatching(page, CLIP_FRAGMENT);
  await page.getByTestId('audio-replay').click();

  // A normal clip plays once from the replay; no repeat is scheduled even after
  // the clock advances past the repeat gap.
  await expect.poll(async () => (await playsMatching(page, CLIP_FRAGMENT)) - before).toBe(1);
  await page.clock.runFor(5_000);
  expect((await playsMatching(page, CLIP_FRAGMENT)) - before).toBe(1);
});

test('the solo mute preference persists across reload and silences both the SFX and the clip', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);
  await startSoloAudioQuiz(page, 'Mute', browserName, SINGLE_QUESTION);

  await page.getByRole('button', { name: 'Start Game' }).click();
  await page.clock.runFor(3_500);
  const mute = page.getByTestId('audio-mute');
  await expect(mute).toBeVisible();
  await mute.click();
  await expect(mute).toHaveText('Unmute');

  // Reload mid-game: the mute preference survives. The spies reset on reload
  // (fresh page context), so any play recorded after the reload happened while
  // muted.
  await page.reload();
  await page.clock.runFor(3_500);
  await expect(page.getByTestId('audio-mute')).toHaveText('Unmute');

  // While muted, the SFX are fully suppressed: the resumed question's
  // question-show sting never plays even though the question reveals.
  await expect(page.getByTestId('question-text')).toBeVisible();
  expect(await playsMatching(page, QUESTION_SHOW_SFX)).toBe(0);
  expect(await playsMatching(page, ROUND_START_SFX)).toBe(0);

  // The clip is muted too: the preloaded Howls carry the muted flag, so even a
  // played clip is silent. Verify the live Howls report muted.
  const clipsMuted = await page.evaluate(() => {
    const reg = (window as unknown as { Howler?: { _howls?: Array<{ _muted: boolean }> } }).Howler;
    const howls = reg && Array.isArray(reg._howls) ? reg._howls : [];
    return howls.length > 0 && howls.every((h) => h._muted === true);
  });
  expect(clipsMuted).toBe(true);
});

test('the solo player surfaces the manual play fallback when a clip fails to load', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);

  const quizTitle = uniqueTitle('Fail', browserName);
  await createQuizWithQuestions(page, quizTitle, SINGLE_QUESTION);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  attachQuizAudio(quizTitle);

  await page.context().clearCookies();
  await installAudioSpies(page);
  // Abort the clip fetch so the preloaded Howl errors; the engine then flags the
  // blocked fallback when it tries to play.
  await page.route('**/media/**', (route: Route) => route.abort());
  await installPlaythroughClock(page);

  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await page.getByRole('button', { name: 'Start Game' }).click();
  await page.clock.runFor(3_500);
  await expect(page.getByTestId('question-text')).toHaveText(SINGLE_QUESTION[0].text);

  // The replay control reads "Play audio" (not "Replay audio") once the clip is
  // blocked/failed, the manual recovery affordance.
  await expect(page.getByTestId('audio-replay')).toHaveText('Play audio');
});

// liveOptionId polls the session state until the answer window is open, then
// returns the option id whose text matches. Used to drive a player's answer via
// the API so the runner advances the room into the reveal phase.
async function liveOptionId(
  request: import('@playwright/test').APIRequestContext,
  code: string,
  text: string,
): Promise<number> {
  let optionId: number | undefined;
  await expect(async () => {
    const resp = await request.get(`/api/sessions/${code}/state`);
    expect(resp.ok()).toBeTruthy();
    const state = await resp.json();
    expect(state.phase).toBe('question');
    expect(state.question?.startedAt).toBeTruthy();
    expect(Date.parse(state.serverNow) >= Date.parse(state.question.startedAt)).toBeTruthy();
    const option = (state.question.options as Array<{ id: number; text: string }>).find(
      (o) => o.text === text,
    );
    expect(option).toBeTruthy();
    optionId = option!.id;
  }).toPass({ timeout: 10_000 });
  return optionId!;
}

// startLiveAudioSession authors a live quiz with audio, hosts it, joins +
// readies one player, and starts the game so the host page advances into the
// question phase. The caller installs the spies + clip route on the host page
// BEFORE calling. The returned cleanup closes the joined player's context AND
// ends the hosted room (the shared admin hosts one room at a time, #957).
async function startLiveAudioSession(
  page: Page,
  context: import('@playwright/test').BrowserContext,
  baseURL: string | undefined,
  browserName: string,
  quizTitle: string,
): Promise<{
  cleanup: () => Promise<void>;
  code: string;
  caseyCtx: import('@playwright/test').BrowserContext;
  caseyPage: Page;
}> {
  await createQuizWithQuestions(page, quizTitle, SINGLE_QUESTION);
  attachQuizAudio(quizTitle);
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

  return {
    code,
    caseyCtx,
    caseyPage,
    cleanup: async () => {
      await caseyCtx.close();
      await endHostedSession(page, code).catch(() => undefined);
    },
  };
}

test('the host big screen Start tap resumes the AudioContext, plays the round-start SFX, then autoplays the question clip', async ({
  page,
  context,
  baseURL,
  browserName,
}) => {
  test.setTimeout(60_000);

  await installAudioSpies(page);
  await serveClips(page);

  const { cleanup } = await startLiveAudioSession(
    page,
    context,
    baseURL,
    browserName,
    uniqueTitle('Host', browserName),
  );
  try {
    // The host Start gesture resumed the context and played the round-start
    // sting.
    await expect.poll(() => resumeCount(page)).toBeGreaterThan(0);
    await expect.poll(() => playsMatching(page, ROUND_START_SFX)).toBeGreaterThan(0);

    // The question phase paints on the big screen and the question clip + the
    // question-show sting play.
    await expect(page.locator('[data-phase-question]')).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('[data-question-text]')).toHaveText(SINGLE_QUESTION[0].text);
    await expect.poll(() => playsMatching(page, QUESTION_SHOW_SFX)).toBeGreaterThan(0);
    await expect.poll(() => playsMatching(page, CLIP_FRAGMENT)).toBeGreaterThan(0);

    // The mute + replay controls render.
    const mute = page.getByTestId('audio-mute');
    await expect(mute).toBeVisible();
    await expect(mute).toHaveText('Mute');
    await mute.click();
    await expect(mute).toHaveText('Unmute');
    await expect(page.getByTestId('audio-replay')).toBeVisible();
  } finally {
    await cleanup();
  }
});

test('the host big screen plays the reveal sting (not a pick sting) and the phone stays audio-free', async ({
  page,
  context,
  baseURL,
  browserName,
}) => {
  test.setTimeout(60_000);

  await installAudioSpies(page);
  await serveClips(page);

  const { code, caseyCtx, caseyPage, cleanup } = await startLiveAudioSession(
    page,
    context,
    baseURL,
    browserName,
    uniqueTitle('Reveal', browserName),
  );
  try {
    await expect(page.locator('[data-phase-question]')).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('[data-question-text]')).toHaveText(SINGLE_QUESTION[0].text);

    // Make the only player answer via the API so the runner closes the question
    // and moves the room into the reveal phase (the same path host-game.spec.ts
    // uses). At reveal the big screen lights the correct option.
    const optionId = await liveOptionId(caseyCtx.request, code, 'Correct');
    const answer = await caseyCtx.request.post(`/api/sessions/${code}/answer`, {
      data: { optionId },
    });
    expect(answer.status()).toBe(204);

    // Wait until reveal is actually on screen (the correct option is lit).
    await expect(page.locator('[data-answer-option][data-correct="true"]')).toHaveCount(1, {
      timeout: 30_000,
    });

    // The big screen plays the dedicated reveal sting as the answer is shown...
    await expect.poll(() => playsMatching(page, ANSWER_REVEAL_SFX), { timeout: 30_000 }).toBeGreaterThan(0);
    // ...but NOT a pick sting: there is no per-player pick on the big screen, so
    // answer-correct / answer-wrong would be meaningless here (those belong to the
    // solo surface, where one device picks).
    expect(await playsMatching(page, ANSWER_CORRECT_SFX)).toBe(0);
    expect(await playsMatching(page, ANSWER_WRONG_SFX)).toBe(0);

    // The phone (answer pad) carries NO audio controls and never loaded Howler:
    // the live phone is answer-only.
    await expect(caseyPage.getByTestId('audio-mute')).toHaveCount(0);
    await expect(caseyPage.getByTestId('audio-replay')).toHaveCount(0);
    const hasHowler = await caseyPage.evaluate(
      () => typeof (window as unknown as { Howl?: unknown }).Howl !== 'undefined',
    );
    expect(hasHowler).toBe(false);
  } finally {
    await cleanup();
  }
});

test('the join phone loads no Howler and shows no audio controls', async ({
  page,
  context,
  baseURL,
  browserName,
}) => {
  test.setTimeout(60_000);

  await serveClips(page);

  const { caseyPage, cleanup } = await startLiveAudioSession(
    page,
    context,
    baseURL,
    browserName,
    uniqueTitle('Phone', browserName),
  );
  try {
    await expect(caseyPage.getByTestId('question-view')).toBeVisible({ timeout: 15_000 });

    // No Howler global and no audio controls on the phone.
    const hasHowler = await caseyPage.evaluate(
      () => typeof (window as unknown as { Howl?: unknown }).Howl !== 'undefined',
    );
    expect(hasHowler).toBe(false);
    await expect(caseyPage.getByTestId('audio-mute')).toHaveCount(0);
    await expect(caseyPage.getByTestId('audio-replay')).toHaveCount(0);
  } finally {
    await cleanup();
  }
});
