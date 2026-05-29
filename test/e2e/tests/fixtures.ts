// Worker-aware base test: each Playwright worker hits its own server on
// its own port and its own SQLite file. Eliminates the SQLITE_BUSY
// contention that 4-way parallel writes against one DB used to surface
// (#398). Test files import { test, expect, ... } from this module
// instead of '@playwright/test' so the per-worker baseURL is wired
// automatically — no per-test setup required.
import { test as base } from '@playwright/test';

// playwright.config.ts discovers a free port per worker and publishes
// the list as TOPBANANA_E2E_PORTS (comma-separated, indexed by worker).
// It is always set by the time a worker imports this fixture, since the
// worker re-loads the config first. Read it at use-time and index by
// the worker's parallelIndex so each worker hits its own server (#398,
// #476).
export const test = base.extend({
  baseURL: async ({}, use, testInfo) => {
    const ports = (process.env.TOPBANANA_E2E_PORTS ?? '').split(',').map(Number);
    const port = ports[testInfo.parallelIndex];
    await use(`http://127.0.0.1:${port}`);
  },
});

export { expect } from '@playwright/test';
export type { Route, Request, Page, Locator } from '@playwright/test';
