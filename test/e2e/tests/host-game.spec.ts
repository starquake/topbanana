import type { APIRequestContext } from '@playwright/test';

import { test, expect } from './fixtures';
import {
  registerForPending,
  markEmailVerified,
  markAdmin,
  login,
  seedQuiz,
  setQuizMode,
  importQuiz,
} from './helpers';

// MP-8 (#685): the host TV in-game view. The host puts a live quiz on a TV,
// players join and ready up, the host starts, and the TV shows the live
// question with a countdown, the answered-order badges filling in (no
// correctness), an all-answered indicator, then the reveal of the correct
// answer. The server runner drives every phase transition; the TV only
// re-renders whatever phase the latest GET /state reports.
//
// MP-7's player in-game UI is owned by another worktree, so the players are
// driven through the REST API (join / ready / answer) from anonymous
// contexts. The state read returns the live question's option ids to a
// participant, so a player can pick a known option without a UI.

type SessionState = {
  phase: string;
  serverNow: string;
  question: {
    id: number;
    startedAt: string | null;
    options: { id: number; text: string }[];
  } | null;
};

// optionIdForText reads the participant's GET /state and resolves the option
// id whose text matches, so a player can answer a known choice over the API.
// It polls until the answer window has opened (serverNow at or after
// startedAt), since the read beat (#247 parity) holds answers closed for a
// brief beat after the question is issued and a pick before then would 409.
async function optionIdForText(
  request: APIRequestContext,
  code: string,
  text: string,
): Promise<number> {
  let state: SessionState | undefined;
  await expect(async () => {
    const resp = await request.get(`/api/sessions/${code}/state`);
    expect(resp.ok(), `state read: ${resp.status()} ${await resp.text()}`).toBeTruthy();
    state = (await resp.json()) as SessionState;
    expect(state.phase, 'expected the session to be in the question phase').toBe('question');
    expect(state.question?.startedAt, 'question should carry an answers-open anchor').toBeTruthy();
    expect(
      Date.parse(state.serverNow) >= Date.parse(state.question!.startedAt!),
      'answer window should have opened (read beat elapsed)',
    ).toBeTruthy();
  }).toPass({ timeout: 10_000 });
  const option = state!.question?.options.find((o) => o.text === text);
  expect(option, `option ${text} not found in question`).toBeTruthy();

  return option!.id;
}

