import { test, expect, Route, Request, Page } from './fixtures';
import { seedQuiz } from './helpers';
import { adminStatePath } from '../e2e-auth';

// Seed each quiz as the shared admin via the JSON importer, then clear the
// admin cookie so the start-screen flows run anonymous.
test.use({ storageState: adminStatePath() });

// Covers #1120: a quiz-list fetch failure (network / 5xx) used to be
// swallowed into an empty list, so it looked identical to a genuinely empty
// catalogue - on a bare start screen the picker copy stayed, and on a deep
// link it claimed "That quiz isn't available". The fix surfaces a distinct
// error + Retry, and Retry recovers once the endpoint is healthy.

// failQuizListOnce fails the first GET /api/quizzes with a 500, then lets
// every later call through, so the Retry button's second call recovers.
// Scoped to the exact list path so the per-quiz leaderboard / my-game
// routes (/api/quizzes/{slug}/...) are untouched.
async function failQuizListOnce(page: Page): Promise<void> {
  let quizListCount = 0;
  await page.route(/\/api\/quizzes$/, async (route: Route, request: Request) => {
    if (request.method() === 'GET' && quizListCount === 0) {
      quizListCount++;
      await route.fulfill({ status: 500, body: 'simulated server error' });
      return;
    }
    await route.continue();
  });
}

test('quiz-list 500 on a deep link shows an error + Retry, then Retry recovers', async ({ page, browserName }) => {
  test.setTimeout(45_000);

  // Date.now() keeps the title unique per attempt: a Playwright retry reuses
  // the same per-worker DB, so a fixed title would 409 on re-import (#908).
  const quizTitle = `E2E Quiz List Err ${browserName} ${Date.now()}`;

  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();

  // Discover the quiz's /play/{slug-id} deep link from the public list (a
  // server-rendered page that does not depend on /api/quizzes).
  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  const playUrl = new URL(page.url()).pathname;

  // Fail the first /api/quizzes, then revisit the deep link so the SPA's
  // init() load is the failing call.
  await failQuizListOnce(page);
  await page.goto(playUrl);

  // The error + Retry surface - not the misleading "not available" note,
  // not the quiz header.
  const errorBanner = page.getByTestId('quizzes-error');
  await expect(errorBanner).toBeVisible();
  await expect(errorBanner).toContainText("Couldn't load");
  await expect(page.getByTestId('quizzes-retry')).toBeVisible();
  await expect(page.getByTestId('deep-link-unavailable')).toHaveCount(0);
  await expect(page.getByTestId('deep-link-header')).toHaveCount(0);

  // Retry: the second /api/quizzes call passes through, the list loads, and
  // the deep-link header (the quiz title) resolves.
  await page.getByTestId('quizzes-retry').click();
  await expect(errorBanner).toBeHidden();
  const header = page.getByTestId('deep-link-header');
  await expect(header).toBeVisible();
  await expect(header.getByRole('heading', { name: quizTitle })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Start Game' })).toBeVisible();
});

test('quiz-list 500 on the start screen shows an error + Retry, then Retry recovers', async ({ page, browserName }) => {
  test.setTimeout(45_000);

  const quizTitle = `E2E Quiz List Picker ${browserName} ${Date.now()}`;

  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();

  await failQuizListOnce(page);
  await page.goto('/client/');

  // The error + Retry surface, not the "Browse all quizzes" picker copy a
  // swallowed failure used to leave in place.
  const errorBanner = page.getByTestId('quizzes-error');
  await expect(errorBanner).toBeVisible();
  await expect(page.getByTestId('quizzes-retry')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Browse all quizzes' })).toBeHidden();

  // Retry: the list loads and the picker affordance appears.
  await page.getByTestId('quizzes-retry').click();
  await expect(errorBanner).toBeHidden();
  await expect(page.getByRole('link', { name: 'Browse all quizzes' })).toBeVisible();
});
