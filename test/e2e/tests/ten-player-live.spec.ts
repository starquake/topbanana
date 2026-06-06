import { execFileSync, spawn, type ChildProcess } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import type { BrowserContext, Page } from '@playwright/test';

import { SESSION_KEY, SEED_ADMIN_EMAIL, SEED_ADMIN_PASSWORD, SEED_ADMIN_PASSWORD_HASH } from '../e2e-auth';
import { test, expect } from './fixtures';
import { importQuiz } from './helpers';

// On-demand video walkthrough: ten players play a full live multi-round game so
// the many-player path can be eyeballed (lobby roster, round intro, synchronized
// question / read beat / reveal, the between-rounds standings bar graph that
// animates and REORDERS as scores change, and the final standings with ten
// rows). Each player runs in its own context (its own cookie jar -> a distinct
// anonymous player) and records its own video; the host TV is recorded too.
//
// Unlike the rest of the suite, this spec spins up its OWN server with
// production-like runner beats (SESSION_RUNNER_BEAT=3s). The shared e2e servers
// compress every beat to 500ms so the suite runs fast, but that is too short to
// see the between-rounds standings animation: the round_results phase advances
// before the bars finish growing/reordering. The longer beats let the animation
// play and settle on screen (and in the recording).
//
// Kept OUT of the default CI gate by choice: it is a heavy, always-recording
// walkthrough whose real output is the videos, and the underlying behaviour is
// already covered deterministically by play-live / host-game / standings-bargraph
// with fewer players and the fast beats. Run it on demand:
//
//   cd test/e2e && RUN_MANUAL_E2E=1 npx playwright test ten-player-live --project=chromium
//
// Videos land in test/e2e/videos/ten-player-live/ (gitignored):
// player-01.webm .. player-10.webm and host-tv.webm. They go under videos/
// rather than test-results/ on purpose: Playwright wipes test-results/ at the
// start of every run, so a later `make test-e2e` would delete them.

const PLAYER_COUNT = 10;

type Player = { seat: number; name: string; context: BrowserContext; page: Page };

type RoundSpec = {
  title: string;
  questions: { text: string; options: string[]; correctIndex: number }[];
};

// A three-round quiz of single-correct questions. Scoring is steered per-seat
// (see CORRECT_SEATS) so the standings reorder between rounds rather than just
// growing in place.
const ROUNDS: RoundSpec[] = [
  {
    title: 'Warm-up',
    questions: [
      { text: 'What is 2 + 2?', options: ['3', '4', '5', '6'], correctIndex: 1 },
      { text: 'Capital of France?', options: ['Paris', 'Rome', 'Berlin', 'Madrid'], correctIndex: 0 },
    ],
  },
  {
    title: 'Catch-up',
    questions: [
      { text: 'What is 10 - 3?', options: ['6', '7', '8', '9'], correctIndex: 1 },
      { text: 'Capital of Japan?', options: ['Seoul', 'Beijing', 'Tokyo', 'Bangkok'], correctIndex: 2 },
    ],
  },
  {
    title: 'Final',
    questions: [
      { text: 'What is 6 / 2?', options: ['2', '3', '4', '5'], correctIndex: 1 },
      { text: 'Largest planet?', options: ['Mars', 'Jupiter', 'Saturn', 'Earth'], correctIndex: 1 },
    ],
  },
];

const QUESTIONS = ROUNDS.flatMap((r) => r.questions);

// Which seats answer each question (in play order) correctly; the rest pick a
// wrong option. The pattern flips the standings each round: round 1 favours the
// low seats, round 2 lifts the high seats from last to the top, round 3 surges
// seats 0/3/4 to the front. The exact totals depend on the speed bonus, but the
// correct-count gradient (more correct = higher) drives a visible reorder each
// round - which is the whole point of the bar-graph animation.
const CORRECT_SEATS: number[][] = [
  [0, 1, 2, 3, 4], // R1 Q1
  [0, 1, 2], //       R1 Q2
  [5, 6, 7, 8, 9], // R2 Q1
  [5, 6, 7, 8, 9], // R2 Q2
  [0, 3, 4], //       R3 Q1
  [3, 4], //          R3 Q2
];

// freePort opens an ephemeral listener on :0, reads the OS-assigned port, and
// closes it - the same trick playwright.config.ts uses to avoid fixed-port
// collisions across concurrent runs.
function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = createServer();
    srv.on('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      srv.close(() => resolve(port));
    });
  });
}

