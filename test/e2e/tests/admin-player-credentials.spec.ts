import type { BrowserContext } from '@playwright/test';

import { test, expect } from './fixtures';
import { PASSWORD } from './helpers';
import { adminStatePath } from '../e2e-auth';

// Act as the shared seed admin; this spec mutates only the separately
// registered target player, never the seed admin.
test.use({ storageState: adminStatePath() });

// Registers a fresh plain player in an isolated, anonymous browser context
// so the admin session on the main page is left untouched. storageState is
// forced empty so the seed-admin cookie this spec runs with does not leak
// into the new context; baseURL pins it to the same worker server.
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

// #535 — an Admin can rename a player and set a new password from the
// player detail page. Both forms live inside the Admin gate in the
// Actions card.
test('admin sets a player display name and password from the detail page', async ({ page, context, browserName, baseURL }) => {
  const targetDisplayName = `e2e-cred-target-${browserName}`;
  const renamedDisplayName = `e2e-cred-renamed-${browserName}`;
  const newPassword = 'freshpassword13plus';

  await registerPlayer(context, targetDisplayName, baseURL!);

  // Open the target's detail view from the players list.
  await page.goto('/admin/players');
  await page.getByRole('link', { name: targetDisplayName }).click();
  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
  await expect(page.getByRole('heading', { name: targetDisplayName })).toBeVisible();

  // Rename via the new display-name form; PRG returns with a flash and
  // the persisted name shows in the page heading.
  const nameInput = page.getByLabel('Name', { exact: true });
  await expect(nameInput).toHaveValue(targetDisplayName);
  await nameInput.fill(renamedDisplayName);
  await page.getByRole('button', { name: 'Save display name' }).click();
  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
  await expect(page.getByText('Display name updated.')).toBeVisible();
  await expect(page.getByRole('heading', { name: renamedDisplayName })).toBeVisible();

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
