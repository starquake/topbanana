import { test, expect } from './fixtures';
import { PASSWORD, markEmailVerified } from './helpers';

// Registers a plain player (not via registerAdmin, which assumes the
// ADMIN_EMAILS promotion slot) and clears the verified-email gate so
// /profile — which requires a verified email — is reachable.
test('profile page links to change-email and change-password', async ({ page, browserName }) => {
  const username = `e2e-profile-${browserName}-${Date.now()}`;

  await page.goto('/register');
  await page.locator('input[name=email]').fill(`${username}@example.test`);
  await page.locator('input[name=username]').fill(username);
  await page.locator('input[name=password]').fill(PASSWORD);
  await page.locator('input[name=password_confirm]').fill(PASSWORD);
  await page.locator('button[type=submit]').click();

  // Registration sets the session and redirects off /register (a plain
  // player lands on /, an admin on /verify-email/pending). Either way the
  // row now exists, so wait for the redirect, then clear the verified-email
  // gate that /profile enforces.
  await expect(page).not.toHaveURL(/\/register$/);
  markEmailVerified(username);

  await page.goto('/profile');

  const email = page.getByRole('link', { name: 'Change email' });
  await expect(email).toBeVisible();
  await expect(email).toHaveAttribute('href', '/profile/email');

  const password = page.getByRole('link', { name: 'Change password' });
  await expect(password).toBeVisible();
  await expect(password).toHaveAttribute('href', '/profile/password');
});
