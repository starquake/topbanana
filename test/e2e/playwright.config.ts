import { defineConfig, devices } from '@playwright/test';
import { mkdtempSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';

// Fixed port keeps the Playwright config synchronous. Override with TOPBANANA_E2E_PORT
// when the default collides (e.g. running multiple suites in parallel).
const port = Number(process.env.TOPBANANA_E2E_PORT ?? 8181);
const dataDir = mkdtempSync(join(tmpdir(), 'topbanana-e2e-'));
const dbPath = join(dataDir, 'e2e.db');
const baseURL = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  // Tests register per-project usernames and rely on the registration succeeding
  // exactly once per run — a retry would just hit ErrUsernameTaken from the prior
  // attempt and fail again, so retries provide no value here.
  retries: 0,
  workers: 1,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL,
    trace: 'on-first-retry',
    video: 'retain-on-failure',
  },
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
      // Whitelist the per-browser usernames the auth spec creates so each project's
      // registrant is promoted to admin regardless of who got there first. The
      // bootstrap-admin rule (first password-bearing player becomes admin) only
      // applies to the very first registration, which would leave subsequent
      // browser projects stuck on the role of `player`.
      ADMIN_USERNAMES: 'e2e-admin-chromium,e2e-admin-firefox,e2e-admin-webkit',
    },
    reuseExistingServer: false,
    timeout: 60_000,
    stdout: 'pipe',
    stderr: 'pipe',
  },
});
