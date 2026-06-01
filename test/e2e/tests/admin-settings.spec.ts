import type { BrowserContext } from '@playwright/test';

import { test, expect } from './fixtures';
import { registerAdmin, markAdmin, markHost, PASSWORD } from './helpers';
import { adminStatePath } from '../e2e-auth';

// Registers a fresh plain player in an isolated, anonymous browser context
// so the admin session on the main page is left untouched. Returns nothing;
// the row simply needs to exist for the admin to act on. storageState is
// forced empty so the seed-admin cookie this spec runs with does not leak
// into the new context (a signed-in visit to /register just redirects
// away); baseURL pins the context to the same worker server as the admin.
async function registerPlayer(
  context: BrowserContext,
  displayName: string,
  baseURL: string,
): Promise<void> {
  const playerContext = await context.browser()!.newContext({ storageState: undefined, baseURL });
  try {
    const playerPage = await playerContext.newPage();
    await playerPage.goto('/register');
    await playerPage.locator('input[name=email]').fill(`${displayName}@example.test`);
    await playerPage.locator('input[name=display_name]').fill(displayName);
    await playerPage.locator('input[name=password]').fill(PASSWORD);
    await playerPage.locator('input[name=password_confirm]').fill(PASSWORD);
    await playerPage.locator('button[type=submit]').click();
    // Hard gate (#574): register creates the row then renders the
    // confirmation page at /register with no session. The row exists
    // (unverified), which is all the admin action needs.
    await expect(playerPage).toHaveURL(/\/register$/);
    await playerPage.close();
  } finally {
    await playerContext.close();
  }
}

// These three tests act as the shared seed admin and mutate only a
// separately registered target player, so they never touch the seed
// admin's own role. The host-gating tests below demote the acting
// admin to host, so they stay on a freshly registered admin and are
// kept outside this storageState group.
test.describe('admin acting on a target player', () => {
  test.use({ storageState: adminStatePath() });

  // #527/#538 — role management lives on the player detail page. An Admin
  // opens a player's detail view, sets their role with the selector, and the
  // change sticks. The Settings page lists Admins and demotes them back to
  // Host via the repointed /role endpoint; it no longer carries a displayName
  // promote form.
  test('admin promotes a player to admin from the detail page', async ({ page, context, browserName, baseURL }) => {
    const targetDisplayName = `e2e-admin-target-${browserName}`;

    await registerPlayer(context, targetDisplayName, baseURL!);

    // Open the target's detail view from the players list.
    await page.goto('/admin/players');
    await page.getByRole('link', { name: targetDisplayName }).click();
    await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
    await expect(page.getByRole('heading', { name: targetDisplayName })).toBeVisible();

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
    await expect(admins.getByRole('cell', { name: targetDisplayName, exact: true })).toBeVisible();

    // The displayName promote form is gone.
    await expect(page.getByRole('textbox', { name: 'Promote a player' })).toHaveCount(0);
  });

  // The role selector can set every tier, ending on Host.
  test('admin sets a player to host from the detail page', async ({ page, context, browserName, baseURL }) => {
    const targetDisplayName = `e2e-host-target-${browserName}`;

    await registerPlayer(context, targetDisplayName, baseURL!);

    await page.goto('/admin/players');
    await page.getByRole('link', { name: targetDisplayName }).click();
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

  test('admin demotes an admin from the settings table', async ({ page, context, browserName, baseURL }) => {
    const targetDisplayName = `e2e-demote-target-${browserName}`;

    // Bootstrap the target straight to Admin so the table has a row to
    // demote that is not the last Admin.
    await registerPlayer(context, targetDisplayName, baseURL!);
    markAdmin(targetDisplayName);

    await page.goto('/admin/settings');
    const admins = page.getByRole('region', { name: 'Admins' });
    await expect(admins.getByRole('cell', { name: targetDisplayName, exact: true })).toBeVisible();

    // The target's row Demote button posts to /role with role=host.
    const targetRow = admins.getByRole('row', { name: new RegExp(`^${targetDisplayName}\\b`) });
    page.once('dialog', (dialog) => dialog.accept());
    await targetRow.getByRole('button', { name: 'Demote' }).click();

    await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
    await expect(page.getByText('Player role set to host.')).toBeVisible();
    await expect(page.getByLabel('Role')).toHaveValue('host');

    // Back on settings the demoted player no longer appears in the table.
    await page.goto('/admin/settings');
    await expect(admins.getByRole('cell', { name: targetDisplayName, exact: true })).toHaveCount(0);
  });
});

test('host does not see Settings and gets 404 at /admin/settings', async ({ page, browserName }) => {
  const displayName = `e2e-host-plain-${browserName}`;

  await registerAdmin(page, displayName);
  markHost(displayName);
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
  const displayName = `e2e-host-gating-${browserName}`;

  await registerAdmin(page, displayName);
  markHost(displayName);
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
