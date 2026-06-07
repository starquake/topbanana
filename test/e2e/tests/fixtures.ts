// Worker-aware base test: each Playwright worker hits its own server on
// its own port and its own SQLite file. Eliminates the SQLITE_BUSY
// contention that 4-way parallel writes against one DB used to surface
// (#398). Test files import { test, expect, ... } from this module
// instead of '@playwright/test' so the per-worker baseURL is wired
// automatically — no per-test setup required.
import { test as base } from '@playwright/test';
import { join } from 'node:path';

import { SEED_ADMIN_PASSWORD_HASH } from '../e2e-auth';
import { execSqlite } from './sqlite';

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

  // Make the migration-seeded admin (players.id = 1) the shared admin the
  // storageState specs act as. Two stamps:
  //   - role 'admin': the roles migration (#538) lands it on 'host', but the
  //     admin specs reach Admin-only sections.
  //   - a non-null password_hash: marks it the credentialled player so target
  //     registrations stay plain 'player' (the first-credentialled-becomes-
  //     admin rule). Never used to log in; auth is cookie-based.
  // Worker-scoped + auto: runs once per worker after its migrated DB is up.
  seedAdminTopTier: [async ({}, use, workerInfo) => {
    const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
    if (!dataDir) {
      throw new Error('TOPBANANA_E2E_DATA_DIR is not set; cannot prepare the seed admin');
    }
    const dbFile = join(dataDir, `e2e-${workerInfo.parallelIndex}.db`);
    // Fixed bcrypt constant (no quotes), so safe to interpolate without escaping.
    execSqlite(
      dbFile,
      `UPDATE players SET role = 'admin', password_hash = '${SEED_ADMIN_PASSWORD_HASH}' WHERE id = 1;`,
    );
    await use();
  }, { scope: 'worker', auto: true }],
});

export { expect } from '@playwright/test';
export type { Route, Request, Page, Locator } from '@playwright/test';
