import { test, expect } from './fixtures';

const password = 'correctbatterystaple';

test('register, log out, log back in, and reach the admin dashboard', async ({ page, browserName }) => {
  // Each browser project runs against the same shared server, so use a unique
  // username per project and rely on ADMIN_USERNAMES (set in playwright.config.ts)
  // to promote every project's registrant to admin.
  const username = `e2e-admin-${browserName}`;

  await page.goto('/register');
  await page.locator('input[name=username]').fill(username);
  await page.locator('input[name=email]').fill(`${username}@example.test`);
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

  // After logout, /admin/quizzes redirects to /login + ?next=
  // carrying the original URI so the login flow can drop the visitor
  // back on the page they tried to reach (#449).
  await page.goto('/admin/quizzes');
  await expect(page).toHaveURL(/\/login\?next=%2Fadmin%2Fquizzes$/);
  await expect(page.getByRole('heading', { name: 'Log in', level: 1 })).toBeVisible();

  // Log back in with the same credentials.
  await page.locator('input[name=username]').fill(username);
  await page.locator('input[name=password]').fill(password);
  await page.locator('button[type=submit]').click();

  await expect(page).toHaveURL(/\/admin\/quizzes$/);
  await expect(page.getByRole('heading', { name: 'Quizzes', level: 1 })).toBeVisible();
});

test('deep link while logged out lands at the deep link after login', async ({ page, browserName }) => {
  // #449 e2e: a logged-out admin clicks a link to /admin/email,
  // bounces through /login, signs in, and lands on /admin/email
  // (NOT the role landing at /admin/quizzes). Without the next
  // round-trip the admin would land on /admin/quizzes and have to
  // re-navigate by hand.
  const username = `e2e-admin-next-${browserName}`;
  const password = 'correctbatterystaple';

  // Register fresh and log out so the session is empty for the deep-link click.
  await page.goto('/register');
  await page.locator('input[name=username]').fill(username);
  await page.locator('input[name=email]').fill(`${username}@example.test`);
  await page.locator('input[name=password]').fill(password);
  await page.locator('button[type=submit]').click();
  await expect(page).toHaveURL(/\/admin\/quizzes$/);

  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/\/login$/);

  // Click the deep link. RequireAdmin redirects to /login?next=/admin/email.
  await page.goto('/admin/email');
  await expect(page).toHaveURL(/\/login\?next=%2Fadmin%2Femail$/);

  await page.locator('input[name=username]').fill(username);
  await page.locator('input[name=password]').fill(password);
  await page.locator('button[type=submit]').click();

  // Lands on the originally requested deep link, not the role landing.
  await expect(page).toHaveURL(/\/admin\/email$/);
  await expect(page.getByRole('heading', { name: /email diagnostics/i })).toBeVisible();
});
