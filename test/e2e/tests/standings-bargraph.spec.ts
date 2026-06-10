import type { APIRequestContext, Page } from '@playwright/test';
import { join } from 'node:path';

import { test, expect } from './fixtures';
import { importQuiz, claimAndJoin, execSqlite } from './helpers';

// MP-9 (#686): the between-rounds standings bar graph on the round_results /
// finished screens, on BOTH the host TV surface and the player join surface.
// The graph animates each player's round points onto their pre-round total and
// rests the rows in rank order (best-first). The animation is reduced-motion
// safe, so this spec runs the contexts under prefers-reduced-motion: reduce and
// asserts the final settled DOM (rendered order + numeric totals) - which also
// exercises the reduced-motion jump-to-final path.
//
// The runner drives every phase transition on its 500ms e2e beat, so the
// round_results window is brief; the assertions use Playwright's retrying
// web-first matchers, and the terminal finished phase (stable) is asserted in
// full on both surfaces.

// SSE-driven standings settle only after the runner advances a phase and the
// tick reaches the client. Under the full parallel e2e run the CI box is
// saturated, so that round-trip occasionally takes far longer than the
// happy-path sub-second. These waits are retrying matchers (toPass / web-first
// toBeVisible) that resolve the instant the condition holds, so a generous
// budget is free on the happy path and only spends time on a genuine failure -
// headroom for worst-case contention, not a fixed wait (#811).
const STANDINGS_SETTLE_TIMEOUT = 30_000;

// makeQuizLiveByTitle flips a quiz to mode='live' (the importer lands quizzes
// on 'solo', and only live quizzes are hostable) and returns its id. Mirrors
// the sqlite3 shortcut the other live-session specs use.
function makeQuizLiveByTitle(title: string): number {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; cannot mark a quiz live');
  }
  const dbFile = join(dataDir, `e2e-${test.info().parallelIndex}.db`);
  const escapedTitle = title.replace(/'/g, "''");
  const output = execSqlite(
    dbFile,
    `UPDATE quizzes SET mode = 'live' WHERE title = '${escapedTitle}'; SELECT id FROM quizzes WHERE title = '${escapedTitle}';`,
  );
  const lines = output.split('\n');
  const id = Number.parseInt(lines[lines.length - 1], 10);
  if (!Number.isInteger(id)) {
    throw new Error(`makeQuizLiveByTitle(${title}): could not resolve quiz id from ${JSON.stringify(output)}`);
  }
  return id;
}

type SessionState = {
  phase: string;
  serverNow: string;
  question: { id: number; startedAt: string | null; options: { id: number; text: string }[] } | null;
};

// answerOverApi resolves the option id whose text matches off the participant's
// GET /state and POSTs it, so an API-only player can answer a known choice with
// no UI. It waits for the answer window to open (serverNow at or after
// startedAt) before posting, since the read beat (#247 parity) holds answers
// closed for a brief beat after the question is issued. Returns silently when
// the session is not currently in the question phase (the runner may have
// advanced past it).
async function answerOverApi(
  request: APIRequestContext,
  code: string,
  text: string,
): Promise<void> {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    const resp = await request.get(`/api/sessions/${code}/state`);
    if (!resp.ok()) return;
    const state = (await resp.json()) as SessionState;
    if (state.phase !== 'question' || !state.question) return;
    const open = state.question.startedAt
      ? Date.parse(state.serverNow) >= Date.parse(state.question.startedAt)
      : true;
    if (open) {
      const option = state.question.options.find((o) => o.text === text);
      if (!option) return;
      await request.post(`/api/sessions/${code}/answer`, { data: { optionId: option.id } });
      return;
    }
    await new Promise((r) => setTimeout(r, 50));
  }
}

// answerOnPage clicks the option with the given text on the player surface once
// the question view is shown. Tolerates the runner having already advanced.
async function answerOnPage(page: Page, text: string): Promise<void> {
  const view = page.getByTestId('question-view');
  try {
    await expect(view).toBeVisible({ timeout: 15_000 });
  } catch {
    return;
  }
  const button = page.getByTestId('question-options').getByRole('button', { name: text, exact: true });
  try {
    await expect(button).toBeEnabled({ timeout: 5_000 });
    await button.click();
  } catch {
    // The window may have closed (the runner early-closes once everyone is in);
    // the answer is not required for the standings assertions.
  }
}

