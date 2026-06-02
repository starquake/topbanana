import { test, expect } from './fixtures';
import { adminStatePath } from '../e2e-auth';
import { PASSWORD } from './helpers';
import { waitForEmailLink } from './mailpit';

// Invite accept-via-link round-trip. The admin-invites spec covers the
// management UI but stops at the send (it predates SMTP in e2e); this
// spec completes the loop now that mailpit catches the invite mail: an
// admin sends an invite, and the invitee accepts it through the emailed
// link in a separate browser context, landing signed in.
test.use({ storageState: adminStatePath() });

test('invite accept link creates an account and signs it in', async ({ page, browser, browserName }) => {
  const inviteEmail = `e2e-invite-rt-${browserName}@example.test`;
  const inviteeName = `e2e-invitee-rt-${browserName}`;

  // Send the invite as the signed-in admin.
  await page.goto('/admin/invites');
  await page.locator('input[name=email]').fill(inviteEmail);
  await page.getByRole('button', { name: 'Send invite' }).click();
  await expect(page).toHaveURL('/admin/invites');
  await expect(page.getByRole('cell', { name: inviteEmail })).toBeVisible();

  // Read the accept link mailpit caught for the invitee.
  const link = await waitForEmailLink(inviteEmail, '/accept-invite?token=');

  // Accept in a fresh context so the admin session on `page` is left
  // intact. The emailed link is absolute, so this context needs no
  // baseURL of its own.
  const inviteeContext = await browser.newContext();
  try {
    const inviteePage = await inviteeContext.newPage();
    await inviteePage.goto(link);
    await expect(inviteePage.getByRole('heading', { name: 'Accept your invite' })).toBeVisible();
    await inviteePage.locator('input[name=display_name]').fill(inviteeName);
    await inviteePage.locator('input[name=password]').fill(PASSWORD);
    await inviteePage.locator('input[name=confirm]').fill(PASSWORD);
    await inviteePage.getByRole('button', { name: 'Create account' }).click();

    // Accept creates an already-verified account and auto-signs in, so
    // the new session lands without a separate login step.
    await expect(inviteePage.getByRole('button', { name: 'Log out' })).toBeVisible();
  } finally {
    await inviteeContext.close();
  }
});
