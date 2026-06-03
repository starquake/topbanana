import type { BrowserContext } from '@playwright/test';

import { test, expect } from './fixtures';
import { PASSWORD, markEmailVerified } from './helpers';
import { adminStatePath } from '../e2e-auth';
import { waitForEmail } from './mailpit';

// #578 role-change notification round-trip. The integration handler tests
// (internal/admin/playeractions_role_test.go) pin the opt-in/opt-out,
// unverified, and SMTP-unconfigured branches deterministically with a
// mailer spy. This spec's unique job is the real admin-UI -> server ->
// SMTP -> mailpit inbox roundtrip: that the opt-in actually puts the
// notice into a player's inbox. A per-browser recipient keeps the shared
// mailpit inbox unambiguous under parallel workers.
test.use({ storageState: adminStatePath() });

// Registers a fresh plain player in an isolated, anonymous browser context
// so the admin session on the main page is left untouched, then stamps the
// email verified (the role notice only sends to a verified address). The
// row defaults to role 'player', so promoting it to host is a real change.
async function registerVerifiedPlayer(
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
    await expect(playerPage).toHaveURL(/\/register$/);
    await playerPage.close();
  } finally {
    await playerContext.close();
  }
  // Stamp the address verified so the notice path actually dispatches.
  markEmailVerified(displayName);
}

test('role change with notify opt-in delivers the notice email to the player', async ({ page, context, browserName, baseURL }) => {
  const targetDisplayName = `e2e-role-notify-${browserName}`;
  const email = `${targetDisplayName}@example.test`;

  await registerVerifiedPlayer(context, targetDisplayName, baseURL!);

  // Open the target's detail view from the players list.
  await page.goto('/admin/players');
  await page.getByRole('link', { name: targetDisplayName }).click();
  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
  await expect(page.getByRole('heading', { name: targetDisplayName })).toBeVisible();

  // Promote player -> host, opting in to the notification.
  await page.getByLabel('Role').selectOption('host');
  await page.getByRole('checkbox', { name: 'Email the player about this change' }).check();

  // The role form's onsubmit handler asks for confirmation before posting.
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Save role' }).click();

  // The PRG lands back on the detail page; the flash confirms a notice was sent.
  await expect(page).toHaveURL(/\/admin\/players\/\d+$/);
  await expect(page.getByText('A notification email was sent to the player.')).toBeVisible();

  // Read the notice mailpit caught for the player and assert its subject and
  // that the body names the new role.
  const mail = await waitForEmail(email);
  expect(mail.subject).toBe('Your Top Banana! account role changed');
  expect(mail.text).toContain('An administrator changed the role on your Top Banana! account to host.');
  expect(mail.text).toContain('contact the site administrator');
});
