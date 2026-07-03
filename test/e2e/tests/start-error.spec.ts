import { test, expect, Route, Request, Page } from './fixtures';
import { seedQuiz, startQuizAsAnonymous, QUIZ_QUESTIONS } from './helpers';
import { adminStatePath } from '../e2e-auth';

// Seed the quiz as the shared admin via the JSON importer, then clear the
// admin cookie so the start flow runs anonymous.
test.use({ storageState: adminStatePath() });

// Covers #1188: startGame's first-question fetch (/questions/next) sat outside
// any try/catch, so a 5xx / network failure escaped unhandled and stranded the
// player on the frozen "Loading question..." view with no feedback or retry.
// The fix rolls back to the start screen and surfaces a startError banner; the
// game row already exists, so a refresh resumes it via the checkAlreadyPlayed
// path.

// failFirstNextQuestion fails the first GET .../questions/next with a 500, then
// lets every later call through so a refresh recovers. route.fulfill
// short-circuits before the request reaches the server, so the question is
// never marked asked and the resumed /next returns it fresh.
async function failFirstNextQuestion(page: Page): Promise<void> {
  let nextCount = 0;
  await page.route(/\/api\/games\/[^/]+\/questions\/next$/, async (route: Route, request: Request) => {
    if (request.method() === 'GET' && nextCount === 0) {
      nextCount++;
      await route.fulfill({ status: 500, body: 'simulated server error' });
      return;
    }
    await route.continue();
  });
}

test('first-question 500 shows a start error instead of a frozen loading view, and a refresh recovers', async ({ page, browserName }) => {
  test.setTimeout(45_000);

  // Date.now() keeps the title unique per attempt: a Playwright retry reuses the
  // same per-worker DB, so a fixed title would 409 on re-import (#908).
  const quizTitle = `E2E Start Err ${browserName} ${Date.now()}`;

  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();

  await failFirstNextQuestion(page);
  await startQuizAsAnonymous(page, quizTitle);

  // The start-error banner surfaces and the frozen "Loading question..." state
  // (the in-game view's pre-first-question placeholder) is gone.
  const banner = page.getByTestId('start-error');
  await expect(banner).toBeVisible({ timeout: 10_000 });
  await expect(banner).toContainText("Couldn't start the quiz");
  await expect(page.getByTestId('audio-loading')).toBeHidden();

  // The start screen is reachable again (Start Game button re-rendered), not a
  // dead half-loaded SPA.
  await expect(page.getByRole('button', { name: 'Start Game' })).toBeVisible();

  // Refresh: the game row exists, so the resume path loads the first question
  // (the second /next call passes through).
  await page.reload();
  await expect(page.getByText(QUIZ_QUESTIONS[0].text)).toBeVisible({ timeout: 15_000 });
});
