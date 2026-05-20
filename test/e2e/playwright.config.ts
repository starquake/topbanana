import { defineConfig, devices } from '@playwright/test';
import { mkdtempSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';

// Fixed port keeps the Playwright config synchronous. Override with TOPBANANA_E2E_PORT
// when the default collides (e.g. running multiple suites in parallel).
const port = Number(process.env.TOPBANANA_E2E_PORT ?? 8181);
// Playwright re-loads this config in worker processes, so guard the temp dir
// behind the env var to avoid creating one per worker.
const dataDir = process.env.TOPBANANA_E2E_DATA_DIR ?? mkdtempSync(join(tmpdir(), 'topbanana-e2e-'));
process.env.TOPBANANA_E2E_DATA_DIR = dataDir;
const dbPath = join(dataDir, 'e2e.db');
const baseURL = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: './tests',
  // Every spec creates its own admin user + quiz titled per-browser, and the
  // anonymous-visitor specs in claim.spec.ts use isolated Playwright contexts
  // so their auto-minted petnames never collide. The SQLite store is shared
  // within a project but uses WAL + a 5s busy_timeout, which absorbs the
  // short writer contention this introduces.
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  // Tests register per-project usernames and rely on the registration succeeding
  // exactly once per run — a retry would just hit ErrUsernameTaken from the prior
  // attempt and fail again, so retries provide no value here.
  retries: 0,
  workers: 4,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL,
    trace: 'retain-on-failure',
    video: 'retain-on-failure',
  },
  globalTeardown: './global-teardown.ts',
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
    {
      name: 'firefox',
      use: { ...devices['Desktop Firefox'] },
    },
  ],
  webServer: {
    command: 'go run ./cmd/server',
    cwd: '../..',
    url: `${baseURL}/healthz`,
    env: {
      APP_ENV: 'development',
      HOST: '127.0.0.1',
      PORT: String(port),
      DB_URI: `file:${dbPath}?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)`,
      SESSION_KEY: 'e2e-test-session-key-do-not-use-in-prod-1234567890abcdef',
      REGISTRATION_ENABLED: 'true',
      // Shrink the per-question reveal beat (#247, default 3s) so the
      // suite isn't paying ~12s of dead time per gameplay spec. 500ms
      // still leaves the .progress-reveal phase observable for the
      // visibility assertion in player.spec.ts.
      REVEAL_DELAY: '500ms',
      // Whitelist every per-browser username the specs register so each project's
      // registrant is promoted to admin regardless of who got there first. The
      // bootstrap-admin rule (first password-bearing player becomes admin) only
      // applies to the very first registration, which would leave subsequent
      // browser projects stuck on the role of `player`.
      ADMIN_USERNAMES: [
        'e2e-admin-chromium', 'e2e-admin-firefox',                          // auth.spec.ts
        'e2e-admin-create-chromium', 'e2e-admin-create-firefox',            // admin.spec.ts
        'e2e-admin-player-chromium', 'e2e-admin-player-firefox',            // player.spec.ts
        'e2e-admin-claim-chromium', 'e2e-admin-claim-firefox',              // claim.spec.ts test 3
        'e2e-admin-claim-skip-chromium', 'e2e-admin-claim-skip-firefox',    // claim.spec.ts test 4
        'e2e-admin-timeout-chromium', 'e2e-admin-timeout-firefox',          // timeout.spec.ts
        'e2e-admin-submit-err-chromium', 'e2e-admin-submit-err-firefox',    // submit-error.spec.ts
      ].join(','),
    },
    reuseExistingServer: false,
    timeout: 60_000,
    stdout: 'pipe',
    stderr: 'pipe',
  },
});
