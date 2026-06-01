import { test, expect } from './fixtures';
import { PASSWORD } from './helpers';
import { adminStatePath } from '../e2e-auth';

// Act as the shared seed admin; this spec mutates only the separately
// registered target player, never the seed admin.
test.use({ storageState: adminStatePath() });

// #450 — admin player management. The golden path: a signed-in admin and
// a second account, land on /admin/players, filter to the unverified
// tab, open the second player's detail row, click Mark verified, and
// confirm the row no longer matches the filter.
//
// The second-player branch deliberately registers via the form rather
// than the helper so the row stays unverified (helpers.markEmailVerified
// is the bypass the admin-action UI is supposed to replace; using it
// here would defeat the test).
test('admin marks an unverified player verified via the detail view', async ({ page, context, browserName, baseURL }) => {
  const targetDisplayName = `e2e-mgmt-target-${browserName}`;

  // Register the second player from a fresh, anonymous context so the
  // admin session is preserved on the main page. storageState is forced
  // empty so the seed-admin cookie this spec runs with does not leak into
  // the new context (a signed-in /register just redirects away); baseURL
  // pins it to the same worker server. The unverified player is the
  // "target" the admin will mark verified.
  const targetContext = await context.browser()!.newContext({ storageState: undefined, baseURL });
  try {
    const targetPage = await targetContext.newPage();
    await targetPage.goto('/register');
    await targetPage.locator('input[name=email]').fill(`${targetDisplayName}@example.test`);
    await targetPage.locator('input[name=display_name]').fill(targetDisplayName);
    await targetPage.locator('input[name=password]').fill(PASSWORD);
    await targetPage.locator('input[name=password_confirm]').fill(PASSWORD);
    await targetPage.locator('button[type=submit]').click();
    // Hard gate (#574): register creates the row then renders the
    // confirmation page at /register with no session. The row exists
    // and is unverified - exactly the state this test needs so it shows
    // up under the Unverified tab.
    await expect(targetPage).toHaveURL(/\/register$/);
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
  await expect(page.getByRole('link', { name: targetDisplayName })).toBeVisible();

  // Filter to the unverified tab. The target appears; the admin row does not.
  await page.getByRole('link', { name: /^Unverified/i }).click();
  await expect(page).toHaveURL(/\/admin\/players\?state=unverified/);
  await expect(page.getByRole('link', { name: targetDisplayName })).toBeVisible();

  // Open the target's detail view.
  await page.getByRole('link', { name: targetDisplayName }).click();
  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
  await expect(page.getByRole('heading', { name: targetDisplayName })).toBeVisible();

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
  await expect(page.getByRole('link', { name: targetDisplayName })).toHaveCount(0);
});
