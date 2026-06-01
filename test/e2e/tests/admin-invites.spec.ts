import { test, expect } from './fixtures';
import { adminStatePath } from '../e2e-auth';

// Reuse the shared seed-admin session; this spec only needs to be signed
// in as an admin to drive the invite-management UI.
test.use({ storageState: adminStatePath() });

// #318 slice 2 - admin invite management UI. Admin-side only: the e2e
// harness has no SMTP and the raw invite token is never stored, so this
// covers the dashboard tile, the create form, and the per-row resend /
// revoke actions on the pending list. Accept-via-link is exercised in the
// integration suite, where the token is mintable directly.
test('admin creates, resends, and revokes an invite from the management page', async ({ page, browserName }) => {
  const inviteEmail = `e2e-invitee-${browserName}@example.test`;

  // From the dashboard, the Invite tile leads to the management page.
  await page.goto('/admin');
  await page.getByRole('link', { name: 'Invite player' }).click();
  await expect(page).toHaveURL('/admin/invites');
  await expect(page.getByRole('heading', { name: 'Invites', exact: true })).toBeVisible();

  // Submit the create form; SMTP is not configured in e2e, so the banner
  // says the link exists but no mail went out. The PRG lands back on the
  // list with the new invite visible.
  await page.locator('input[name=email]').fill(inviteEmail);
  await page.locator('input[name=note]').fill('a friend');
  await page.getByRole('button', { name: 'Send invite' }).click();
  await expect(page).toHaveURL('/admin/invites');
  // Scope to the table cell; the email also appears in the success banner.
  await expect(page.getByRole('cell', { name: inviteEmail })).toBeVisible();

  // The invite's row carries Resend + Revoke buttons. Resend keeps it
  // listed (the link is rotated, not removed).
  const row = page.getByRole('row').filter({ hasText: inviteEmail });
  await row.getByRole('button', { name: 'Resend' }).click();
  await expect(page).toHaveURL('/admin/invites');
  await expect(page.getByText('Invite link rotated', { exact: false })).toBeVisible();
  await expect(page.getByRole('cell', { name: inviteEmail })).toBeVisible();

  // Revoke drops it from the pending list. The confirm() dialog is auto-
  // accepted.
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('row').filter({ hasText: inviteEmail })
    .getByRole('button', { name: 'Revoke' }).click();
  await expect(page).toHaveURL('/admin/invites');
  await expect(page.getByText('Invite revoked', { exact: false })).toBeVisible();
  await expect(page.getByRole('cell', { name: inviteEmail })).toHaveCount(0);
});
