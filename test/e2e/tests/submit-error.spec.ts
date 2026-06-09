import { test, expect } from './fixtures';
import {
  seedQuiz,
  startQuizAsAnonymous,
  QUIZ_QUESTIONS,
} from './helpers';
import { adminStatePath } from '../e2e-auth';

// Seed the quiz as the shared admin via the JSON importer, then clear the
// admin cookie so the retry flow runs anonymous.
test.use({ storageState: adminStatePath() });

// Covers issue #179: when the answers POST throws (server 5xx, network
// drop), the player client used to leave the question on screen with no
// feedback, timer drained, and no path forward. The fix re-arms the
// countdown so the player keeps the time they had left and surfaces a
// "couldn't submit" banner; the next click retries the POST.
//
// The route handler fails ONLY the first matching POST so the rest of
// the flow (the retry click + later questions) goes through normally.
// Page.route runs before any browser-side fetch reaches the network, so
// the failure is indistinguishable from a real 5xx to the client.
test('retry banner appears when answers POST fails, and a re-click advances the game', async ({ page, browserName }) => {
  test.setTimeout(45_000);

  const quizTitle = `E2E Submit Err ${browserName}`;

  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();

  // Fail the first answers POST with a 500; let subsequent ones through.
  // Scoped to POST so the in-progress GETs (countdown, next question)
  // are unaffected.
  let answersPostCount = 0;
  await page.route(/\/api\/games\/[^/]+\/questions\/\d+\/answers$/, async (route, request) => {
    if (request.method() === 'POST' && answersPostCount === 0) {
      answersPostCount++;
      await route.fulfill({ status: 500, body: 'simulated server error' });
      return;
    }
    await route.continue();
  });

  await startQuizAsAnonymous(page, quizTitle);

  const firstChoice = QUIZ_QUESTIONS[0].options[0];
  const firstButton = page.getByRole('button', { name: firstChoice });

  // Wait through the reveal-countdown (#247, e2e shrinks to 500ms via
  // REVEAL_DELAY) before the buttons appear.
  await expect(firstButton).toBeVisible({ timeout: 10_000 });
  await firstButton.click();

  // The banner uses role="alert" so AT tools announce it; the visible
  // text starts with "Couldn't submit" — partial match keeps the test
  // resilient to copy edits.
  const banner = page.getByRole('alert');
  await expect(banner).toBeVisible({ timeout: 5_000 });
  await expect(banner).toContainText("Couldn't submit");

  // Buttons must stay clickable — that's the headline guarantee of
  // the fix. `feedback` is null after the catch, so :disabled is
  // false; toBeEnabled covers both the attribute and pointer-events.
  await expect(firstButton).toBeEnabled();

  // Re-click. The second POST is allowed through by the route handler
  // and submits normally; the game advances to the next question.
  await firstButton.click();

  // Banner clears the instant the retry click runs (submitError = false
  // at the top of submitAnswer). The next click drives the standard
  // reveal + auto-advance path.
  await expect(banner).toBeHidden({ timeout: 5_000 });

  const secondChoice = QUIZ_QUESTIONS[1].options[0];
  await expect(page.getByRole('button', { name: secondChoice })).toBeVisible({ timeout: 15_000 });
});
