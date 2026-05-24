import { test, expect } from './fixtures';
import {
  registerAdmin,
  createQuizWithQuestions,
  startQuizAsAnonymous,
  answerRemainingQuestions,
  QUIZ_QUESTIONS,
} from './helpers';

// #310: mobile pull-to-refresh used to bounce the player back to the
// start screen mid-question. After the resume fix the boot path
// probes /my-game; an in-flight game skips the start screen and
// re-renders the same question via the idempotent /questions/next
// path.
test('mid-game reload lands back on the question, not the start screen', async ({ page, browserName }) => {
  // registerAdmin + createQuizWithQuestions costs ~10s, the resumed
  // game then plays through four questions with ~2-3s feedback
  // pauses each. 90s leaves headroom on slow CI runners.
  test.setTimeout(90_000);

  const adminUser = `e2e-admin-resume-${browserName}`;
  const quizTitle = `E2E Resume Quiz ${browserName}`;

  await registerAdmin(page, adminUser);
  await createQuizWithQuestions(page, quizTitle);
  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/\/login$/);

  await startQuizAsAnonymous(page, quizTitle);

  // First question is on screen with its options. Do NOT click —
  // the whole point is to refresh mid-question.
  const firstChoice = QUIZ_QUESTIONS[0].options[0];
  await expect(page.getByRole('button', { name: firstChoice })).toBeVisible({ timeout: 10_000 });

  // Pull-to-refresh simulation. The reloaded page must skip the
  // start screen and re-render the same question.
  await page.reload();

  await expect(page.getByRole('button', { name: firstChoice })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole('button', { name: 'Start Game' })).toBeHidden();

  // The resumed game still walks through to completion — a regression
  // where the resume path left the client in a broken state would
  // surface here as a timeout in the helper loop.
  await answerRemainingQuestions(page);
});
