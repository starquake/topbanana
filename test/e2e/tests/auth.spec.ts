import { test, expect } from './fixtures';
import { registerAdmin } from './helpers';

const password = 'correctbatterystaple';

test('register, log out, log back in, and reach the admin dashboard', async ({ page, browserName }) => {
  // Each browser project runs against the same shared server, so use a unique
  // displayName per project and rely on ADMIN_EMAILS (set in playwright.config.ts)
  // to promote every project's registrant to admin.
  const displayName = `e2e-admin-${browserName}`;

  // registerAdmin handles the register POST, satisfies the #111 PR3
  // verified-email gate by stamping email_verified_at via sqlite3, and
  // leaves the browser at /admin/quizzes ready for the assertions.
  await registerAdmin(page, displayName);
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

  // Log back in with the same credentials. Email is the credential after #446.
  await page.locator('input[name=email]').fill(`${displayName}@example.test`);
  await page.locator('input[name=password]').fill(password);
  await page.locator('button[type=submit]').click();

  await expect(page).toHaveURL(/\/admin\/quizzes$/);
  await expect(page.getByRole('heading', { name: 'Quizzes', level: 1 })).toBeVisible();
});

test('login page links to forgot-password', async ({ page }) => {
  await page.goto('/login');

  const link = page.getByRole('link', { name: 'Forgot your password?' });
  await expect(link).toBeVisible();
  await expect(link).toHaveAttribute('href', '/forgot-password');
});

test('deep link while logged out lands at the deep link after login', async ({ page, browserName }) => {
  // #449 e2e: a logged-out admin clicks a link to /admin/email,
  // bounces through /login, signs in, and lands on /admin/email
  // (NOT the role landing at /admin/quizzes). Without the next
  // round-trip the admin would land on /admin/quizzes and have to
  // re-navigate by hand.
  const displayName = `e2e-admin-next-${browserName}`;
  const password = 'correctbatterystaple';

  // Register fresh (and clear the verified-email gate) so the session
  // is set up for the logout + deep-link flow.
  await registerAdmin(page, displayName);

  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/\/login$/);

  // Click the deep link. RequireAdmin redirects to /login?next=/admin/email.
  await page.goto('/admin/email');
  await expect(page).toHaveURL(/\/login\?next=%2Fadmin%2Femail$/);

  await page.locator('input[name=email]').fill(`${displayName}@example.test`);
  await page.locator('input[name=password]').fill(password);
  await page.locator('button[type=submit]').click();

  // Lands on the originally requested deep link, not the role landing.
  await expect(page).toHaveURL(/\/admin\/email$/);
  await expect(page.getByRole('heading', { name: /email diagnostics/i })).toBeVisible();
});
