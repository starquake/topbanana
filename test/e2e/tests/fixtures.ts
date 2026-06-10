// Worker-aware base test: each Playwright worker hits its own server on
// its own port and its own SQLite file. Eliminates the SQLITE_BUSY
// contention that 4-way parallel writes against one DB used to surface
// (#398). Test files import { test, expect, ... } from this module
// instead of '@playwright/test' so the per-worker baseURL is wired
// automatically — no per-test setup required.
import { test as base, expect } from '@playwright/test';
import type { Browser, BrowserContext, Page } from '@playwright/test';
import { join } from 'node:path';

import { SEED_ADMIN_PASSWORD_HASH, adminStatePath } from '../e2e-auth';
import { endHostedSession } from './helpers';
import { execSqlite } from './sqlite';

// HostSessions is the test-scoped factory the host-session specs use to open
// live rooms. It owns the admin host context (opened once, lazily, and reused -
// a host runs one room at a time) plus every extra player context it spins up,
// and in teardown ends every room it opened and closes every context it owns.
// Specs no longer roll their own try/finally cleanup: opening a room through
// the factory registers it for an end-on-teardown that runs even when the test
// times out or throws.
export type HostSessions = {
  // openViaApi creates a session straight over the REST API for the given quiz
  // id (POST /api/sessions) and returns the admin host page plus the new join
  // code. The room is registered for teardown.
  openViaApi(quizId: number): Promise<{ host: Page; joinCode: string }>;
  // openEmptyRoom drives the dashboard "Host a session" control to open an empty
  // staging room (no quiz armed) and returns the host page plus the join code
  // read off the lobby URL. The room is registered for teardown. A stray room
  // from a crashed prior test is defensively ended first so the dashboard offers
  // the submit rather than a resume link.
  openEmptyRoom(): Promise<{ host: Page; joinCode: string }>;
  // hostLive opens the named quiz's admin view and clicks "Host live", landing
  // the host on a fresh big screen, and returns the host page plus the join code
  // read off the /host/{code} URL. The room is registered for teardown.
  hostLive(quizTitle: string): Promise<{ host: Page; joinCode: string }>;
  // adminHost returns the lazily-opened, reused admin host page so a spec can
  // drive the host browser directly (seed a quiz, resolve an id) before opening
  // a room.
  adminHost(): Promise<Page>;
  // track registers a join code created indirectly (e.g. a confirm-restart
  // restart that swaps the host onto a new room) so teardown ends it too.
  track(host: Page, joinCode: string): void;
  // newPlayerContext opens a tracked anonymous browser context (its own session
  // cookie -> a distinct anonymous player), auto-closed in teardown.
  newPlayerContext(): Promise<BrowserContext>;
};

async function makeHostSessions(
  browser: Browser,
  baseURL: string | undefined,
): Promise<{ factory: HostSessions; teardown: () => Promise<void> }> {
  const contexts: BrowserContext[] = [];
  const sessions: { host: Page; joinCode: string }[] = [];
  let adminPage: Page | null = null;

  async function adminHost(): Promise<Page> {
    if (adminPage) return adminPage;
    const context = await browser.newContext({ storageState: adminStatePath(), baseURL });
    contexts.push(context);
    adminPage = await context.newPage();
    return adminPage;
  }

  const factory: HostSessions = {
    adminHost,

    async openViaApi(quizId) {
      const host = await adminHost();
      const createResp = await host.request.post('/api/sessions', { data: { quizId } });
      const status = createResp.status();
      if (status !== 201) {
        throw new Error(`create session: ${status} ${await createResp.text()}`);
      }
      const { joinCode } = (await createResp.json()) as { joinCode: string };
      sessions.push({ host, joinCode });
      return { host, joinCode };
    },

    async openEmptyRoom() {
      const host = await adminHost();
      await host.goto('/admin');
      // Defensive single end for a stray room a crashed prior test left open
      // (its cleanup never ran): the dashboard then shows "Resume session"
      // instead of the submit. End that one room, then reload so the submit is
      // offered. Not a drain loop - one stray room is all an interrupted test
      // can leave.
      const resume = host.getByTestId('resume-hosting');
      if ((await resume.count()) > 0) {
        const strayCode = (await resume.getAttribute('href'))?.split('/host/')[1] ?? '';
        if (strayCode) await endHostedSession(host, strayCode);
        await host.goto('/admin');
      }
      const submit = host.getByTestId('host-session-submit');
      await expect(submit).toBeVisible();
      await submit.click();
      await host.waitForURL(/\/host\/[A-Z0-9]{6}$/);
      const joinCode = host.url().split('/host/')[1];
      sessions.push({ host, joinCode });
      return { host, joinCode };
    },

    async hostLive(quizTitle) {
      const host = await adminHost();
      await host.goto('/admin/quizzes');
      await host.getByRole('link', { name: quizTitle }).click();
      await host.waitForURL(/\/admin\/quizzes\/\d+$/);
      await host.getByRole('button', { name: 'Host live' }).click();
      await host.waitForURL(/\/host\/[A-Z0-9]{6}$/);
      const joinCode = host.url().split('/host/')[1];
      sessions.push({ host, joinCode });
      return { host, joinCode };
    },

    track(host, joinCode) {
      sessions.push({ host, joinCode });
    },

    async newPlayerContext() {
      const context = await browser.newContext({ storageState: undefined, baseURL });
      contexts.push(context);
      return context;
    },
  };

  async function teardown(): Promise<void> {
    for (const { host, joinCode } of sessions) {
      try {
        await endHostedSession(host, joinCode);
      } catch {
        // endHostedSession is best-effort cleanup; one failed room must not
        // skip ending the rest or closing the contexts.
      }
    }
    for (const context of contexts) {
      try {
        await context.close();
      } catch {
        // Already-closed contexts are fine to ignore on teardown.
      }
    }
  }

  return { factory, teardown };
}

// playwright.config.ts discovers a free port per worker and publishes
// the list as TOPBANANA_E2E_PORTS (comma-separated, indexed by worker).
// It is always set by the time a worker imports this fixture, since the
// worker re-loads the config first. Read it at use-time and index by
// the worker's parallelIndex so each worker hits its own server (#398,
// #476).
export const test = base.extend<{ hostSessions: HostSessions }, { seedAdminTopTier: void }>({
  baseURL: async ({}, use, testInfo) => {
    const ports = (process.env.TOPBANANA_E2E_PORTS ?? '').split(',').map(Number);
    const port = ports[testInfo.parallelIndex];
    await use(`http://127.0.0.1:${port}`);
  },

  // Test-scoped host-session factory: owns the admin host context(s) and any
  // anonymous player contexts a host-session spec opens, and tears them all
  // down (ending every room it opened) after the test, even on timeout/failure.
  hostSessions: async ({ browser, baseURL }, use) => {
    const { factory, teardown } = await makeHostSessions(browser, baseURL);
    await use(factory);
    await teardown();
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
