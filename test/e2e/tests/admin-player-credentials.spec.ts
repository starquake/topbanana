import type { BrowserContext } from '@playwright/test';

import { test, expect } from './fixtures';
import { registerAdmin, markSuperAdmin, PASSWORD } from './helpers';

// Registers a fresh plain player in an isolated browser context so the
// admin session on the main page is left untouched.
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

// #535 — a super admin can rename a player and set a new password from
// the player detail page. Both forms live inside the super-admin gate in
// the Actions card.
test('super admin sets a player display name and password from the detail page', async ({ page, context, browserName }) => {
  const adminUsername = `e2e-cred-boss-${browserName}`;
  const targetUsername = `e2e-cred-target-${browserName}`;
  const renamedUsername = `e2e-cred-renamed-${browserName}`;
  const newPassword = 'freshpassword13plus';

  await registerAdmin(page, adminUsername);
  markSuperAdmin(adminUsername);
  await registerPlayer(context, targetUsername);

  // Open the target's detail view from the players list.
  await page.goto('/admin/players');
  await page.getByRole('link', { name: targetUsername }).click();
  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
  await expect(page.getByRole('heading', { name: targetUsername })).toBeVisible();

  // Rename via the new display-name form; PRG returns with a flash and
  // the persisted name shows in the page heading.
  const nameInput = page.getByLabel('Set display name');
  await expect(nameInput).toHaveValue(targetUsername);
  await nameInput.fill(renamedUsername);
  await page.getByRole('button', { name: 'Save display name' }).click();
  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
  await expect(page.getByText('Display name updated.')).toBeVisible();
  await expect(page.getByRole('heading', { name: renamedUsername })).toBeVisible();

  // Set a new password; the confirm dialog warns about signing out other
  // sessions. The success flash confirms the reset ran.
  await page.getByLabel('Set password').fill(newPassword);
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Save password' }).click();
  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
  await expect(
    page.getByText("Password set. The player's other sessions were signed out; hand the new password over out-of-band."),
  ).toBeVisible();
});
