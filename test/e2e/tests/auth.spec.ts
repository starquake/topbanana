import { test, expect } from './fixtures';

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

  // Successful registration redirects to /admin/quizzes. Match the page
  // heading by role rather than by class — admin + auth are both Tailwind
  // now, so role-based locators survive future reskin tweaks.
  await expect(page).toHaveURL(/\/admin\/quizzes$/);
  await expect(page.getByRole('heading', { name: 'Quizzes', level: 1 })).toBeVisible();

  // Log out via the navbar button. The form posts to /logout, the server
  // clears the cookie and 303s to /login, and the browser follows the redirect.
  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/\/login$/);
  await expect(page.getByRole('heading', { name: 'Log in', level: 1 })).toBeVisible();

  // After logout, /admin/quizzes redirects to /login (303). Navigating with the
  // browser follows the redirect; assert on the final URL.
  await page.goto('/admin/quizzes');
  await expect(page).toHaveURL(/\/login$/);
  await expect(page.getByRole('heading', { name: 'Log in', level: 1 })).toBeVisible();

  // Log back in with the same credentials.
  await page.locator('input[name=username]').fill(username);
  await page.locator('input[name=password]').fill(password);
  await page.locator('button[type=submit]').click();

  await expect(page).toHaveURL(/\/admin\/quizzes$/);
  await expect(page.getByRole('heading', { name: 'Quizzes', level: 1 })).toBeVisible();
});
