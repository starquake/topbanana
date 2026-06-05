import type { APIRequestContext } from '@playwright/test';

import { test, expect } from './fixtures';
import {
  registerForPending,
  markEmailVerified,
  markAdmin,
  login,
  seedQuiz,
  setQuizMode,
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
  question: {
    id: number;
    options: { id: number; text: string }[];
  } | null;
};

// optionIdForText reads the participant's GET /state and resolves the option
// id whose text matches, so a player can answer a known choice over the API.
async function optionIdForText(
  request: APIRequestContext,
  code: string,
  text: string,
): Promise<number> {
  const resp = await request.get(`/api/sessions/${code}/state`);
  expect(resp.ok(), `state read: ${resp.status()} ${await resp.text()}`).toBeTruthy();
  const state = (await resp.json()) as SessionState;
  expect(state.phase, 'expected the session to be in the question phase').toBe('question');
  const option = state.question?.options.find((o) => o.text === text);
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
  await page.getByRole('button', { name: 'Play live' }).click();
  await expect(page).toHaveURL(/\/host\/[A-Z0-9]+$/);
  const code = page.url().split('/host/')[1];

  // Two players join from fresh anonymous contexts (each gets its own
  // anonymous player). They ready up so the host start has a non-empty,
  // all-ready roster.
  const caseyCtx = await context.browser()!.newContext({ storageState: undefined, baseURL });
  const danaCtx = await context.browser()!.newContext({ storageState: undefined, baseURL });
  try {
    for (const [ctx, name] of [[caseyCtx, 'Casey'], [danaCtx, 'Dana']] as const) {
      const joinResp = await ctx.request.post(`/api/sessions/${code}/join`, { data: { displayName: name } });
      expect(joinResp.status(), `join ${name}: ${await joinResp.text()}`).toBe(200);
      const readyResp = await ctx.request.post(`/api/sessions/${code}/ready`, { data: { ready: true } });
      expect(readyResp.status()).toBe(204);
    }

    // The TV roster shows both players before the start.
    await expect(page.locator('[data-player-row]')).toHaveCount(2);

    // Host starts the game; the runner moves the session into the first
    // question. The TV swaps from the lobby to the question view off the SSE
    // tick. (round_intro is a brief hold; the question view is what we wait
    // for.)
    await page.getByRole('button', { name: 'Start game' }).click();

    const questionView = page.locator('[data-phase-question]');
    await expect(questionView).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('[data-question-text]')).toHaveText('What is 2+2?');

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
    await expect(badges.nth(0).locator('[data-answered-name]')).toHaveText('Casey');

    const danaOption = await optionIdForText(danaCtx.request, code, '3');
    const danaAns = await danaCtx.request.post(`/api/sessions/${code}/answer`, { data: { optionId: danaOption } });
    expect(danaAns.status(), `dana answer: ${await danaAns.text()}`).toBe(204);

    // Both badges now show, in the order the picks landed: Casey then Dana.
    await expect(badges).toHaveCount(2, { timeout: 10_000 });
    await expect(badges.nth(0).locator('[data-answered-name]')).toHaveText('Casey');
    await expect(badges.nth(1).locator('[data-answered-order]')).toHaveText('2');
    await expect(badges.nth(1).locator('[data-answered-name]')).toHaveText('Dana');

    // With every active player answered, the runner closes the question early
    // and moves into reveal. The TV now lights the correct option ("4") and
    // shows a Correct badge - the first time correctness is exposed.
    const correctOption = page.locator('[data-answer-option][data-correct="true"]');
    await expect(correctOption).toHaveCount(1, { timeout: 15_000 });
    await expect(correctOption).toContainText('4');
    await expect(correctOption.locator('[data-correct-badge]')).toBeVisible();
  } finally {
    await caseyCtx.close();
    await danaCtx.close();
  }
});
