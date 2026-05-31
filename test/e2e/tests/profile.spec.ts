import { test, expect } from './fixtures';
import { registerForPending, login, markEmailVerified } from './helpers';

// Registers a plain player (not via registerAdmin, which assumes the
// ADMIN_EMAILS promotion slot) and clears the verified-email gate so
// /profile — which requires a verified email — is reachable.
test('profile page links to change-email and change-password', async ({ page, browserName }) => {
  const username = `e2e-profile-${browserName}-${Date.now()}`;

  // The hard gate (#574) means register no longer signs the player in:
  // it renders the confirmation page with no session. Verify the row
  // directly, then log in to clear the verified-email gate /profile
  // enforces and obtain a session.
  await registerForPending(page, username);
  markEmailVerified(username);
  await login(page, username);

  await page.goto('/profile');

  const email = page.getByRole('link', { name: 'Change email' });
  await expect(email).toBeVisible();
  await expect(email).toHaveAttribute('href', '/profile/email');

  const password = page.getByRole('link', { name: 'Change password' });
  await expect(password).toBeVisible();
  await expect(password).toHaveAttribute('href', '/profile/password');
});
