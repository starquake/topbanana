import { join } from 'node:path';

// SESSION_KEY is the signing key every per-worker server runs with (see
// playwright.config.ts). Exported here as the single source of truth so the
// config and any future auth tooling agree on it.
export const SESSION_KEY = 'e2e-test-session-key-do-not-use-in-prod-1234567890abcdef';

// The shared admin the suite acts as is the migration-seeded row
// (players.id = 1, email below, verified by 20260527140000). auth.setup.ts
// logs it in through the real /login form - no cookie is minted or forged,
// so the suite never reimplements the Go session/signing logic. The seed
// admin starts with no password and on the 'host' tier (roles migration
// #538), so the setup + the worker fixture stamp a real bcrypt hash and
// promote it to admin first.
export const SEED_ADMIN_EMAIL = 'email@example.com';
export const SEED_ADMIN_PASSWORD = 'correctbatterystaple';

// SEED_ADMIN_PASSWORD_HASH is a real bcrypt hash of SEED_ADMIN_PASSWORD,
// generated once with auth.HashPassword. bcrypt embeds its salt + cost, so
// this fixed value verifies forever and never needs regenerating. It is the
// only password the seed admin ever has, and it exists solely so the real
// login flow can issue a genuine session cookie for the storageState.
export const SEED_ADMIN_PASSWORD_HASH = '$2a$10$wMDwFkHyaFYIvm1tmn43wO.PRpvxWtdzNPacr.vWyp9l4pCPQE/9.';

// adminStatePath is the Playwright storageState file auth.setup.ts saves the
// logged-in admin context into. It lives in the per-run data dir that
// playwright.config.ts creates and global-teardown removes.
export function adminStatePath(): string {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; cannot resolve the admin storageState path');
  }
  return join(dataDir, 'admin-state.json');
}
