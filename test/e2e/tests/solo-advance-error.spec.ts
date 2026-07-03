import { test, expect } from './fixtures';
import { seedQuiz, startQuizAsAnonymous, QUIZ_QUESTIONS } from './helpers';
import { adminStatePath } from '../e2e-auth';

// Seed the quiz as the shared admin via the JSON importer, then clear the
// admin cookie so the playthrough runs anonymous.
test.use({ storageState: adminStatePath() });

// Covers issue #1166: when the question-advance fetch throws (server 5xx,
// mobile network blip) the solo game used to freeze on the feedback card with
// every answer button disabled and no path forward - recoverable only by a full
// reload. The await resolveAndAdvance() call sat outside submitAnswer's
// try/catch, so the rejection was unhandled. The fix catches it in advanceToNext
// and surfaces a retry banner (mirroring the #179 submit-error one) whose Retry
// control re-attempts the advance.
//
// The route fails GET .../questions/next while a flag is armed, then lets it
// through, so the first advance (prefetch + direct fetch) 500s and the Retry
// then succeeds. Page.route runs before any browser-side fetch reaches the
// network, so the failure is indistinguishable from a real 5xx to the client.
test('advance-retry banner appears when the next-question fetch fails, and Retry advances the game', async ({ page, browserName }) => {
  test.setTimeout(45_000);

  const quizTitle = `E2E Advance Err ${browserName}`;

  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();

  // Fail GET .../questions/next only while armed. The first question loads via
  // this same endpoint, so it must stay unarmed until Q1 is on screen; arming
  // after the answer click makes the prefetch + the direct advance fetch 500.
  let failNext = false;
  await page.route(/\/api\/games\/[^/]+\/questions\/next$/, async (route) => {
    if (failNext) {
      await route.fulfill({ status: 500, body: 'simulated server error' });
      return;
    }
    await route.continue();
  });

  await startQuizAsAnonymous(page, quizTitle);

  const firstChoice = QUIZ_QUESTIONS[0].options[0];
  const firstButton = page.getByRole('button', { name: firstChoice });

  // Wait through the reveal-countdown (#247, e2e shrinks it) before the buttons
  // appear, then arm the failure and answer.
  await expect(firstButton).toBeVisible({ timeout: 10_000 });
  failNext = true;
  await firstButton.click();

  // After the feedback pause the advance fetch 500s; without the fix the game
  // would freeze here. The banner uses role="alert" and carries a Retry button.
  const banner = page.getByTestId('advance-error');
  await expect(banner).toBeVisible({ timeout: 15_000 });
  await expect(banner).toContainText("Couldn't load the next question");

  const retry = page.getByTestId('advance-retry');
  await expect(retry).toBeEnabled();

  // Recover: let the next-question fetch through and retry. The advance
  // completes and the second question loads, and the banner clears.
  failNext = false;
  await retry.click();

  const secondChoice = QUIZ_QUESTIONS[1].options[0];
  await expect(page.getByRole('button', { name: secondChoice })).toBeVisible({ timeout: 15_000 });
  await expect(banner).toBeHidden();
});
