import { join } from 'node:path';
import { test, expect } from './fixtures';
import { execSqlite } from './helpers';

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
  execSqlite(
    dbFile,
    [
      'DELETE FROM game_answers;',
      'DELETE FROM game_questions;',
      'DELETE FROM game_participants;',
      'DELETE FROM games;',
    ].join(' '),
  );
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
