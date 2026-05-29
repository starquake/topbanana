import { defineConfig, devices } from '@playwright/test';
import { execFileSync } from 'child_process';
import { mkdtempSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';

// One server + one SQLite file per Playwright worker, so the 4 workers
// don't contend at the SQLite writer (#398). The worker count is the
// authoritative knob; the webServer array and the per-test baseURL
// fixture both derive from it.
const WORKER_COUNT = 4;

// Port assignment (#476). By default each worker server listens on an
// OS-assigned free port, so concurrent `make test-e2e` runs - different
// worktrees, a stale leftover server, CI matrix steps - never collide
// on a fixed range (the old 8181-8184 base needed `pkill` recovery).
//
// The ports are discovered ONCE in the main config load and shared with
// the re-loaded worker configs and the fixtures (tests/fixtures.ts) via
// TOPBANANA_E2E_PORTS - the same cross-process env-var handoff dataDir
// uses below. TOPBANANA_E2E_PORT (singular) stays as an explicit
// override for debugging a single worker on a known port: when set,
// worker i listens on TOPBANANA_E2E_PORT + i (the old fixed-base
// behaviour). It is no longer the default.
const WORKER_PORTS = resolveWorkerPorts(WORKER_COUNT);

function resolveWorkerPorts(count: number): number[] {
  const cached = process.env.TOPBANANA_E2E_PORTS;
  if (cached) {
    return cached.split(',').map(Number);
  }
  const override = process.env.TOPBANANA_E2E_PORT;
  const ports = override
    ? Array.from({ length: count }, (_, i) => Number(override) + i)
    : discoverFreePorts(count);
  process.env.TOPBANANA_E2E_PORTS = ports.join(',');
  return ports;
}

// Open `count` ephemeral listeners on :0 in a short-lived Node
// subprocess, read back the OS-assigned ports, and close them.
// Synchronous on purpose: the webServer array below needs the ports at
// config-load time, before Playwright forks workers or runs
// globalSetup, so an async helper or top-level await won't fit (and the
// config is CommonJS). Simultaneous listeners are guaranteed distinct
// ports; the close->rebind window is microscopic in practice - the
// documented trade-off for option 1 in #476.
function discoverFreePorts(count: number): number[] {
  const script =
    'const net=require("net");const n=+process.argv[1];const s=[];const p=[];let left=n;' +
    'for(let i=0;i<n;i++){const srv=net.createServer();s.push(srv);' +
    'srv.listen(0,"127.0.0.1",()=>{p[i]=srv.address().port;' +
    'if(--left===0){process.stdout.write(p.join(","));for(const x of s)x.close();}});}';
  const out = execFileSync(process.execPath, ['-e', script, String(count)], { encoding: 'utf8' });
  return out.trim().split(',').map(Number);
}
// Playwright re-loads this config in worker processes, so guard the temp dir
// behind the env var to avoid creating one per worker.
const dataDir = process.env.TOPBANANA_E2E_DATA_DIR ?? mkdtempSync(join(tmpdir(), 'topbanana-e2e-'));
process.env.TOPBANANA_E2E_DATA_DIR = dataDir;

// Same allowlist for every per-worker server: any worker may register
// any of the per-browser admin emails the specs use, regardless of
// which server it lands on. Bootstrap-admin (first registrant gets the
// admin role) only fires once per fresh DB, so the allowlist is what
// promotes subsequent registrants on the same DB. The suite's
// registerAdmin helper auto-builds "<username>@example.test" from the
// per-browser usernames, so the email list mirrors those usernames with
// the same suffix.
const adminUsernames = [
  'e2e-admin-chromium', 'e2e-admin-firefox',                          // auth.spec.ts
  'e2e-admin-create-chromium', 'e2e-admin-create-firefox',            // admin.spec.ts
  'e2e-admin-player-chromium', 'e2e-admin-player-firefox',            // player.spec.ts
  'e2e-admin-claim-chromium', 'e2e-admin-claim-firefox',              // claim.spec.ts test 3
  'e2e-admin-claim-skip-chromium', 'e2e-admin-claim-skip-firefox',    // claim.spec.ts test 4
  'e2e-admin-timeout-chromium', 'e2e-admin-timeout-firefox',          // timeout.spec.ts
  'e2e-admin-submit-err-chromium', 'e2e-admin-submit-err-firefox',    // submit-error.spec.ts
  'e2e-admin-spoiler-chromium', 'e2e-admin-spoiler-firefox',          // admin.spec.ts spoiler test
  'e2e-admin-share-start-chromium', 'e2e-admin-share-start-firefox',  // share.spec.ts start-screen
  'e2e-admin-share-finish-chromium', 'e2e-admin-share-finish-firefox',// share.spec.ts finish-screen
  'e2e-admin-share-home-chromium', 'e2e-admin-share-home-firefox',    // share.spec.ts home-page
  'e2e-admin-share-revisit-chromium', 'e2e-admin-share-revisit-firefox', // share.spec.ts revisit
  'e2e-admin-287-chromium', 'e2e-admin-287-firefox',                          // api-error-handling.spec.ts 400 branch
  'e2e-admin-287-conflict-chromium', 'e2e-admin-287-conflict-firefox',        // api-error-handling.spec.ts 409 branch
  'e2e-admin-quizzes-chromium', 'e2e-admin-quizzes-firefox',                  // player.spec.ts #284 public list test
  'e2e-admin-resume-chromium', 'e2e-admin-resume-firefox',                    // resume.spec.ts #310
  'e2e-admin-breaks-chromium', 'e2e-admin-breaks-firefox',                    // admin.spec.ts break CRUD (#167)
  'e2e-admin-break-play-chromium', 'e2e-admin-break-play-firefox',            // break.spec.ts break play loop (#167 slice 2)
  'e2e-admin-email-chromium', 'e2e-admin-email-firefox',                      // email-admin.spec.ts diagnostics page (#321)
  'e2e-admin-next-chromium', 'e2e-admin-next-firefox',                        // auth.spec.ts deep-link return (#449)
  'e2e-mgmt-admin-chromium', 'e2e-mgmt-admin-firefox',                        // admin-players.spec.ts player management (#450)
  'e2e-admin-nav-chromium', 'e2e-admin-nav-firefox',                          // admin-nav.spec.ts reachability (#517)
  'e2e-admin-nav-active-chromium', 'e2e-admin-nav-active-firefox',            // admin-nav.spec.ts active-section (#517)
  'e2e-admin-pregame-nav-chromium', 'e2e-admin-pregame-nav-firefox',          // pregame-nav.spec.ts deep-link browse link
  'e2e-super-boss-chromium', 'e2e-super-boss-firefox',                        // admin-settings.spec.ts super-admin promote (#320)
  'e2e-super-plain-chromium', 'e2e-super-plain-firefox',                      // admin-settings.spec.ts regular-admin 404 (#320)
  'e2e-demote-boss-chromium', 'e2e-demote-boss-firefox',                      // admin-settings.spec.ts super-admin demote (#527)
];
const ADMIN_EMAILS = adminUsernames.map(u => `${u}@example.test`).join(',');

const workerServer = (workerIndex: number) => {
  const port = WORKER_PORTS[workerIndex];
  const dbPath = join(dataDir, `e2e-${workerIndex}.db`);
  return {
    command: 'go run ./cmd/server',
    cwd: '../..',
    url: `http://127.0.0.1:${port}/healthz`,
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
      ADMIN_EMAILS,
    },
    reuseExistingServer: false,
    timeout: 120_000,
    stdout: 'pipe' as const,
    stderr: 'pipe' as const,
  };
};

export default defineConfig({
  testDir: './tests',
  // Every spec creates its own admin user + quiz titled per-browser, and the
  // anonymous-visitor specs in claim.spec.ts use isolated Playwright contexts
  // so their auto-minted petnames never collide. Each worker now has its own
  // SQLite file via per-worker servers (see workerServer above), so writes no
  // longer cross-contend.
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  // One retry in CI absorbs the post-registration flakes (e.g. the
  // URL race after question Save tracked in #384, or any slow-runner
  // browser nav). Registration steps are still single-shot: a retry
  // hits ErrUsernameTaken from the prior attempt and fails again,
  // but the affected specs are a small subset and the upside on the
  // larger pool of timing-sensitive UI assertions is worth the
  // trade. Local runs keep retries=0 so flakes surface loudly during
  // development (#350).
  retries: process.env.CI ? 1 : 0,
  workers: WORKER_COUNT,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    // Per-test baseURL is set by the fixture in tests/fixtures.ts, which
    // routes each worker to its own server. This fallback only matters
    // for tests that use the raw @playwright/test entrypoint.
    baseURL: `http://127.0.0.1:${WORKER_PORTS[0]}`,
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
  webServer: Array.from({ length: WORKER_COUNT }, (_, i) => workerServer(i)),
});
