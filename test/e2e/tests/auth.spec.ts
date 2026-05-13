import { test, expect } from '@playwright/test';

const password = 'correctbatterystaple';

test('register, log out, log back in, and reach the admin dashboard', async ({ page, browserName }) => {
  // Each browser project runs against the same shared server, so use a unique
  // username per project and rely on ADMIN_USERNAMES (set in playwright.config.ts)
  // to promote every project's registrant to admin.
  const username = `e2e-admin-${browserName}`;

  await page.goto('/register');
  await page.locator('input[name=username]').fill(username);
  await page.locator('input[name=password]').fill(password);
  await page.locator('button[type=submit]').click();

  // Successful registration redirects to /admin/quizzes — the redesigned
  // page header (#207) renders the heading "Quizzes" inside h1.title.is-3.
  await expect(page).toHaveURL(/\/admin\/quizzes$/);
  await expect(page.locator('h1.title')).toContainText('Quizzes');

  // Log out via the navbar button. The form posts to /logout, the server
  // clears the cookie and 303s to /login, and the browser follows the redirect.
  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/\/login$/);
  await expect(page.locator('h1.title')).toContainText('Log in');

  // After logout, /admin/quizzes redirects to /login (303). Navigating with the
  // browser follows the redirect; assert on the final URL.
  await page.goto('/admin/quizzes');
  await expect(page).toHaveURL(/\/login$/);
  await expect(page.locator('h1.title')).toContainText('Log in');

  // Log back in with the same credentials.
  await page.locator('input[name=username]').fill(username);
  await page.locator('input[name=password]').fill(password);
  await page.locator('button[type=submit]').click();

  await expect(page).toHaveURL(/\/admin\/quizzes$/);
  await expect(page.locator('h1.title')).toContainText('Quizzes');
});