test('host TV shows the live question, answered order, and the reveal', async ({
  page,
  context,
  baseURL,
  browserName,
}) => {
  const displayName = `e2e-game-host-${browserName}`;
  const quizTitle = `E2E Host Game ${browserName}`;

  await registerForPending(page, displayName);
  markEmailVerified(displayName);
  markAdmin(displayName);
  await login(page, displayName);
  await expect(page).toHaveURL(/\/admin\/quizzes$/);

  await seedQuiz(page, quizTitle);
  setQuizMode(quizTitle, 'live');

  // Open a session and land on the TV lobby.
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  await page.getByRole('button', { name: 'Host live' }).click();
  await expect(page).toHaveURL(/\/host\/[A-Z0-9]+$/);
  const code = page.url().split('/host/')[1];

  // Two players join from fresh anonymous contexts (each gets its own
  // anonymous player). They ready up so the host start has a non-empty,
  // all-ready roster.
  // Player names are global on players.display_name now (#716), so use unique
  // names to avoid colliding with a parallel spec on the worker DB.
  const casey = `Casey-${browserName}-${Date.now()}`;
  const dana = `Dana-${browserName}-${Date.now()}`;
  const caseyCtx = await context.browser()!.newContext({ storageState: undefined, baseURL });
  const danaCtx = await context.browser()!.newContext({ storageState: undefined, baseURL });
  try {
    for (const [ctx, name] of [[caseyCtx, casey], [danaCtx, dana]] as const) {
      // #716: the join carries no name. An anonymous player claims their
      // players.display_name through the shared claim endpoint first, then
      // joins; the roster and answered-order badges read that current name.
      const claimResp = await ctx.request.patch('/api/players/me', { data: { displayName: name } });
      expect(claimResp.status(), `claim ${name}: ${await claimResp.text()}`).toBe(200);
      const joinResp = await ctx.request.post(`/api/sessions/${code}/join`);
      expect(joinResp.status(), `join ${name}: ${await joinResp.text()}`).toBe(200);
      const readyResp = await ctx.request.post(`/api/sessions/${code}/ready`, { data: { ready: true } });
      expect(readyResp.status()).toBe(204);
    }

    // The TV roster shows both players before the start.
    await expect(page.locator('[data-player-row]')).toHaveCount(2);

    // Host starts the game now; the runner moves the session into the first
    // round's intro, then the first question. The TV swaps phases off the SSE
    // tick.
    await page.getByRole('button', { name: 'Start now' }).click();

    // Round intro (#748): the TV names the round about to start. The seeded
    // quiz lands every question in the default round titled "Round 1", so the
    // title shows it and the eyebrow reads "Round 1 of 1" - never the old
    // generic "Next round" wording.
    const introView = page.locator('[data-phase-intro]');
    await expect(introView).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('[data-round-title]')).toHaveText('Round 1');
    await expect(page.locator('[data-round-eyebrow]')).toHaveText('Round 1 of 1');

    const questionView = page.locator('[data-phase-question]');
    await expect(questionView).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('[data-question-text]')).toHaveText('What is 2+2?');

    // Read beat (#247 parity): the TV shows the question with the options and
    // answered-order area hidden behind a "Get ready" indicator until the
    // answer window opens, then the options appear.
    await expect(page.locator('[data-read-beat]')).toBeVisible();
    await expect(page.locator('[data-answer-option]').first()).toBeHidden();
    await expect(page.locator('[data-answer-option]').first()).toBeVisible({ timeout: 10_000 });

    // Before any answer, correctness is hidden: no option is lit correct and
    // no Correct badge is visible (the no-spoiler guarantee). The badge spans
    // sit in the DOM but stay display:none via x-show until reveal, so assert
    // visibility, not presence.
    await expect(page.locator('[data-answer-option][data-correct="true"]')).toHaveCount(0);
    await expect(page.locator('[data-correct-badge]:visible')).toHaveCount(0);

    // Casey answers the correct option first, then Dana answers a wrong one.
    // The badges fill in answer order, never correctness order.
    const caseyOption = await optionIdForText(caseyCtx.request, code, '4');
    const caseyAns = await caseyCtx.request.post(`/api/sessions/${code}/answer`, { data: { optionId: caseyOption } });
    expect(caseyAns.status(), `casey answer: ${await caseyAns.text()}`).toBe(204);

    // The first badge appears for Casey before Dana answers, so the order is
    // unambiguous.
    const badges = page.locator('[data-answered-badge]');
    await expect(badges).toHaveCount(1, { timeout: 10_000 });
    await expect(badges.nth(0).locator('[data-answered-order]')).toHaveText('1');
    await expect(badges.nth(0).locator('[data-answered-name]')).toHaveText(casey);

    const danaOption = await optionIdForText(danaCtx.request, code, '3');
    const danaAns = await danaCtx.request.post(`/api/sessions/${code}/answer`, { data: { optionId: danaOption } });
    expect(danaAns.status(), `dana answer: ${await danaAns.text()}`).toBe(204);

    // Both badges now show, in the order the picks landed: Casey then Dana.
    await expect(badges).toHaveCount(2, { timeout: 10_000 });
    await expect(badges.nth(0).locator('[data-answered-name]')).toHaveText(casey);
    await expect(badges.nth(1).locator('[data-answered-order]')).toHaveText('2');
    await expect(badges.nth(1).locator('[data-answered-name]')).toHaveText(dana);

    // With every active player answered, the runner closes the question early
    // and moves into reveal. The TV now lights the correct option ("4") and
    // shows a Correct badge - the first time correctness is exposed.
    const correctOption = page.locator('[data-answer-option][data-correct="true"]');
    await expect(correctOption).toHaveCount(1, { timeout: 15_000 });
    await expect(correctOption).toContainText('4');
    await expect(correctOption.locator('[data-correct-badge]')).toBeVisible();

    // The answered-order badges also gain correctness at reveal: Casey picked
    // the right option ("4") so her badge is marked correct, Dana picked a
    // wrong one ("3") so his is marked incorrect (#734). The order stays
    // answer order, so badge 0 is Casey and badge 1 is Dana.
    await expect(badges.nth(0)).toHaveAttribute('data-correctness', 'correct', { timeout: 15_000 });
    await expect(badges.nth(0).locator('[data-answered-name]')).toHaveText(casey);
    await expect(badges.nth(1)).toHaveAttribute('data-correctness', 'incorrect');
    await expect(badges.nth(1).locator('[data-answered-name]')).toHaveText(dana);
  } finally {
    await caseyCtx.close();
    await danaCtx.close();
  }
});

