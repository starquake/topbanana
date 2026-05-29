import type { BrowserContext } from '@playwright/test';

import { test, expect } from './fixtures';
import { registerAdmin, markAdmin, markHost, PASSWORD } from './helpers';

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

// #527/#538 — role management lives on the player detail page. An Admin
// opens a player's detail view, sets their role with the selector, and the
// change sticks. The Settings page lists Admins and demotes them back to
// Host via the repointed /role endpoint; it no longer carries a username
// promote form.
test('admin promotes a player to admin from the detail page', async ({ page, context, browserName }) => {
  const adminUsername = `e2e-admin-boss-${browserName}`;
  const targetUsername = `e2e-admin-target-${browserName}`;

  await registerAdmin(page, adminUsername);
  markAdmin(adminUsername);
  await registerPlayer(context, targetUsername);

  // Open the target's detail view from the players list.
  await page.goto('/admin/players');
  await page.getByRole('link', { name: targetUsername }).click();
  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
  await expect(page.getByRole('heading', { name: targetUsername })).toBeVisible();

  // The role selector starts at 'player' for a fresh registration.
  const roleSelect = page.getByLabel('Role');
  await expect(roleSelect).toHaveValue('player');

  // Promote straight to Admin and accept the confirm dialog.
  await roleSelect.selectOption('admin');
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Save role' }).click();

  // PRG returns to the detail page with the success flash; the selector
  // now reflects the persisted role.
  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
  await expect(page.getByText('Player role set to admin.')).toBeVisible();
  await expect(page.getByLabel('Role')).toHaveValue('admin');

  // The Settings Admins table now lists the target.
  await page.goto('/admin/settings');
  await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible();
  const admins = page.getByRole('region', { name: 'Admins' });
  await expect(admins.getByRole('cell', { name: targetUsername, exact: true })).toBeVisible();

  // The username promote form is gone.
  await expect(page.getByRole('textbox', { name: 'Promote a player' })).toHaveCount(0);
});

// The role selector can set every tier, ending on Host.
test('admin sets a player to host from the detail page', async ({ page, context, browserName }) => {
  const adminUsername = `e2e-host-boss-${browserName}`;
  const targetUsername = `e2e-host-target-${browserName}`;

  await registerAdmin(page, adminUsername);
  markAdmin(adminUsername);
  await registerPlayer(context, targetUsername);

  await page.goto('/admin/players');
  await page.getByRole('link', { name: targetUsername }).click();
  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);

  const roleSelect = page.getByLabel('Role');
  await expect(roleSelect).toHaveValue('player');

  await roleSelect.selectOption('host');
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Save role' }).click();

  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
  await expect(page.getByText('Player role set to host.')).toBeVisible();
  await expect(page.getByLabel('Role')).toHaveValue('host');
});

test('admin demotes an admin from the settings table', async ({ page, context, browserName }) => {
  const adminUsername = `e2e-demote-boss-${browserName}`;
  const targetUsername = `e2e-demote-target-${browserName}`;

  await registerAdmin(page, adminUsername);
  markAdmin(adminUsername);
  // Bootstrap the target straight to Admin so the table has a row to
  // demote that is not the last Admin.
  await registerPlayer(context, targetUsername);
  markAdmin(targetUsername);

  await page.goto('/admin/settings');
  const admins = page.getByRole('region', { name: 'Admins' });
  await expect(admins.getByRole('cell', { name: targetUsername, exact: true })).toBeVisible();

  // The target's row Demote button posts to /role with role=host.
  const targetRow = admins.getByRole('row', { name: new RegExp(`^${targetUsername}\\b`) });
  page.once('dialog', (dialog) => dialog.accept());
  await targetRow.getByRole('button', { name: 'Demote' }).click();

  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
  await expect(page.getByText('Player role set to host.')).toBeVisible();
  await expect(page.getByLabel('Role')).toHaveValue('host');

  // Back on settings the demoted player no longer appears in the table.
  await page.goto('/admin/settings');
  await expect(admins.getByRole('cell', { name: targetUsername, exact: true })).toHaveCount(0);
});

test('host does not see Settings and gets 404 at /admin/settings', async ({ page, browserName }) => {
  const username = `e2e-host-plain-${browserName}`;

  await registerAdmin(page, username);
  markHost(username);
  await page.goto('/admin/quizzes');

  // No Settings nav link for a Host.
  const nav = page.getByRole('navigation', { name: 'Primary' });
  await expect(nav.getByRole('link', { name: 'Settings' })).toHaveCount(0);

  // The route stays hidden: a direct hit is a 404, not a 403.
  const response = await page.goto('/admin/settings');
  expect(response?.status()).toBe(404);
});

// A Host gets the dashboard and Quizzes but never the Admin-only Players
// or Email links (they 404 for a Host).
test('host does not see Players or Email nav links and 404s on them', async ({ page, browserName }) => {
  const username = `e2e-host-gating-${browserName}`;

  await registerAdmin(page, username);
  markHost(username);
  await page.goto('/admin/quizzes');

  const nav = page.getByRole('navigation', { name: 'Primary' });
  await expect(nav.getByRole('link', { name: 'Quizzes' })).toBeVisible();
  await expect(nav.getByRole('link', { name: 'Players' })).toHaveCount(0);
  await expect(nav.getByRole('link', { name: 'Email' })).toHaveCount(0);

  const playersResponse = await page.goto('/admin/players');
  expect(playersResponse?.status()).toBe(404);
  const emailResponse = await page.goto('/admin/email');
  expect(emailResponse?.status()).toBe(404);
});
