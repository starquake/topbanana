import { test, expect } from '@playwright/test';
import { registerAdmin, createQuizWithQuestions, QUIZ_QUESTIONS } from './helpers';

test('register, create a quiz with varied questions, and see them on the quiz view', async ({ page, browserName }) => {
  // Each browser project runs against the same shared server, so use unique
  // names per project. ADMIN_USERNAMES (in playwright.config.ts) whitelists
  // these usernames so registration promotes them to admin.
  const username = `e2e-admin-create-${browserName}`;
  const quizTitle = `E2E Admin Quiz ${browserName}`;

  await registerAdmin(page, username);
  await createQuizWithQuestions(page, quizTitle);

  // After the last addQuestion the quiz view is loaded. For each question,
  // assert its row exists and contains the expected number of "correct" icons.
  // This covers the 1-correct, 0-correct, all-correct, and 3-of-4 cases.
  for (const [index, q] of QUIZ_QUESTIONS.entries()) {
    const row = page.locator('table tbody tr').nth(index);
    await expect(row).toContainText(q.text);
    await expect(row.locator('.has-text-success')).toHaveCount(q.correctIndices.length);
  }

  // The new quiz title should appear in the admin quiz list. Match by cell
  // (rather than by row position) so the assertion is stable across runs that
  // share a long-lived SQLite file.
  await page.goto('/admin/quizzes');
  await expect(page.getByRole('cell', { name: quizTitle })).toBeVisible();

  // The "Top Banana!" brand in the navbar should be a real link that lands
  // on /admin — guards against regressing back to href="#".
  await page.getByRole('link', { name: 'Top Banana!' }).click();
  await expect(page).toHaveURL(/\/admin$/);
});
