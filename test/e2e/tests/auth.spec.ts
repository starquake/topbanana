import { test, expect } from '@playwright/test';

const password = 'correctbatterystaple';

test('register, log out, log back in, and reach the admin dashboard', async ({ page, browserName }) => {
  // Each browser project runs against the same shared server, so use a unique
  // username per project and rely on ADMIN_USERNAMES (set in playwright.config.ts)
  // to promote every project's registrant to admin.
  const username = `e2e-admin-${browserName}`;
  // Register: with a fresh DB, the first password-bearing user is promoted to admin
  // by the SQL CASE in CreatePlayerWithCredentials, so this run also exercises the
  // bootstrap-admin path.
  await page.goto('/register');
  await page.locator('input[name=username]').fill(username);
  await page.locator('input[name=password]').fill(password);
  await page.locator('button[type=submit]').click();

  // Successful registration redirects to /admin/quizzes.
  await expect(page).toHaveURL(/\/admin\/quizzes$/);
  await expect(page.locator('h1.title')).toContainText('Admin Dashboard');

  // Log out via POST /logout. There is no logout button in the admin UI yet
  // (tracked by #113); for now we drive the endpoint directly using the page's
  // request context so cookies are shared.
  const logoutResp = await page.request.post('/logout', { maxRedirects: 0 });
  expect(logoutResp.status()).toBe(303);
  expect(logoutResp.headers()['location']).toBe('/login');

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
  await expect(page.locator('h1.title')).toContainText('Admin Dashboard');
});
