import { test as setup, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';

import { adminStatePath, SEED_ADMIN_EMAIL, SEED_ADMIN_PASSWORD, SEED_ADMIN_PASSWORD_HASH } from '../e2e-auth';

// Logs the migration-seeded admin (players.id = 1) in through the real
// /login form and saves the resulting context as the shared storageState the
// admin specs reuse. Running the genuine login is the point: the suite
// exercises real Go session issuance and verification and never reimplements
// the signing.
//
// The seed admin ships with no password and on the 'host' tier (roles
// migration #538), so first stamp a real bcrypt hash and promote it to admin.
// This setup project runs against worker 0 (the config's default baseURL), so
// it prepares worker 0's DB; the worker fixture does the same on every other
// worker, so the issued cookie authenticates an admin on all of them.
setup('authenticate the shared admin', async ({ page }) => {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; cannot prepare the seed admin');
  }
  // SEED_ADMIN_PASSWORD_HASH is a fixed bcrypt constant (chars [./A-Za-z0-9$],
  // no quotes), so it is safe to interpolate directly - unlike the
  // user-derived values in markEmailVerified/setRole, it needs no escaping.
  execFileSync('sqlite3', [
    join(dataDir, 'e2e-0.db'),
    `UPDATE players SET role = 'admin', password_hash = '${SEED_ADMIN_PASSWORD_HASH}' WHERE id = 1;`,
  ]);

  await page.goto('/login');
  await page.locator('input[name=email]').fill(SEED_ADMIN_EMAIL);
  await page.locator('input[name=password]').fill(SEED_ADMIN_PASSWORD);
  await page.locator('button[type=submit]').click();
  // An admin-role login lands on /admin/quizzes (landingPathFor); reaching it
  // confirms the credentials worked and the seed admin is on the admin tier.
  await expect(page).toHaveURL(/\/admin\/quizzes$/);

  await page.context().storageState({ path: adminStatePath() });
});