// readStandingsRows reads the rendered standings rows in DOM order, returning
// each row's rank, playerId, the displayed name, and the displayed total. DOM
// order is the on-screen order, so a best-first assertion checks both the data
// and the sort; the playerId order pins that the keyed nodes ended in the new
// ranking (the FLIP target order).
async function readStandingsRows(
  scope: Page,
): Promise<{ rank: string; playerId: string; name: string; total: string }[]> {
  const rows = scope.locator('[data-testid="standings-bars"] [data-standings-row]');
  const count = await rows.count();
  const out: { rank: string; playerId: string; name: string; total: string }[] = [];
  for (let i = 0; i < count; i++) {
    const row = rows.nth(i);
    out.push({
      rank: (await row.getAttribute('data-rank')) ?? '',
      playerId: (await row.getAttribute('data-player-id')) ?? '',
      name: (await row.locator('[data-standings-name]').innerText()).trim(),
      total: (await row.locator('[data-standings-total]').innerText()).trim(),
    });
  }
  return out;
}

// standingsRowTransforms reads the inline transform on each rendered row. The
// FLIP slides via an inline translateY that runAnim clears on complete (and
// under reduced motion clears synchronously, in the same frame the inverse was
// applied). Once the screen has settled no row should carry a residual
// transform - this pins that the reduced-motion path leaves no stuck translateY.
async function standingsRowTransforms(scope: Page): Promise<string[]> {
  return scope.evaluate(() => {
    const rows = document.querySelectorAll('[data-testid="standings-bars"] [data-standings-row]');
    return Array.from(rows).map((row) => (row as HTMLElement).style.transform);
  });
}

// readFinishedStanding reads the end-of-game /state standing for the named
// player, returning the roundScore (the last round's points, the animation fuel
// #729) and totalScore. A game now ends in intermission (#836), the
// between-games screen that carries the same final standings; the bar graph
// animates from totalScore - roundScore up to totalScore, so a non-zero
// roundScore is what makes the bars grow.
async function readFinishedStanding(
  request: APIRequestContext,
  code: string,
  displayName: string,
): Promise<{ roundScore: number; totalScore: number }> {
  const resp = await request.get(`/api/sessions/${code}/state`);
  expect(resp.ok(), `state: ${resp.status()} ${await resp.text()}`).toBe(true);
  const state = (await resp.json()) as {
    phase: string;
    standings: { displayName: string; roundScore: number; totalScore: number }[] | null;
  };
  expect(state.phase).toBe('intermission');
  const standing = (state.standings ?? []).find((s) => s.displayName === displayName);
  expect(standing, `end-of-game standings missing ${displayName}`).toBeTruthy();
  return { roundScore: standing!.roundScore, totalScore: standing!.totalScore };
}

