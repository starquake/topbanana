import { test, expect } from './fixtures';
import { registerAdmin } from './helpers';

// #321 - admin email diagnostics view. The e2e test boots the admin
// surface, opens /admin/email, and confirms the status panel + log
// table render. With SMTP unconfigured in CI, the test-send button
// surfaces the verbatim "not configured" banner; a Mailpit inbox
// check is deferred to the integration test (which also runs against
// the no-op mailer) until the e2e harness gets a real Mailpit-backed
// run. TODO: assert delivery against Mailpit once the e2e harness
// boots with SMTP_HOST=mailpit (#321 follow-up).
test('admin can open the email diagnostics page and see status + log', async ({ page, browserName }) => {
  const username = `e2e-admin-email-${browserName}`;

  await registerAdmin(page, username);

  await page.goto('/admin/email');
  await expect(page).toHaveURL(/\/admin\/email$/);

  // Page identity - the diagnostics view renders this header.
  await expect(page.getByRole('heading', { name: /email diagnostics/i })).toBeVisible();

  // Status panel pins the "disabled (no-op)" badge when SMTP env
  // vars are unset (the default in the e2e harness).
  await expect(page.getByText(/disabled \(no-op\)/i)).toBeVisible();

  // Test-send form exists; the recipient field defaults to empty
  // because the registered admin has no email on file yet.
  const recipient = page.locator('input[name=to]');
  await expect(recipient).toBeVisible();
  await recipient.fill('ops@example.test');

  // Submit and confirm we see the "not configured" banner inline. The
  // verbatim error also lands in the recent-send log row below, so the
  // assertion targets the banner by role="alert" to keep strict mode
  // happy.
  await page.getByRole('button', { name: /send test email/i }).click();
  await expect(page).toHaveURL(/\/admin\/email\/test$/);
  await expect(page.getByRole('alert')).toContainText(/email is not configured/i);
});
