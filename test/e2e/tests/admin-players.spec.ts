import { test, expect } from './fixtures';
import { registerAdmin, PASSWORD } from './helpers';

// #450 — admin player management. The golden path: register an admin
// + a second account, land on /admin/players, filter to the
// unverified tab, open the second player's detail row, click Mark
// verified, and confirm the row no longer matches the filter.
//
// The second-player branch deliberately registers via the form rather
// than the helper so the row stays unverified (helpers.markEmailVerified
// is the bypass the admin-action UI is supposed to replace; using it
// here would defeat the test).
test('admin marks an unverified player verified via the detail view', async ({ page, context, browserName }) => {
  const adminUsername = `e2e-mgmt-admin-${browserName}`;
  const targetUsername = `e2e-mgmt-target-${browserName}`;

  // Register the admin via the helper (which stamps email_verified_at
  // for the admin so /admin/* loads); the helper finishes on
  // /admin/quizzes.
  await registerAdmin(page, adminUsername);

  // Register the second player from a fresh context so the admin
  // session is preserved on the main page. The unverified player is
  // the "target" the admin will mark verified.
  const targetContext = await context.browser()!.newContext();
  try {
    const targetPage = await targetContext.newPage();
    await targetPage.goto('/register');
    await targetPage.locator('input[name=email]').fill(`${targetUsername}@example.test`);
    await targetPage.locator('input[name=username]').fill(targetUsername);
    await targetPage.locator('input[name=password]').fill(PASSWORD);
    await targetPage.locator('input[name=password_confirm]').fill(PASSWORD);
    await targetPage.locator('button[type=submit]').click();
    // The second registration lands on / (player role) without ever
    // verifying the address.
    await expect(targetPage).toHaveURL('/');
    await targetPage.close();
  } finally {
    await targetContext.close();
  }

  // Land on the players list and verify the tab shows the unverified row.
  await page.goto('/admin/players');
  await expect(page).toHaveURL('/admin/players');
  await expect(page.getByRole('heading', { name: 'Players' })).toBeVisible();
  // Match the target's link in the row rather than getByText, which
  // would also match the substring inside the email cell.
  await expect(page.getByRole('link', { name: targetUsername })).toBeVisible();

  // Filter to the unverified tab. The target appears; the admin row does not.
  await page.getByRole('link', { name: /^Unverified/i }).click();
  await expect(page).toHaveURL(/\/admin\/players\?state=unverified/);
  await expect(page.getByRole('link', { name: targetUsername })).toBeVisible();

  // Open the target's detail view.
  await page.getByRole('link', { name: targetUsername }).click();
  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
  await expect(page.getByRole('heading', { name: targetUsername })).toBeVisible();

  // Confirm before the click because the form's onsubmit handler asks.
  page.once('dialog', async (dialog) => {
    await dialog.accept();
  });
  await page.getByRole('button', { name: 'Mark verified' }).click();

  // The PRG lands us back on the same detail page; the success banner
  // and the new audit-trail entry confirm the action ran.
  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
  await expect(page.getByText('Player marked verified.')).toBeVisible();
  // Scope the audit-row assertion to the "Recent admin actions"
  // section; the success banner above also contains "marked verified"
  // as a case-insensitive substring.
  const audit = page.getByRole('region', { name: 'Recent admin actions' });
  await expect(audit.getByText('Marked verified')).toBeVisible();

  // Walk back to the unverified tab and confirm the target row is gone.
  await page.goto('/admin/players?state=unverified');
  await expect(page.getByRole('link', { name: targetUsername })).toHaveCount(0);
});
