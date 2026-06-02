import { execFileSync } from 'node:child_process';
import { join } from 'node:path';
import { test, expect } from './fixtures';

// Wipe games and the rows that hang off them on this worker's SQLite
// file so the home page renders empty-state HTML for the
// "fresh DB" assertion below (#433). The worker's DB is shared by every
// test scheduled into the same parallel slot, so without this hook a
// prior test in the same worker can populate `games` / `players` and
// push the home page past firefox's 720px viewport under parallel load.
// Only games + dependent rows need clearing — both ListPopularQuizzes
// and ListMostActivePlayers join against `games` (see
// internal/queries/home.sql), so emptying games drains both sections.
//
// Shells out to the system `sqlite3` CLI rather than opening the DB
// from Node so the test stays free of any sqlite npm dep. SQLite's WAL
// mode tolerates concurrent writers; the running server keeps serving
// other workers while this wipe lands.
test.beforeEach(({}, testInfo) => {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) return;
  const dbFile = join(dataDir, `e2e-${testInfo.parallelIndex}.db`);
  execFileSync('sqlite3', [
    dbFile,
    [
      'DELETE FROM game_answers;',
      'DELETE FROM game_questions;',
      'DELETE FROM game_participants;',
      'DELETE FROM games;',
    ].join(' '),
  ]);
});

// #285 — the body used `min-h-screen` (100vh), which on mobile
// browsers includes the collapsing URL bar in the height; the page
// reliably scrolled by ~footer-height even when the lists fit. Fixed
// by switching to min-h-dvh. The test pins the no-overflow invariant
// at desktop viewports too so the sticky-footer column math stays
// honest if anyone touches the layout again. 1px tolerance for
// sub-pixel rounding (the AC mandates it).
test('start page fits within the viewport on a fresh DB', async ({ page }) => {
  await page.goto('/');

  // Wait for the body to render and any web-font swap to settle so
  // the measurement is taken against final layout, not the initial
  // FOUT pass.
  await expect(page.getByRole('tab', { name: 'Popular' })).toBeVisible();

  const measurement = await page.evaluate(() => ({
    scrollHeight: document.documentElement.scrollHeight,
    innerHeight: window.innerHeight,
  }));
  expect(measurement.scrollHeight,
    `documentElement.scrollHeight (${measurement.scrollHeight}) > window.innerHeight (${measurement.innerHeight}) — home page overflows the viewport on empty-DB content`,
  ).toBeLessThanOrEqual(measurement.innerHeight + 1);
});

// #166 — the public start page at GET /. The test relies on nothing
// beyond what every project starts with: the page renders even with an
// empty database (empty-state messaging is part of the contract). Both
// the popular-quizzes and active-players sections must be present, and
// the discreet admin link in the footer must deep-link a logged-out
// visitor into the /login flow.
test('start page renders the popular + active sections and a discreet admin link', async ({ page }) => {
  await page.goto('/');

  // Title + brand wordmark.
  // Non-production deploys prefix the title with their env label
  // (e.g. "[development] Top Banana!"); CI runs with APP_ENV=development.
  await expect(page).toHaveTitle(/Top Banana!$/);
  await expect(page.getByRole('heading', { level: 1 })).toContainText(/Top\s*Banana!?/i);

  // The primary section is a tablist (Popular / Newest) over two
  // server-rendered lists; the active-players aside stays an <h2>.
  await expect(page.getByRole('tab', { name: 'Popular' })).toBeVisible();
  await expect(page.getByRole('tab', { name: 'Newest' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Most active players', level: 2 })).toBeVisible();

  // Discreet admin link sits in the footer. Logged-out visitors get
  // redirected to /login by the admin middleware, which since #449
  // also carries the original URI as ?next=<encoded> so the visitor
  // can return to /admin after signing in.
  const adminLink = page.getByRole('link', { name: 'Manage quizzes' });
  await expect(adminLink).toBeVisible();
  await adminLink.click();
  await expect(page).toHaveURL(/\/login\?next=%2Fadmin$/);
});
