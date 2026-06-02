import { test, expect } from './fixtures';
import { login, registerForPending } from './helpers';
import { waitForEmailLink } from './mailpit';

// Email round-trip spec. Unlike the rest of the suite (which fakes the
// verify step by stamping email_verified_at), this drives the real
// browser flow end to end: the worker server sends mail to the shared
// mailpit catch-all, and the spec reads the link back over mailpit's API
// and follows it. A per-browser recipient keeps the shared inbox
// unambiguous under parallel workers.
//
// Only the verify link is exercised here. The reset-link leg is the same
// mailpit mechanism, and exercising it would need the forgot-password
// form whose 60s per-IP cooldown cooldown.spec.ts deliberately depends
// on - the two cannot share one server config. Reset is covered by the
// integration suite (TestResetPassword_HappyPath) and the invite link
// round-trip lives in invite-roundtrip.spec.ts.

test('verify-email link signs the account in once followed', async ({ page, browserName }) => {
  const displayName = `e2e-verify-rt-${browserName}`;
  const email = `${displayName}@example.test`;

  // Register through the UI; the hard gate leaves the account unverified
  // with no session, and a verification mail is dispatched.
  await registerForPending(page, displayName);

  // Read the verification link mailpit caught and follow it in the browser.
  const link = await waitForEmailLink(email, '/verify-email?token=');
  await page.goto(link);
  await expect(page.getByRole('heading', { name: 'Email verified' })).toBeVisible();

  // The account can now sign in and hold a session (the navbar Log out
  // button is the role-agnostic signed-in marker).
  await login(page, displayName);
  await expect(page.getByRole('button', { name: 'Log out' })).toBeVisible();
});
