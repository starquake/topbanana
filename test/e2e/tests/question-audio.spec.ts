import type { Page } from '@playwright/test';

import { test, expect, Route } from './fixtures';
import {
  createQuizWithQuestions,
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
// the Slice 2 upload path.
async function injectStateAudio(page: Page): Promise<void> {
  await page.route('**/api/sessions/*/state', async (route: Route) => {
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
}

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
