import type { APIRequestContext, Page } from '@playwright/test';

import { test, expect } from './fixtures';
import { createQuizWithQuestions, endHostedSession, installPlaythroughClock, playerRow, publishQuiz, setQuizMode, waitForHostRoom, type QuestionSpec } from './helpers';
import { adminStatePath } from '../e2e-auth';

// Seed and author as the shared admin; play anonymously after clearing the
// cookie. The headline behaviour for #938: a host uploads an image, attaches
// it to a question, and a player sees that image render on the play screen.
test.use({ storageState: adminStatePath() });

// A small valid 120x80 PNG so the upload pipeline can decode and re-encode it,
// and the served jpeg actually renders in the client (a broken fetch would trip
// the <img> @error handler and hide the element, defeating the assertion).
const PNG_SAMPLE = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAHgAAABQCAIAAABd+SbeAAAAzklEQVR4nOzQURGAMADFsHcw4UhHxfqVXBX0bN+z6XZn7wgYHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjfwAAAP//uxwDnKDt4NgAAAAASUVORK5CYII=',
  'base64',
);

const SINGLE_QUESTION: readonly QuestionSpec[] = [
  { text: 'Question with an image', options: ['Correct', 'Wrong', 'Nope', 'No'], correctIndices: [0] },
];

// authorQuizWithImageQuestion authors a one-question quiz through the admin UI,
// uploads PNG_SAMPLE to the quiz library, and attaches it to the question - the
// shared setup both the solo and live image tests need. Leaves the page on the
// quiz view with the image attached.
async function authorQuizWithImageQuestion(page: Page, quizTitle: string): Promise<void> {
  await createQuizWithQuestions(page, quizTitle, SINGLE_QUESTION);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await page.locator('input[type="file"][name="images"]').setInputFiles({
    name: 'pic.png',
    mimeType: 'image/png',
    buffer: PNG_SAMPLE,
  });
  const libraryThumb = page.getByTestId('library-thumb').first();
  await expect(libraryThumb).toBeVisible({ timeout: 30_000 });

  await page.getByRole('link', { name: 'Edit question' }).first().click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+\/questions\/\d+\/edit$/);
  const pickerThumb = page
    .getByTestId('question-image-picker')
    .getByTestId('library-thumb')
    .first();
  await expect(pickerThumb).toBeVisible();
  await pickerThumb.click();
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.locator('.q-text', { hasText: SINGLE_QUESTION[0].text })).toBeVisible({ timeout: 15_000 });
}

// answerOpenOptionId reads a participant's GET /state and returns an option id,
// polling until the answer window has opened (serverNow at or after startedAt):
// the read beat (#247) holds answers closed for a brief beat, and a pick before
// then would 409. Mirrors host-game.spec's optionIdForText - the live image
// test drives its player through the REST API, with no player UI.
async function answerOpenOptionId(request: APIRequestContext, code: string): Promise<number> {
  let optionId: number | undefined;
  await expect(async () => {
    const resp = await request.get(`/api/sessions/${code}/state`);
    expect(resp.ok(), `state read: ${resp.status()} ${await resp.text()}`).toBeTruthy();
    const state = await resp.json();
    expect(state.phase, 'expected the session to be in the question phase').toBe('question');
    expect(state.question?.startedAt, 'question should carry an answers-open anchor').toBeTruthy();
    expect(
      Date.parse(state.serverNow) >= Date.parse(state.question.startedAt),
      'answer window should have opened (read beat elapsed)',
    ).toBeTruthy();
    optionId = state.question.options[0]?.id;
    expect(optionId, 'question should carry at least one option').toBeTruthy();
  }).toPass({ timeout: 10_000 });

  return optionId!;
}

