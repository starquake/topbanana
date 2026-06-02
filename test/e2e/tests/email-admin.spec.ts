import { test, expect } from './fixtures';
import { adminStatePath } from '../e2e-auth';

// Reuse the shared seed-admin session; this spec only needs to be signed
// in as an admin to reach the email diagnostics page.
test.use({ storageState: adminStatePath() });

// #321 - admin email diagnostics view. POST /admin/email/test follows
// the Post/Redirect/Get pattern: the form action 303s to /admin/email
// with a one-shot flash cookie that the GET reads + clears. The PRG
// hop is what keeps Firefox from prompting "resend this form?" on
// refresh, so the URL assertion below is load-bearing.
test('admin can open the email diagnostics page and see status + log', async ({ page }) => {
  await page.goto('/admin/email');
  await expect(page).toHaveURL(/\/admin\/email$/);

  await expect(page.getByRole('heading', { name: /email diagnostics/i })).toBeVisible();
  // SMTP is now wired to the mailpit catch-all and BASE_URL is set
  // per-worker, so both status rows read configured and no
  // "disabled (no-op)" badge appears. Pinning the count keeps the test
  // honest if either wiring regresses.
  await expect(page.getByText('enabled', { exact: true })).toBeVisible();
  await expect(page.getByText(/disabled \(no-op\)/i)).toHaveCount(0);

  const recipient = page.locator('input[name=to]');
  await expect(recipient).toBeVisible();
  await recipient.fill('ops@example.test');

  // Submit. With PRG the browser lands on /admin/email (not
  // /admin/email/test); the banner is delivered via the flash cookie
  // the GET clears. The send now reaches mailpit, so the notice reports
  // success rather than the not-configured error.
  await page.getByRole('button', { name: /send test email/i }).click();
  await expect(page).toHaveURL(/\/admin\/email$/);
  await expect(page.getByRole('status')).toContainText(/test email sent to ops@example.test/i);

  // Flash is one-shot: navigating away and back must drop the banner.
  await page.goto('/admin');
  await page.goto('/admin/email');
  await expect(page.getByRole('status')).toHaveCount(0);
});
