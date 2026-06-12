import { test, expect } from './fixtures';
import {
  seedQuiz,
  startQuizAsAnonymous,
  answerRemainingQuestions,
  installPlaythroughClock,
  QUIZ_QUESTIONS,
} from './helpers';
import { adminStatePath } from '../e2e-auth';

// Seed the quiz as the shared admin via the JSON importer, then clear the
// admin cookie so the resume flow runs anonymous.
test.use({ storageState: adminStatePath() });

// #310: mobile pull-to-refresh used to bounce the player back to the
// start screen mid-question. After the resume fix the boot path
// probes /my-game; an in-flight game skips the start screen and
// re-renders the same question via the idempotent /questions/next
// path.
test('mid-game reload lands back on the question, not the start screen', async ({ page, browserName }) => {
  // The resumed game plays through four questions with ~2-3s feedback
  // pauses each; setup is one import. 45s leaves headroom on slow CI.
  test.setTimeout(45_000);

  const quizTitle = `E2E Resume Quiz ${browserName}`;

  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();

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

  // Install the virtual clock now (not before page.goto) so the
  // pre-reload boot path keeps real timing - page.reload would
  // otherwise re-paint Q1 against a frozen clock and the resumed
  // reveal beat would stall. From here on the helper fast-forwards
  // per-question timers via runFor.
  await installPlaythroughClock(page);

  // The resumed game still walks through to completion — a regression
  // where the resume path left the client in a broken state would
  // surface here as a timeout in the helper loop.
  await answerRemainingQuestions(page);
});