test('a host attaches an uploaded image to a question and the player sees it on the play screen', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Image Quiz ${browserName}`;

  // ---- Author the quiz, upload an image, and attach it to the question.
  await authorQuizWithImageQuestion(page, quizTitle);
  // Publish after the UI authoring so the quiz shows in the public solo list
  // (#1192); a draft is filtered out there.
  publishQuiz(quizTitle);

  // ---- Play anonymously and assert the question image renders.
  await page.context().clearCookies();
  await installPlaythroughClock(page);

  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  await page.getByRole('button', { name: 'Start Game' }).click();

  // The reveal beat holds the options hidden for a few seconds; fast-forward
  // it so the question view is fully painted.
  await page.clock.runFor(3_500);

  const questionImage = page.getByTestId('question-image');
  await expect(questionImage).toBeVisible();
  // The src points at the serving endpoint, and the image actually loaded
  // (naturalWidth > 0) rather than falling back to the broken-image state.
  await expect(questionImage).toHaveAttribute('src', /\/media\/\d+$/);
  await expect
    .poll(async () => questionImage.evaluate((img: HTMLImageElement) => img.naturalWidth))
    .toBeGreaterThan(0);
});

// The bigscreen half of #938: the same attached image renders on the host TV
// (the shared room screen), not just the player phones. The image block sits
// inside the question/reveal block, so it stays on screen through both phases.
// The player is driven over the REST API (join / ready / answer) with no UI,
// mirroring host-game.spec; the host TV is the surface under test.
test('the question image renders on the host bigscreen during the question and reveal', async ({
  page,
  context,
  baseURL,
  browserName,
}) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Live Image Quiz ${browserName}`;

  // ---- Author the quiz, upload an image, attach it, and switch to live mode.
  await authorQuizWithImageQuestion(page, quizTitle);
  setQuizMode(quizTitle, 'live');

  // ---- Open a live session and land on the host TV lobby.
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  await page.getByRole('button', { name: 'Host live' }).click();
  const code = await waitForHostRoom(page);

  // ---- One player joins and readies from a fresh anonymous context so the
  // host start has a non-empty, all-ready roster.
  const casey = `Casey-${browserName}-${Date.now()}`;
  const caseyCtx = await context.browser()!.newContext({ storageState: undefined, baseURL });
  try {
    const claimResp = await caseyCtx.request.patch('/api/players/me', { data: { displayName: casey } });
    expect(claimResp.status(), `claim ${casey}: ${await claimResp.text()}`).toBe(200);
    const joinResp = await caseyCtx.request.post(`/api/sessions/${code}/join`);
    expect(joinResp.status(), `join ${casey}: ${await joinResp.text()}`).toBe(200);
    const readyResp = await caseyCtx.request.post(`/api/sessions/${code}/ready`, { data: { ready: true } });
    expect(readyResp.status()).toBe(204);

    await expect(playerRow(page, casey)).toBeVisible();
    await page.getByRole('button', { name: 'Start now' }).click();

    // ---- Question phase: the TV paints the question and its attached image.
    const questionView = page.locator('[data-phase-question]');
    await expect(questionView).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('[data-question-text]')).toHaveText(SINGLE_QUESTION[0].text);

    const questionImage = page.getByTestId('question-image');
    await expect(questionImage).toBeVisible();
    await expect(questionImage).toHaveAttribute('src', /\/media\/\d+$/);
    await expect
      .poll(async () => questionImage.evaluate((img: HTMLImageElement) => img.naturalWidth))
      .toBeGreaterThan(0);

    // ---- Reveal: the player answers to close the question early, and the
    // image stays on screen through the reveal (the block spans both phases).
    const optionId = await answerOpenOptionId(caseyCtx.request, code);
    const answerResp = await caseyCtx.request.post(`/api/sessions/${code}/answer`, { data: { optionId } });
    expect(answerResp.status(), `answer: ${await answerResp.text()}`).toBe(204);

    await expect(page.locator('[data-answer-option][data-correct="true"]')).toHaveCount(1, { timeout: 15_000 });
    await expect(questionImage).toBeVisible();
  } finally {
    // End the session this spec started as the shared admin BEFORE closing the
    // player context, so a rejecting caseyCtx.close() cannot skip the
    // session-end: a live game left running flips the next shared-admin host
    // test's "Host live" into the confirm-restart modal (no /host/<code>
    // navigation), stalling it (#1143).
    await endHostedSession(page, code).catch(() => undefined);
    await caseyCtx.close();
  }
});
