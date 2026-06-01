import { join } from 'node:path';

// Signing key every per-worker server runs with (see playwright.config.ts);
// kept here as the single source of truth.
export const SESSION_KEY = 'e2e-test-session-key-do-not-use-in-prod-1234567890abcdef';

// The shared admin is the migration-seeded row (players.id = 1, verified by
// migration 20260527140000). auth.setup.ts logs it in through the real /login
// form (no cookie minting). It ships passwordless on 'host' (roles migration
// #538), so the setup + worker fixture stamp a hash and promote it to admin.
export const SEED_ADMIN_EMAIL = 'email@example.com';
export const SEED_ADMIN_PASSWORD = 'correctbatterystaple';

// Real bcrypt hash of SEED_ADMIN_PASSWORD, generated once with
// auth.HashPassword; bcrypt embeds its salt + cost so it verifies forever. The
// seed admin's only password, so the real login can issue a genuine cookie.
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
