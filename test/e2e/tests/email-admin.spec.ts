import { test, expect } from './fixtures';
import { registerAdmin } from './helpers';

// #321 - admin email diagnostics view. POST /admin/email/test follows
// the Post/Redirect/Get pattern: the form action 303s to /admin/email
// with a one-shot flash cookie that the GET reads + clears. The PRG
// hop is what keeps Firefox from prompting "resend this form?" on
// refresh, so the URL assertion below is load-bearing.
test('admin can open the email diagnostics page and see status + log', async ({ page, browserName }) => {
  const username = `e2e-admin-email-${browserName}`;

  await registerAdmin(page, username);

  await page.goto('/admin/email');
  await expect(page).toHaveURL(/\/admin\/email$/);

  await expect(page.getByRole('heading', { name: /email diagnostics/i })).toBeVisible();
  // Two "disabled (no-op)" badges in e2e: SMTP is unwired AND BASE_URL
  // is empty (#495). Pinning the count keeps the test honest if either
  // becomes configurable later.
  await expect(page.getByText(/disabled \(no-op\)/i)).toHaveCount(2);

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
