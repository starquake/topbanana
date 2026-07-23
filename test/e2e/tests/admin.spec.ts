import { test, expect } from './fixtures';
import {
  createQuizWithQuestions,
  QUIZ_QUESTIONS,
  registerAdmin,
} from './helpers';

// #246 — admin spoiler toggle. Options sit inside a <details> closed by
// default so the admin can present the quiz on screen without exposing
// answers; the summary toggles per question, independent of siblings.
test('register, create a quiz with varied questions, and see them on the quiz view', async ({ page, browserName }) => {
  // Each browser project runs against the same shared server, so use unique
  // names per project. ADMIN_EMAILS (in playwright.config.ts) whitelists
  // these emails so registration promotes them to admin.
  const displayName = `e2e-admin-create-${browserName}`;
  const quizTitle = `E2E Admin Quiz ${browserName}`;

  await registerAdmin(page, displayName);
  await createQuizWithQuestions(page, quizTitle);

  // The questions live in the editor now (#1260). Open it and check each is in
  // the rail with the right number of correct options; the badge cluster
  // carries the count, so nothing needs expanding.
  await page.getByTestId('open-question-editor').click();
  await expect(page.locator('#questions-list')).toBeVisible();
  for (const [index, q] of QUIZ_QUESTIONS.entries()) {
    const row = page.locator('article.q-row').nth(index);
    await expect(row).toContainText(q.text);
    await expect(row.getByTestId('q-badge-correct'))
      .toContainText(`${q.correctIndices.length} correct`);
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