// #716: the live surfaces show the player's CURRENT players.display_name, so a
// rename propagates everywhere. A player joins under one name, renames their
// players row through the shared claim endpoint, and the host TV lobby roster
// shows the new name. The rename does not itself publish a session tick, so the
// player toggles ready (a lobby mutation that does publish) to make the TV
// re-GET state - the eventual-consistency the SSE side-channel gives.
test('the host TV roster reflects a player rename', async ({
  page,
  context,
  baseURL,
  browserName,
}) => {
  const displayName = `e2e-rename-host-${browserName}`;
  const quizTitle = `E2E Rename Game ${browserName}`;

  await registerForPending(page, displayName);
  markEmailVerified(displayName);
  markAdmin(displayName);
  await login(page, displayName);
  await expect(page).toHaveURL(/\/admin\/quizzes$/);

  await seedQuiz(page, quizTitle);
  setQuizMode(quizTitle, 'live');

  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  await page.getByRole('button', { name: 'Host live' }).click();
  await expect(page).toHaveURL(/\/host\/[A-Z0-9]+$/);
  const code = page.url().split('/host/')[1];

  // Player names are global on players.display_name now (#716), so use unique
  // names to avoid colliding with a parallel spec on the worker DB.
  const before = `Before-${browserName}-${Date.now()}`;
  const after = `Renamed-${browserName}-${Date.now()}`;
  const playerCtx = await context.browser()!.newContext({ storageState: undefined, baseURL });
  try {
    const claimResp = await playerCtx.request.patch('/api/players/me', { data: { displayName: before } });
    expect(claimResp.status(), `claim: ${await claimResp.text()}`).toBe(200);
    const joinResp = await playerCtx.request.post(`/api/sessions/${code}/join`);
    expect(joinResp.status(), `join: ${await joinResp.text()}`).toBe(200);

    const row = page.locator('[data-player-row]');
    await expect(row).toHaveCount(1);
    await expect(row).toContainText(before);

    // Rename the player's account, then publish a tick (ready toggle) so the
    // TV re-reads state and picks up the new name.
    const renameResp = await playerCtx.request.patch('/api/players/me', { data: { displayName: after } });
    expect(renameResp.status(), `rename: ${await renameResp.text()}`).toBe(200);
    const readyResp = await playerCtx.request.post(`/api/sessions/${code}/ready`, { data: { ready: true } });
    expect(readyResp.status()).toBe(204);

    await expect(row).toContainText(after, { timeout: 10_000 });
    await expect(row).not.toContainText(before);
  } finally {
    await playerCtx.close();
  }
});

// #755 cross-surface contract (TV half): the host TV round-intro names the round
// and words its heading correctly, matching the live player surface (join.html)
// and the solo client (index.html) field-for-field even though the TV uses its
// own room-scale typography. A two-round quiz with a round summary exercises all
// three round-intro fields the surfaces share: the title (data-round-title), the
// optional summary (data-round-summary), and an accurate "Round N of M" eyebrow
// (data-round-eyebrow) that is NOT the old generic "Next round" wording on the
// first round. Asserting "Round 1 of 2" (not the single-round "Round 1 of 1" the
// in-game spec above checks) pins that N/M reflects the real round position. The
// player half is pinned in play-live.spec.ts; the standings half is in
// standings-bargraph.spec.ts.
test('host TV round intro shows the round title, summary, and an accurate Round N of M heading', async ({
  page,
  context,
  baseURL,
  browserName,
}) => {
  test.setTimeout(60_000);

  const displayName = `e2e-intro-host-${browserName}`;
  const quizTitle = `E2E Host Intro ${browserName} ${Date.now()}`;
  const roundSummary = 'Warm up with the easy ones first.';

  await registerForPending(page, displayName);
  markEmailVerified(displayName);
  markAdmin(displayName);
  await login(page, displayName);
  await expect(page).toHaveURL(/\/admin\/quizzes$/);

  // A two-round quiz, imported live: the first round carries a summary so the
  // optional copy is exercised, and the round count is 2 so the eyebrow reads
  // "Round 1 of 2".
  await importQuiz(page, {
    title: quizTitle,
    description: 'Host round-intro contract spec',
    rounds: [
      {
        title: 'Opening round',
        summary: roundSummary,
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
        title: 'Closing round',
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
  }, 'live');

  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  await page.getByRole('button', { name: 'Host live' }).click();
  await expect(page).toHaveURL(/\/host\/[A-Z0-9]+$/);
  const code = page.url().split('/host/')[1];

  // One player joins and readies so the start has a non-empty, all-ready roster
  // and the runner advances into the round intro.
  const casey = `Casey-${browserName}-${Date.now()}`;
  const caseyCtx = await context.browser()!.newContext({ storageState: undefined, baseURL });
  try {
    const claimResp = await caseyCtx.request.patch('/api/players/me', { data: { displayName: casey } });
    expect(claimResp.status(), `claim ${casey}: ${await claimResp.text()}`).toBe(200);
    const joinResp = await caseyCtx.request.post(`/api/sessions/${code}/join`);
    expect(joinResp.status(), `join ${casey}: ${await joinResp.text()}`).toBe(200);
    const readyResp = await caseyCtx.request.post(`/api/sessions/${code}/ready`, { data: { ready: true } });
    expect(readyResp.status()).toBe(204);

    await expect(page.locator('[data-player-row]')).toHaveCount(1);
    await page.getByRole('button', { name: 'Start now' }).click();

    // Round intro: the TV names the first round, shows its summary, and the
    // eyebrow reads "Round 1 of 2" - never "Next round" on the first round.
    const introView = page.locator('[data-phase-intro]');
    await expect(introView).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('[data-round-title]')).toHaveText('Opening round');
    await expect(page.locator('[data-round-summary]')).toHaveText(roundSummary);
    await expect(page.locator('[data-round-eyebrow]')).toHaveText('Round 1 of 2');
    await expect(introView).not.toContainText('Next round');
  } finally {
    await caseyCtx.close();
  }
});