// startServer launches a dedicated `go run ./cmd/server` with production-like
// runner beats on its own port + temp SQLite DB, and waits for /healthz. It is
// detached so the whole process group (go run + the compiled server child) can
// be torn down together. Returns the live base URL, the db file (for the
// post-boot admin stamp + quiz queries), and a stop() that kills the group and
// removes the temp dir.
async function startServer(): Promise<{ baseURL: string; dbFile: string; stop: () => void }> {
  const port = await freePort();
  const dataDir = mkdtempSync(join(tmpdir(), 'tenplayer-'));
  const dbFile = join(dataDir, 'app.db');
  const baseURL = `http://127.0.0.1:${port}`;
  const proc: ChildProcess = spawn('go', ['run', './cmd/server'], {
    cwd: join(process.cwd(), '..', '..'),
    detached: true,
    stdio: 'ignore',
    env: {
      ...process.env,
      APP_ENV: 'development',
      HOST: '127.0.0.1',
      PORT: String(port),
      DB_URI: `file:${dbFile}?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_txlock=immediate`,
      SESSION_KEY,
      BASE_URL: baseURL,
      ADMIN_EMAILS: SEED_ADMIN_EMAIL,
      LOGIN_COOLDOWN: '0s',
      // Production-like beats so the standings animation plays and lingers.
      // The read beat (REVEAL_DELAY) stays short to keep the round moving.
      SESSION_RUNNER_BEAT: '3s',
      REVEAL_DELAY: '2s',
    },
  });

  const stop = () => {
    try {
      if (proc.pid) process.kill(-proc.pid, 'SIGTERM');
    } catch {
      // already gone
    }
    rmSync(dataDir, { recursive: true, force: true });
  };

  const deadline = Date.now() + 90_000;
  while (Date.now() < deadline) {
    try {
      const resp = await fetch(`${baseURL}/healthz`);
      if (resp.ok) return { baseURL, dbFile, stop };
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  stop();
  throw new Error('dedicated walkthrough server did not become healthy within 90s');
}

// sqlite runs a statement against the dedicated DB and returns trimmed stdout.
function sqlite(dbFile: string, stmt: string): string {
  return execFileSync('sqlite3', [dbFile, stmt], { encoding: 'utf8' }).trim();
}

test.describe('ten-player live game (video walkthrough)', () => {
  test('ten players play a full live multi-round game and the standings reorder each round', async ({
    browser,
    browserName,
  }) => {
    test.skip(
      !process.env.RUN_MANUAL_E2E,
      'on-demand video walkthrough; run with RUN_MANUAL_E2E=1 (kept out of the CI gate by choice)',
    );
    test.skip(browserName !== 'chromium', 'records the walkthrough on chromium only');
    test.setTimeout(300_000);

    const videoDir = join(process.cwd(), 'videos', 'ten-player-live');
    const rawDir = join(videoDir, '_raw');
    mkdirSync(rawDir, { recursive: true });

    const server = await startServer();
    const { baseURL, dbFile } = server;

    // The migration-seeded admin (players.id = 1) ships passwordless on 'host';
    // stamp the known bcrypt hash + admin role so the real /login flow issues a
    // genuine host cookie for this fresh DB (mirrors the worker fixture).
    sqlite(dbFile, `UPDATE players SET role = 'admin', password_hash = '${SEED_ADMIN_PASSWORD_HASH}' WHERE id = 1;`);

    const players: Player[] = [];
    let hostContext: BrowserContext | undefined;
    try {
      // Host: log in for real, seed a multi-round quiz, make it live, open a
      // session, and watch the TV. Recording it captures the room code + live
      // roster + round intros + the animating standings.
      hostContext = await browser.newContext({
        baseURL,
        viewport: { width: 1280, height: 720 },
        recordVideo: { dir: rawDir, size: { width: 1280, height: 720 } },
      });
      const host = await hostContext.newPage();
      await host.goto('/login');
      await host.locator('input[name=email]').fill(SEED_ADMIN_EMAIL);
      await host.locator('input[name=password]').fill(SEED_ADMIN_PASSWORD);
      await host.locator('button[type=submit]').click();
      await expect(host).toHaveURL(/\/admin\/quizzes$/);

      const quizTitle = `Ten Player Live ${Date.now()}`;
      await importQuiz(host, {
        title: quizTitle,
        description: 'Ten-player multi-round video walkthrough',
        rounds: ROUNDS.map((r) => ({
          title: r.title,
          questions: r.questions.map((q) => ({
            text: q.text,
            options: q.options.map((text, i) => ({ text, correct: i === q.correctIndex })),
          })),
        })),
      });
      const escapedTitle = quizTitle.replace(/'/g, "''");
      sqlite(dbFile, `UPDATE quizzes SET mode = 'live' WHERE title = '${escapedTitle}';`);
      const quizID = Number.parseInt(sqlite(dbFile, `SELECT id FROM quizzes WHERE title = '${escapedTitle}';`), 10);
      expect(Number.isInteger(quizID)).toBeTruthy();

      const createResp = await host.request.post('/api/sessions', { data: { quizId: quizID } });
      expect(createResp.status(), `create session: ${createResp.status()} ${await createResp.text()}`).toBe(201);
      const { joinCode } = (await createResp.json()) as { joinCode: string };
      expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

      await host.goto(`/host/${joinCode}`);

      // Ten anonymous players, each in its own phone-viewport context with its
      // own video (the player client is phone-first).
      for (let seat = 0; seat < PLAYER_COUNT; seat++) {
        const context = await browser.newContext({
          baseURL,
          viewport: { width: 390, height: 844 },
          recordVideo: { dir: rawDir, size: { width: 390, height: 844 } },
        });
        const page = await context.newPage();
        players.push({ seat, name: `Player-${String(seat + 1).padStart(2, '0')}`, context, page });
      }

      // Join + ready in parallel: each deep-links to /join/{code}, claims its
      // name, lands in the lobby, and marks ready.
      await Promise.all(
        players.map(async ({ name, page }) => {
          await page.goto(`/join/${joinCode}`);
          await page.getByTestId('join-name-input').fill(name);
          await page.getByTestId('join-name-submit').click();
          await expect(page.getByTestId('lobby-view')).toBeVisible({ timeout: 20_000 });
          await expect(page.getByTestId('lobby-roster').getByText(name, { exact: true })).toBeVisible();
          await page.getByTestId('ready-toggle').click();
        }),
      );

      // Host starts; the runner drives round_intro -> question -> reveal ->
      // round_results on its (now production-like) beat.
      const startResp = await host.request.post(`/api/sessions/${joinCode}/start`);
      expect(startResp.status(), `start: ${startResp.status()} ${await startResp.text()}`).toBe(204);

      // Play every question across every round. Each player picks the correct
      // option when its seat is a winner for that question, otherwise a fixed
      // wrong option - so scores reorder the standings between rounds. Once all
      // active players answer, the runner reveals and (between rounds) shows the
      // animating standings before the next round's intro.
      for (let qi = 0; qi < QUESTIONS.length; qi++) {
        const spec = QUESTIONS[qi];
        const correctText = spec.options[spec.correctIndex];
        const wrongText = spec.options[(spec.correctIndex + 1) % spec.options.length];
        const winners = new Set(CORRECT_SEATS[qi]);
        await Promise.all(
          players.map(async ({ seat, page }) => {
            await expect(page.getByTestId('question-text')).toHaveText(spec.text, { timeout: 30_000 });
            const options = page.getByTestId('question-options');
            await expect(options).toBeVisible({ timeout: 15_000 });
            const choice = winners.has(seat) ? correctText : wrongText;
            const button = options.getByRole('button', { name: choice, exact: true });
            await expect(button).toBeEnabled({ timeout: 10_000 });
            await button.click();
            // The runner early-closes once every active player has answered, so
            // the last to click skip the transient "answered, waiting" state and
            // land straight on the reveal. Accept either: both prove the pick
            // registered.
            await expect(
              page.getByTestId('answered-waiting').or(page.getByTestId('reveal-view')),
            ).toBeVisible({ timeout: 15_000 });
          }),
        );
        await Promise.all(
          players.map(({ page }) => expect(page.getByTestId('reveal-view')).toBeVisible({ timeout: 20_000 })),
        );
      }

      // Finished: every player reaches the finished view, the standings list all
      // ten, and scores spread out (top total beats bottom total) - i.e. the
      // ordering is meaningful, not a flat tie.
      await Promise.all(
        players.map(({ page }) => expect(page.getByTestId('finished-view')).toBeVisible({ timeout: 25_000 })),
      );
      const rows = players[0].page.getByTestId('finished-view').locator('[data-standings-row]');
      await expect(rows).toHaveCount(PLAYER_COUNT, { timeout: 25_000 });
      for (const { name } of players) {
        await expect(
          players[0].page.getByTestId('finished-view').locator('[data-standings-name]', { hasText: name }),
        ).toBeVisible();
      }
      const topTotal = Number(await rows.first().locator('[data-standings-total]').innerText());
      const bottomTotal = Number(await rows.last().locator('[data-standings-total]').innerText());
      expect(topTotal).toBeGreaterThan(bottomTotal);

      // Let the host TV settle on the finished frame for the recording.
      await host.waitForTimeout(2000);
    } finally {
      // Finalize and name every video, then tear down. The video handle must be
      // captured before close; saveAs waits for the file to flush.
      if (hostContext) {
        const hostVideo = hostContext.pages()[0]?.video();
        await hostContext.close();
        if (hostVideo) await hostVideo.saveAs(join(videoDir, 'host-tv.webm'));
      }
      for (const { name, page, context } of players) {
        const video = page.video();
        await context.close();
        if (video) await video.saveAs(join(videoDir, `${name.toLowerCase()}.webm`));
      }
      server.stop();
    }
  });
});
