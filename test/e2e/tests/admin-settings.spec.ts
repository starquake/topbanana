import { test, expect } from './fixtures';
import { registerAdmin, markSuperAdmin, PASSWORD } from './helpers';

// #320 — super-admin settings page. A super admin sees the Settings nav
// link, opens /admin/settings, and promotes another player via the
// username form. A regular admin never sees the link and gets a 404 at
// /admin/settings (the route stays hidden, not forbidden).
test('super admin opens settings and promotes a player', async ({ page, context, browserName }) => {
  const adminUsername = `e2e-super-boss-${browserName}`;
  const targetUsername = `e2e-super-target-${browserName}`;

  // Register the admin (stamps email_verified_at) then bootstrap them to
  // super admin directly, mirroring the CLI promote path.
  await registerAdmin(page, adminUsername);
  markSuperAdmin(adminUsername);

  // Register the player to promote from a fresh context so the admin
  // session survives. A plain player (not in ADMIN_EMAILS) is fine; the
  // promote form only needs the row to exist.
  const targetContext = await context.browser()!.newContext();
  try {
    const targetPage = await targetContext.newPage();
    await targetPage.goto('/register');
    await targetPage.locator('input[name=email]').fill(`${targetUsername}@example.test`);
    await targetPage.locator('input[name=username]').fill(targetUsername);
    await targetPage.locator('input[name=password]').fill(PASSWORD);
    await targetPage.locator('input[name=password_confirm]').fill(PASSWORD);
    await targetPage.locator('button[type=submit]').click();
    await expect(targetPage).toHaveURL('/');
    await targetPage.close();
  } finally {
    await targetContext.close();
  }

  // The Settings nav link is visible for the super admin; follow it.
  await page.goto('/admin');
  const nav = page.getByRole('navigation', { name: 'Primary' });
  await nav.getByRole('link', { name: 'Settings' }).first().click();
  await expect(page).toHaveURL(/\/admin\/settings$/);
  await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible();

  // The super admin lists themselves and not yet the target. Match the
  // username cell exactly so it does not also resolve to the email cell,
  // whose address contains the username as a substring.
  const superAdmins = page.getByRole('region', { name: 'Super admins' });
  await expect(superAdmins.getByRole('cell', { name: adminUsername, exact: true })).toBeVisible();
  await expect(superAdmins.getByRole('cell', { name: targetUsername, exact: true })).toHaveCount(0);

  // Promote the target by username.
  await page.getByRole('textbox', { name: 'Promote a player' }).fill(targetUsername);
  await page.getByRole('button', { name: 'Promote' }).click();

  // PRG lands back on settings with the success banner and the target now
  // listed as a super admin.
  await expect(page).toHaveURL(/\/admin\/settings$/);
  await expect(page.getByText('Player promoted to super admin.')).toBeVisible();
  await expect(superAdmins.getByRole('cell', { name: targetUsername, exact: true })).toBeVisible();
});

test('regular admin does not see Settings and gets 404 at /admin/settings', async ({ page, browserName }) => {
  const username = `e2e-super-plain-${browserName}`;

  await registerAdmin(page, username);

  // No Settings nav link for a plain admin.
  const nav = page.getByRole('navigation', { name: 'Primary' });
  await expect(nav.getByRole('link', { name: 'Settings' })).toHaveCount(0);

  // The route stays hidden: a direct hit is a 404, not a 403.
  const response = await page.goto('/admin/settings');
  expect(response?.status()).toBe(404);
});
