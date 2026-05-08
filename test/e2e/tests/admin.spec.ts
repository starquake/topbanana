import { test, expect } from '@playwright/test';

const password = 'correctbatterystaple';

test('register, create a quiz with a question, and see it on the list', async ({ page, browserName }) => {
  // Each browser project runs against the same shared server, so use unique
  // names per project. ADMIN_USERNAMES (in playwright.config.ts) whitelists
  // these usernames so registration promotes them to admin.
  const username = `e2e-admin-create-${browserName}`;
  const quizTitle = `E2E Admin Quiz ${browserName}`;

  // Register a new admin user. Successful registration redirects to /admin/quizzes.
  await page.goto('/register');
  await page.locator('input[name=username]').fill(username);
  await page.locator('input[name=password]').fill(password);
  await page.locator('button[type=submit]').click();
  await expect(page).toHaveURL(/\/admin\/quizzes$/);

  // Create a new quiz. The save handler redirects to the new quiz view at
  // /admin/quizzes/{id}, where we can add a question.
  await page.goto('/admin/quizzes/new');
  await page.locator('input[name=title]').fill(quizTitle);
  await page.locator('input[name=description]').fill('E2E generated quiz');
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  // From the quiz view, follow the "Add question" link to the question form.
  await page.getByRole('link', { name: /add question/i }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+\/questions\/new$/);

  // Fill in question text, position, and four options. Mark option B (index 1)
  // as correct. The form posts to /admin/quizzes/{id}/questions and the server
  // redirects back to the quiz view on success.
  await page.locator('input[name=text]').fill('What is 2+2?');
  await page.locator('input[name=position]').fill('1');
  await page.locator('input[name="option[0].text"]').fill('3');
  await page.locator('input[name="option[1].text"]').fill('4');
  await page.locator('input[name="option[2].text"]').fill('5');
  await page.locator('input[name="option[3].text"]').fill('6');
  await page.locator('input[name="option[1].correct"]').check();
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  // The new quiz title should appear in the admin quiz list. Match by cell
  // (rather than by row position) so the assertion is stable across runs that
  // share a long-lived SQLite file.
  await page.goto('/admin/quizzes');
  await expect(page.getByRole('cell', { name: quizTitle })).toBeVisible();
});
