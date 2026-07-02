import { test, expect } from './fixtures';
import {
  seedQuiz,
  attachQuizImage,
  setQuizMode,
  endHostedSession,
  installPlaythroughClock,
  playerRow,
  waitForHostRoom,
  type QuestionSpec,
} from './helpers';
import { adminStatePath } from '../e2e-auth';

// Seed/author as the shared admin; the solo half plays anonymously after
// clearing cookies. #1119: a question image that 404s must show a small
// "Image unavailable" placeholder in the slot instead of collapsing to an
// empty gap, on both the solo player surface and the host big screen.
test.use({ storageState: adminStatePath() });

const SINGLE_QUESTION: readonly QuestionSpec[] = [
  { text: 'Question with a broken image', options: ['Correct', 'Wrong', 'Nope', 'No'], correctIndices: [0] },
];

test('the solo player sees an Image unavailable placeholder when the question image 404s', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Broken Image Quiz ${browserName}`;
  await seedQuiz(page, quizTitle, SINGLE_QUESTION);
  // Stamp an image media row so the question carries a /media/{id} imageUrl.
  attachQuizImage(quizTitle);

  // Play anonymously; force the image fetch to 404 so the <img> @error fires.
  await page.context().clearCookies();
  await installPlaythroughClock(page);
  await page.route('**/media/*', (route) => route.fulfill({ status: 404 }));

  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  await page.getByRole('button', { name: 'Start Game' }).click();

  // Fast-forward past the read beat so the question view is fully painted.
  await page.clock.runFor(3_500);

  const placeholder = page.getByTestId('question-image-placeholder');
  await expect(placeholder).toBeVisible();
  await expect(placeholder).toContainText('Image unavailable');
  // The broken image is hidden (x-show !imageError) rather than the slot
  // collapsing to nothing.
  await expect(page.getByTestId('question-image')).toBeHidden();
});

test('the host bigscreen shows an Image unavailable placeholder when the question image 404s', async ({
  page,
  context,
  baseURL,
  browserName,
}) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Live Broken Image Quiz ${browserName}`;
  await seedQuiz(page, quizTitle, SINGLE_QUESTION);
  attachQuizImage(quizTitle);
  setQuizMode(quizTitle, 'live');

  // Open a live session and land on the host TV lobby.
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  await page.getByRole('button', { name: 'Host live' }).click();
  const code = await waitForHostRoom(page);

  // One player joins and readies from a fresh anonymous context so the host
  // start has a non-empty, all-ready roster.
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

    // Force the bigscreen image fetch to 404 before the question paints.
    await page.route('**/media/*', (route) => route.fulfill({ status: 404 }));
    await page.getByRole('button', { name: 'Start now' }).click();

    // Question phase: the TV paints the question; its image 404s.
    const questionView = page.locator('[data-phase-question]');
    await expect(questionView).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('[data-question-text]')).toHaveText(SINGLE_QUESTION[0].text);

    const placeholder = page.getByTestId('question-image-placeholder');
    await expect(placeholder).toBeVisible({ timeout: 15_000 });
    await expect(placeholder).toContainText('Image unavailable');
    await expect(page.getByTestId('question-image')).toBeHidden();
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
