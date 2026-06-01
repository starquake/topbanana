import { test, expect } from './fixtures';
import { registerAdmin } from './helpers';

// #321 - admin email diagnostics view. POST /admin/email/test follows
// the Post/Redirect/Get pattern: the form action 303s to /admin/email
// with a one-shot flash cookie that the GET reads + clears. The PRG
// hop is what keeps Firefox from prompting "resend this form?" on
// refresh, so the URL assertion below is load-bearing.
test('admin can open the email diagnostics page and see status + log', async ({ page, browserName }) => {
  const displayName = `e2e-admin-email-${browserName}`;

  await registerAdmin(page, displayName);

  await page.goto('/admin/email');
  await expect(page).toHaveURL(/\/admin\/email$/);

  await expect(page.getByRole('heading', { name: /email diagnostics/i })).toBeVisible();
  // One "disabled (no-op)" badge in e2e: SMTP is unwired. BASE_URL is
  // configured per-worker (the invite flow needs it, #318), so its badge
  // does not appear. Pinning the count keeps the test honest if SMTP
  // becomes configurable later.
  await expect(page.getByText(/disabled \(no-op\)/i)).toHaveCount(1);

  const recipient = page.locator('input[name=to]');
  await expect(recipient).toBeVisible();
  await recipient.fill('ops@example.test');

  // Submit. With PRG the browser lands on /admin/email (not
  // /admin/email/test); the banner is delivered via the flash cookie
  // the GET clears.
  await page.getByRole('button', { name: /send test email/i }).click();
  await expect(page).toHaveURL(/\/admin\/email$/);
  await expect(page.getByRole('alert')).toContainText(/email is not configured/i);

  // Flash is one-shot: navigating away and back must drop the banner.
  await page.goto('/admin');
  await page.goto('/admin/email');
  await expect(page.getByRole('alert')).toHaveCount(0);
});