test('the standings bar graph shows final order and totals on the TV and player surfaces', async ({
  page,
  hostSessions,
  browserName,
}) => {
  test.setTimeout(90_000);

  const quizTitle = `MP9 Standings ${browserName} ${Date.now()}`;
  // Player display names are global on players.display_name now (#716), so
  // parallel specs sharing a worker DB must not collide on a literal name.
  const suffix = `${browserName}-${Date.now()}`;
  const quincy = `Quincy-${suffix}`;
  const robin = `Robin-${suffix}`;

  // Host context (shared admin) seeds a two-round quiz, makes it live, opens a
  // session, and watches the TV. The player surface (page) stays anonymous. The
  // standings animation is asserted at its settled (reduced-motion) state, so
  // the host TV runs under prefers-reduced-motion like the player page below.
  const host = await hostSessions.adminHost();
  await host.emulateMedia({ reducedMotion: 'reduce' });

  // Two rounds, one question each. The page player answers the correct option
  // every time and the API player answers a wrong one, so the page player leads
  // throughout - a deterministic best-first order with distinct totals.
  await importQuiz(host, {
    title: quizTitle,
    description: 'MP-9 standings bar graph spec',
    rounds: [
      {
        title: 'Round one',
        questions: [
          { text: 'What is 2+2?', options: [
            { text: '3', correct: false },
            { text: '4', correct: true },
            { text: '5', correct: false },
            { text: '6', correct: false },
          ] },
        ],
      },
      {
        title: 'Round two',
        questions: [
          { text: 'What is 3+3?', options: [
            { text: '5', correct: false },
            { text: '6', correct: true },
            { text: '7', correct: false },
            { text: '8', correct: false },
          ] },
        ],
      },
    ],
  });
  const quizID = makeQuizLiveByTitle(quizTitle);

  const { joinCode } = await hostSessions.openViaApi(quizID);

  // The host opens the TV surface.
  await host.goto(`/host/${joinCode}`);

  // An API-only second player joins from its own anonymous context. It only
  // drives answers over the request API, so reduced motion is moot for it.
  const otherContext = await hostSessions.newPlayerContext();
  const other = otherContext.request;
  await claimAndJoin(other, joinCode, robin);

  // The page player joins via the deep link and lands in the lobby.
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto(`/join/${joinCode}`);
  await page.getByTestId('join-name-input').fill(quincy);
  await page.getByTestId('join-name-submit').click();
  await expect(page.getByTestId('lobby-roster').getByText(quincy)).toBeVisible();

  // Host starts the game; the runner drives the phases on its own beat.
  const startResp = await host.request.post(`/api/sessions/${joinCode}/start`);
  expect(startResp.status(), `start session: ${startResp.status()} ${await startResp.text()}`).toBe(204);

  // Round 1: Quincy answers '4' (correct), Robin answers '3' (wrong).
  await answerOnPage(page, '4');
  await answerOverApi(other, joinCode, '3');

  // Round 1 round_results: both surfaces show Quincy on top (she scored, Robin
  // did not). The window is brief, so use retrying matchers; reduced motion
  // means the final state is rendered on the first paint. The standings <ul>
  // now stays mounted across the standings phases and is no longer nested in the
  // round-results marker (#773), so the round_results screen is pinned by its
  // own marker being visible plus the shared standings rows rendering.
  await expect(page.getByTestId('round-results')).toBeVisible({ timeout: STANDINGS_SETTLE_TIMEOUT });
  await expect(page.locator('[data-testid="standings-bars"] [data-standings-row]').first())
    .toBeVisible({ timeout: STANDINGS_SETTLE_TIMEOUT });
  await expect(async () => {
    const rows = await readStandingsRows(page);
    expect(rows.length).toBe(2);
    expect(rows[0].name).toBe(quincy);
    expect(rows[0].rank).toBe('1');
    expect(Number(rows[0].total)).toBeGreaterThan(0);
    expect(rows[1].name).toBe(robin);
    expect(rows[1].rank).toBe('2');
    expect(Number(rows[1].total)).toBe(0);
  }).toPass({ timeout: STANDINGS_SETTLE_TIMEOUT });

  // The player's own row is highlighted (aria-current). This settles
  // separately from the totals above (the client tags its own row once the
  // SSE state names it), so it gets the same generous settle timeout as the
  // sibling assertions rather than the implicit 5s default — under CI load the
  // highlight can lag past 5s, which flaked this assertion (#845). Quincy leads,
  // so her row is first; it is the viewer's own row, so it carries aria-current.
  await expect(
    page.locator('[data-testid="standings-bars"] [data-standings-row]').first(),
  ).toHaveAttribute('aria-current', 'true', { timeout: STANDINGS_SETTLE_TIMEOUT });

  // Round 2: Quincy answers '6' (correct), Robin answers '5' (wrong).
  await answerOnPage(page, '6');
  await answerOverApi(other, joinCode, '5');

  // End of game: the room enters intermission (#836), the between-games screen
  // that shows the final standings while the room stays alive. Both surfaces
  // show the final standings bar graph with Quincy first (two correct answers)
  // and Robin second on zero. Wait for the host end-of-game standings to render
  // (so the server has reached intermission), then read the authoritative final
  // totals the grow + slide settles the displayed bars onto.
  await expect(host.locator('[data-phase-results] [data-standings-row]').first())
    .toBeVisible({ timeout: STANDINGS_SETTLE_TIMEOUT });
  const quincyFinal = await readFinishedStanding(host.request, joinCode, quincy);
  const robinFinal = await readFinishedStanding(host.request, joinCode, robin);

  // Wait for the grow + slide to settle so the displayed totals match the
  // server. Under reduced motion the bars snap to their finals, but the read
  // still races the first paint, so retry until the leader's bar reaches its
  // final total.
  let tvRows = await readStandingsRows(host);
  await expect(async () => {
    tvRows = await readStandingsRows(host);
    expect(tvRows.length).toBe(2);
    expect(Number(tvRows[0].total)).toBe(quincyFinal.totalScore);
  }).toPass({ timeout: STANDINGS_SETTLE_TIMEOUT });
  expect(tvRows[0].name).toBe(quincy);
  expect(tvRows[0].rank).toBe('1');
  expect(Number(tvRows[0].total)).toBeGreaterThan(0);
  expect(tvRows[1].name).toBe(robin);
  expect(tvRows[1].rank).toBe('2');
  expect(Number(tvRows[1].total)).toBe(0);

  // The end-of-game screen is the intermission marker plus the shared standings
  // rows (the <ul> is no longer nested in the marker, #773).
  await expect(page.getByTestId('intermission-view')).toBeVisible({ timeout: STANDINGS_SETTLE_TIMEOUT });
  await expect(page.locator('[data-testid="standings-bars"] [data-standings-row]').first())
    .toBeVisible({ timeout: STANDINGS_SETTLE_TIMEOUT });
  let playerRows = await readStandingsRows(page);
  await expect(async () => {
    playerRows = await readStandingsRows(page);
    expect(playerRows.length).toBe(2);
    expect(Number(playerRows[0].total)).toBe(quincyFinal.totalScore);
  }).toPass({ timeout: STANDINGS_SETTLE_TIMEOUT });
  expect(playerRows[0].name).toBe(quincy);
  expect(playerRows[0].rank).toBe('1');
  expect(Number(playerRows[0].total)).toBeGreaterThan(0);
  expect(playerRows[1].name).toBe(robin);
  expect(playerRows[1].rank).toBe('2');
  expect(Number(playerRows[1].total)).toBe(0);

  // The keyed rows settle in the new best-first ranking: the data-player-id
  // order matches the rank order on both surfaces, and the two surfaces agree.
  // The FLIP slides these same keyed rows into this order; under reduced motion
  // (this run) they snap to it with no residual transform.
  expect(tvRows.map((r) => r.playerId)).toEqual(playerRows.map((r) => r.playerId));
  expect(tvRows[0].playerId).not.toBe(tvRows[1].playerId);
  expect(Number(tvRows[0].playerId)).toBeGreaterThan(0);
  expect(Number(tvRows[1].playerId)).toBeGreaterThan(0);

  // No row carries a leftover slide transform once the screen has settled
  // (the reduced-motion path must not strand an inverted translateY on a row).
  const tvTransforms = await standingsRowTransforms(host);
  for (const t of tvTransforms) expect(t === '' || t === 'none').toBe(true);
  const playerTransforms = await standingsRowTransforms(page);
  for (const t of playerTransforms) expect(t === '' || t === 'none').toBe(true);

  // #749: the last round must end on a single final-standings screen. The
  // player surface shows only the end-of-game intermission view (no
  // between-rounds round-results), and the host TV heading reads "Final scores",
  // never "Scores so far".
  await expect(page.getByTestId('round-results')).toHaveCount(0);
  await expect(host.getByText('Final scores')).toBeVisible();
  await expect(host.getByText('Scores so far')).toHaveCount(0);

  // #729: the end-of-game standings now carry the last round's score so the bar
  // graph animates that final contribution (rather than landing statically).
  // Quincy scored in round 2 (the last round), so her finished roundScore is the
  // animation fuel: > 0, with a pre-round start (totalScore - roundScore) below
  // her final total. Robin scored nothing in the last round, so roundScore stays
  // 0 (no growth). The settled DOM above (rendered under reduced motion) is the
  // reduced-motion jump-to-final path; this pins the data that drives the grow.
  expect(quincyFinal.totalScore).toBe(Number(tvRows[0].total));
  expect(quincyFinal.roundScore).toBeGreaterThan(0);
  expect(quincyFinal.totalScore - quincyFinal.roundScore).toBeLessThan(quincyFinal.totalScore);
  expect(robinFinal.roundScore).toBe(0);
});
