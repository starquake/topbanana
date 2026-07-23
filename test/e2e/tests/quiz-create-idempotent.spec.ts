import { test, expect } from './fixtures';
import { adminStatePath } from '../e2e-auth';
import { createQuizWithQuestions, QUIZ_QUESTIONS } from './helpers';

test.use({ storageState: adminStatePath() });

// Pins the #1090 contract for the shared createQuizWithQuestions helper. A
// Playwright worker keeps one SQLite DB for its whole life, and the quiz slug is
// derived from the title (the sole UNIQUE column on quizzes). So creating the
// same title twice in a worker - under --repeat-each, or on a retry that re-runs
// the setup helper - hits a 409 slug collision that re-renders the create form
// in place at /admin/quizzes. The helper must recover (delete the leftover and
// re-create) instead of timing out on the create redirect, and the resulting
// quiz must carry each question exactly once - no duplicates, no state inherited
// from the previous attempt.
test('re-creating a quiz with an existing title yields a clean quiz, not a slug-collision timeout', async ({
  page,
  browserName,
}) => {
  const title = `E2E Idempotent Quiz ${browserName}`;

  await createQuizWithQuestions(page, title, QUIZ_QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  // Second create with the same title: the slug collision is recovered instead
  // of dead-ending at the create redirect.
  await createQuizWithQuestions(page, title, QUIZ_QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await expect(page.locator('.q-text')).toHaveCount(QUIZ_QUESTIONS.length);
  for (const q of QUIZ_QUESTIONS) {
    await expect(page.locator('.q-text', { hasText: q.text })).toHaveCount(1);
  }
});
