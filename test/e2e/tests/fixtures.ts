// Worker-aware base test: each Playwright worker hits its own server on
// its own port and its own SQLite file. Eliminates the SQLITE_BUSY
// contention that 4-way parallel writes against one DB used to surface
// (#398). Test files import { test, expect, ... } from this module
// instead of '@playwright/test' so the per-worker baseURL is wired
// automatically — no per-test setup required.
import { test as base } from '@playwright/test';

const BASE_PORT = Number(process.env.TOPBANANA_E2E_PORT ?? 8181);

export const test = base.extend({
  baseURL: async ({}, use, testInfo) => {
    const port = BASE_PORT + testInfo.parallelIndex;
    await use(`http://127.0.0.1:${port}`);
  },
});

export { expect } from '@playwright/test';
export type { Route, Request, Page, Locator } from '@playwright/test';
