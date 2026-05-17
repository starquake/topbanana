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

  // After the last addQuestion the quiz view is loaded. The redesign
  // replaced the old questions <table> with a card-style article grid;
  // each question lives inside an <article class="q-row">. Correct
  // options are marked with aria-label="Correct" on a hidden span.
  for (const [index, q] of QUIZ_QUESTIONS.entries()) {
    const row = page.locator('article.q-row').nth(index);
    await expect(row).toContainText(q.text);
    await expect(row.getByLabel('Correct')).toHaveCount(q.correctIndices.length);
  }

  // The new quiz title should appear in the admin quiz list. The list is
  // a Tailwind card grid; we key off the link role.
  await page.goto('/admin/quizzes');
  await expect(page.getByRole('link', { name: quizTitle })).toBeVisible();

  // The "Top Banana!" brand in the navbar should be a real link that lands
  // on /admin — guards against regressing back to href="#".
  await page.getByRole('link', { name: 'Top Banana!' }).click();
  await expect(page).toHaveURL(/\/admin$/);

  // Open the share modal on the quiz view and confirm the rendered share
  // URL points at /play/<slug>-<id>. Don't try to verify the clipboard
  // (browser permission model varies); just check the user-visible link.
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  await page.getByRole('button', { name: 'Share' }).click();
  const shareLinkText = await page.locator('.share-link').textContent();
  expect(shareLinkText).toMatch(/\/play\/[a-z0-9-]+-\d+$/);
});
