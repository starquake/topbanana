// Worker-aware base test: each Playwright worker hits its own server on
// its own port and its own SQLite file. Eliminates the SQLITE_BUSY
// contention that 4-way parallel writes against one DB used to surface
// (#398). Test files import { test, expect, ... } from this module
// instead of '@playwright/test' so the per-worker baseURL is wired
// automatically — no per-test setup required.
import { test as base } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';

import { SEED_ADMIN_PASSWORD_HASH } from '../e2e-auth';

// playwright.config.ts discovers a free port per worker and publishes
// the list as TOPBANANA_E2E_PORTS (comma-separated, indexed by worker).
// It is always set by the time a worker imports this fixture, since the
// worker re-loads the config first. Read it at use-time and index by
// the worker's parallelIndex so each worker hits its own server (#398,
// #476).
export const test = base.extend<{}, { seedAdminTopTier: void }>({
  baseURL: async ({}, use, testInfo) => {
    const ports = (process.env.TOPBANANA_E2E_PORTS ?? '').split(',').map(Number);
    const port = ports[testInfo.parallelIndex];
    await use(`http://127.0.0.1:${port}`);
  },

  // Prepare the migration-seeded admin (players.id = 1) on this worker's
  // DB to be the shared top-tier admin the storageState specs act as.
  // Two stamps, both needed:
  //   - role = 'admin': the roles migration (#538, 20260529160000)
  //     intentionally lands the seed admin on 'host' (the middle tier),
  //     but the admin specs reach Admin-only sections (Players, Email,
  //     Settings).
  //   - a non-null password_hash: the "first credentialled registrant
  //     becomes admin" rule (queries/players.sql) ignores the seed admin
  //     because it has no password_hash, so without this the first player
  //     a spec registers as a target would be auto-promoted to admin.
  //     Stamping any hash makes the seed admin count as the credentialled
  //     player so targets register as plain 'player'. The value is never
  //     used to log in (auth is cookie-based via storageState).
  // Worker-scoped + auto so it runs once per worker after the server (and
  // its migrated DB) is up, before any test, regardless of which specs the
  // worker picks up. Idempotent.
  seedAdminTopTier: [async ({}, use, workerInfo) => {
    const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
    if (!dataDir) {
      throw new Error('TOPBANANA_E2E_DATA_DIR is not set; cannot prepare the seed admin');
    }
    const dbFile = join(dataDir, `e2e-${workerInfo.parallelIndex}.db`);
    execFileSync(
      'sqlite3',
      [dbFile, `UPDATE players SET role = 'admin', password_hash = '${SEED_ADMIN_PASSWORD_HASH}' WHERE id = 1;`],
      { encoding: 'utf8' },
    );
    await use();
  }, { scope: 'worker', auto: true }],
});

export { expect } from '@playwright/test';
export type { Route, Request, Page, Locator } from '@playwright/test';
