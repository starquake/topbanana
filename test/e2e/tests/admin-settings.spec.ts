import type { BrowserContext } from '@playwright/test';

import { test, expect } from './fixtures';
import { registerAdmin, markSuperAdmin, PASSWORD } from './helpers';

// Registers a fresh plain player in an isolated browser context so the
// admin session on the main page is left untouched. Returns nothing; the
// row simply needs to exist for the admin to act on.
async function registerPlayer(
  context: BrowserContext,
  username: string,
): Promise<void> {
  const playerContext = await context.browser()!.newContext();
  try {
    const playerPage = await playerContext.newPage();
    await playerPage.goto('/register');
    await playerPage.locator('input[name=email]').fill(`${username}@example.test`);
    await playerPage.locator('input[name=username]').fill(username);
    await playerPage.locator('input[name=password]').fill(PASSWORD);
    await playerPage.locator('input[name=password_confirm]').fill(PASSWORD);
    await playerPage.locator('button[type=submit]').click();
    await expect(playerPage).toHaveURL('/');
    await playerPage.close();
  } finally {
    await playerContext.close();
  }
}

// #527 — role management moved to the player detail page. A super admin
// opens a player's detail view, sets their role with the selector, and
// the change sticks. The Settings page lists super admins and demotes
// them back to plain admin via the repointed /role endpoint; it no
// longer carries a username promote form.
test('super admin promotes a player to super admin from the detail page', async ({ page, context, browserName }) => {
  const adminUsername = `e2e-super-boss-${browserName}`;
  const targetUsername = `e2e-super-target-${browserName}`;

  await registerAdmin(page, adminUsername);
  markSuperAdmin(adminUsername);
  await registerPlayer(context, targetUsername);

  // Open the target's detail view from the players list.
  await page.goto('/admin/players');
  await page.getByRole('link', { name: targetUsername }).click();
  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
  await expect(page.getByRole('heading', { name: targetUsername })).toBeVisible();

  // The role selector starts at 'player' for a fresh registration.
  const roleSelect = page.getByLabel('Role');
  await expect(roleSelect).toHaveValue('player');

  // Promote straight to super admin and accept the confirm dialog.
  await roleSelect.selectOption('super_admin');
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Save role' }).click();

  // PRG returns to the detail page with the success flash; the selector
  // now reflects the persisted level.
  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
  await expect(page.getByText('Player promoted to super admin.')).toBeVisible();
  await expect(page.getByLabel('Role')).toHaveValue('super_admin');

  // The Settings super-admin table now lists the target.
  await page.goto('/admin/settings');
  await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible();
  const superAdmins = page.getByRole('region', { name: 'Super admins' });
  await expect(superAdmins.getByRole('cell', { name: targetUsername, exact: true })).toBeVisible();

  // The username promote form is gone.
  await expect(page.getByRole('textbox', { name: 'Promote a player' })).toHaveCount(0);
});

test('super admin demotes a super admin from the settings table', async ({ page, context, browserName }) => {
  const adminUsername = `e2e-demote-boss-${browserName}`;
  const targetUsername = `e2e-demote-target-${browserName}`;

  await registerAdmin(page, adminUsername);
  markSuperAdmin(adminUsername);
  // Bootstrap the target straight to super admin so the table has a row
  // to demote that is not the last super admin.
  await registerPlayer(context, targetUsername);
  markSuperAdmin(targetUsername);

  await page.goto('/admin/settings');
  const superAdmins = page.getByRole('region', { name: 'Super admins' });
  await expect(superAdmins.getByRole('cell', { name: targetUsername, exact: true })).toBeVisible();

  // The target's row Demote button posts to /role with role=admin.
  const targetRow = superAdmins.getByRole('row', { name: new RegExp(`^${targetUsername}\\b`) });
  page.once('dialog', (dialog) => dialog.accept());
  await targetRow.getByRole('button', { name: 'Demote' }).click();

  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
  await expect(page.getByText('Player role set to admin.')).toBeVisible();
  await expect(page.getByLabel('Role')).toHaveValue('admin');

  // Back on settings the demoted player no longer appears in the table.
  await page.goto('/admin/settings');
  await expect(superAdmins.getByRole('cell', { name: targetUsername, exact: true })).toHaveCount(0);
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
