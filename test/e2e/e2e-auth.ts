import { join } from 'node:path';

// Shared session-cookie facts for the e2e suite. The signing key is the
// same literal every per-worker server runs with (see playwright.config.ts),
// so a single cookie minted by global-setup authenticates the
// migration-seeded admin (players.id = 1) against every worker. The cookie
// name and these values must stay in lockstep with internal/session.
export const SESSION_KEY = 'e2e-test-session-key-do-not-use-in-prod-1234567890abcdef';
export const SESSION_COOKIE = 'topbanana_session';

// SEED_ADMIN_ID is players.id of the migration-seeded admin (inserted by
// 20260111110308_add_admin_player.sql); session_version defaults to 0.
export const SEED_ADMIN_ID = 1;

// adminStatePath is the Playwright storageState file global-setup writes the
// seed-admin cookie into. It lives in the per-run data dir that
// playwright.config.ts creates and global-teardown removes.
export function adminStatePath(): string {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; cannot resolve the admin storageState path');
  }
  return join(dataDir, 'admin-state.json');
}
