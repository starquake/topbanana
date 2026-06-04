import { test, expect } from './fixtures';
import { adminStatePath } from '../e2e-auth';

// Reuse the shared seed-admin session; this spec only needs to be signed
// in as an admin to reach an admin page.
test.use({ storageState: adminStatePath() });

// #663 — every admin page carries a discreet build-stamp footer so an
// operator can tell which release + commit is live. The e2e server runs
// as APP_ENV=development, so the label is "development (<commit>)".
test('admin pages show the version footer', async ({ page }) => {
  await page.goto('/admin/quizzes');

  const footer = page.locator('body > footer');
  await expect(footer).toBeVisible();
  await expect(footer).toContainText(/development \(.+\)/);
});
