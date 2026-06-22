import type { APIRequestContext, Locator, Page } from '@playwright/test';

import { test, expect } from './fixtures';
import { importQuiz, setQuizMode, claimAndJoin, playerRow } from './helpers';

// standingsRow scopes to a between-games standings bar by player name. Like the
// lobby roster, the host's one-room-per-host reuse can leave a stale prior-game
// row in the standings, so specs assert a player's row is present by name rather
// than a strict total count (#957, #1061).
function standingsRow(page: Page, displayName: string): Locator {
  return page
    .locator('[data-testid="standings-bars"] [data-standings-row]')
    .filter({ hasText: displayName });
}

// #876: the host big screen reconnecting mid-game. The TV is on a laptop/TV that
// can sleep, lose wifi, or get refreshed. A reload must re-GET /state, re-subscribe
// through the host SSE branch (HandleSessionEvents -> host heartbeat), and resume
// on the current phase - not drop back to the lobby or sit stale. The player side
// of resume is covered in reconnect-resume.spec.ts; this is the host TV half.
//
// The host TV is the shared admin page (the big screen carries the host-only Start
// / End controls and CSRF token); the players join over the REST API from their own
// anonymous contexts. Phase transitions are server-driven by the session runner.

type SessionState = {
  phase: string;
  serverNow: string;
  question: { id: number; startedAt: string | null; options: { id: number; text: string }[] } | null;
};

// seedLiveQuiz imports a single-round, single-question quiz as the shared admin
// and flips it to mode='live' so it is hostable. The answer window is generous
// (120s) so the question stays open across a host reload instead of timing out
// mid-reconnect under CI load.
async function seedLiveQuiz(host: Page, title: string, questionText: string, correct: string): Promise<void> {
  await importQuiz(host, {
    title,
    description: 'E2E host reconnect quiz',
    timeLimitSeconds: 120,
    questions: [
      {
        text: questionText,
        options: [
          { text: correct, correct: true },
          { text: 'wrong-a', correct: false },
          { text: 'wrong-b', correct: false },
          { text: 'wrong-c', correct: false },
        ],
      },
    ],
  });
  setQuizMode(title, 'live');
}

// answerOverApi resolves the option id whose text matches off the participant's
// GET /state and POSTs it, so an API-only player can answer a known choice with
// no UI. It waits for the answer window to open (serverNow at or after startedAt)
// since the read beat (#247 parity) holds answers closed for a brief beat after
// the question is issued, and a pick before then would 409.
async function answerOverApi(request: APIRequestContext, code: string, text: string): Promise<void> {
  let optionId: number | undefined;
  await expect(async () => {
    const resp = await request.get(`/api/sessions/${code}/state`);
    expect(resp.ok(), `state read: ${resp.status()} ${await resp.text()}`).toBeTruthy();
    const state = (await resp.json()) as SessionState;
    expect(state.phase, 'expected the session to be in the question phase').toBe('question');
    expect(state.question?.startedAt, 'question should carry an answers-open anchor').toBeTruthy();
    expect(
      Date.parse(state.serverNow) >= Date.parse(state.question!.startedAt!),
      'answer window should have opened (read beat elapsed)',
    ).toBeTruthy();
    const option = state.question!.options.find((o) => o.text === text);
    expect(option, `option ${text} not found in question`).toBeTruthy();
    optionId = option!.id;
  }).toPass({ timeout: 15_000 });
  const answerResp = await request.post(`/api/sessions/${code}/answer`, { data: { optionId } });
  expect(answerResp.status(), `answer: ${await answerResp.text()}`).toBe(204);
}

// joinAndReady claims a global display name on the request context's player row
// (#716) and joins, then readies up so the host start has a non-empty, all-ready
// roster.
async function joinAndReady(request: APIRequestContext, code: string, displayName: string): Promise<void> {
  await claimAndJoin(request, code, displayName);
  const readyResp = await request.post(`/api/sessions/${code}/ready`, { data: { ready: true } });
  expect(readyResp.status()).toBe(204);
}

test.describe('host reconnect mid-game', () => {
  test('the host big screen reloaded mid-question resumes the live question and the game still completes', async ({ hostSessions }) => {
    test.setTimeout(90_000);

    const stamp = Date.now();
    const quizTitle = `Host Reconnect Question ${stamp}`;
    const questionText = 'What is 2+2?';
    const correct = '4';
    // Player names are global on players.display_name (#716), so use unique
    // names to avoid colliding with a parallel spec on the worker DB.
    const robin = `Robin-${stamp}`;
    const quincy = `Quincy-${stamp}`;

    const host = await hostSessions.adminHost();
    await seedLiveQuiz(host, quizTitle, questionText, correct);

    // Open the big screen by hosting the seeded live quiz; the host lands on
    // /host/{code}. This admin page is the TV that gets reloaded.
    const { joinCode } = await hostSessions.hostLive(quizTitle);
    expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

    // Two players join from their own anonymous contexts and ready up. They hold
    // their answers so the question never early-closes while the host reloads.
    const robinContext = await hostSessions.newPlayerContext();
    const quincyContext = await hostSessions.newPlayerContext();
    await joinAndReady(robinContext.request, joinCode, robin);
    await joinAndReady(quincyContext.request, joinCode, quincy);

    // Present-by-name, not a strict total: one-room-per-host reuse can leave a
    // stale prior-test row in the roster (#957, #1061).
    await expect(playerRow(host, robin)).toBeVisible({ timeout: 15_000 });
    await expect(playerRow(host, quincy)).toBeVisible();

    // Host starts now; the runner drives round_intro -> question. The TV reaches
    // the live question phase.
    await host.getByRole('button', { name: 'Start now' }).click();
    await expect(host.locator('[data-phase-question]')).toBeVisible({ timeout: 20_000 });
    await expect(host.locator('[data-question-text]')).toHaveText(questionText);

    // The drop: reload the host big screen mid-question. The reloaded page must
    // re-GET /state and re-subscribe, landing straight back on the live question -
    // not the lobby or a stale screen.
    await host.reload();

    // Resume lands on the question phase with the same question text, and NOT on
    // the lobby (the lobby block is x-show toggled, so present-but-hidden when the
    // resumed state read reports the question phase).
    await expect(host.locator('[data-phase-question]')).toBeVisible({ timeout: 20_000 });
    await expect(host.locator('[data-question-text]')).toHaveText(questionText);
    await expect(host.locator('[data-enter-code]')).toBeHidden();

    // The game still completes normally after the host reconnect: both players
    // answer over the API, the runner closes the question and reveals, then the
    // single-round game ends in intermission. The reconnected TV follows every
    // transition off its re-subscribed SSE channel.
    await answerOverApi(robinContext.request, joinCode, correct);
    await answerOverApi(quincyContext.request, joinCode, 'wrong-a');

    // Reveal: the correct option lights on the reconnected TV (the first time
    // correctness is exposed), confirming the resumed screen keeps following
    // phase transitions.
    const correctOption = host.locator('[data-answer-option][data-correct="true"]');
    await expect(correctOption).toHaveCount(1, { timeout: 20_000 });
    await expect(correctOption).toContainText(correct);

    // End of game: the single-round room reaches intermission, the between-games
    // standings screen. The standings bars render on the reconnected host with
    // both players, proving the game ran to completion past the reconnect.
    await expect(host.locator('[data-phase-results]')).toBeVisible({ timeout: 20_000 });
    await expect(standingsRow(host, robin)).toBeVisible({ timeout: 20_000 });
    await expect(standingsRow(host, quincy)).toBeVisible();
  });

  test('the host big screen reloaded on the standings screen resumes the standings', async ({ hostSessions }) => {
    test.setTimeout(90_000);

    const stamp = Date.now();
    const quizTitle = `Host Reconnect Standings ${stamp}`;
    const questionText = 'What is 3+3?';
    const correct = '6';
    const robin = `Robin-${stamp}`;
    const quincy = `Quincy-${stamp}`;

    const host = await hostSessions.adminHost();
    await seedLiveQuiz(host, quizTitle, questionText, correct);

    const { joinCode } = await hostSessions.hostLive(quizTitle);
    expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

    const robinContext = await hostSessions.newPlayerContext();
    const quincyContext = await hostSessions.newPlayerContext();
    await joinAndReady(robinContext.request, joinCode, robin);
    await joinAndReady(quincyContext.request, joinCode, quincy);
    // Present-by-name, not a strict total: one-room-per-host reuse can leave a
    // stale prior-test row in the roster (#957, #1061).
    await expect(playerRow(host, robin)).toBeVisible({ timeout: 15_000 });
    await expect(playerRow(host, quincy)).toBeVisible();

    await host.getByRole('button', { name: 'Start now' }).click();
    await expect(host.locator('[data-phase-question]')).toBeVisible({ timeout: 20_000 });

    // Drive the single-round game to its terminal standings screen: both players
    // answer, the runner closes and reveals, then the room enters intermission -
    // the stable between-games standings screen, a non-question phase.
    await answerOverApi(robinContext.request, joinCode, correct);
    await answerOverApi(quincyContext.request, joinCode, 'wrong-a');
    await expect(host.locator('[data-phase-results]')).toBeVisible({ timeout: 20_000 });
    await expect(standingsRow(host, robin)).toBeVisible({ timeout: 20_000 });
    await expect(standingsRow(host, quincy)).toBeVisible();

    // The drop on a non-question phase: reload the host big screen on the
    // standings screen. The reloaded page must re-GET /state and resume on the
    // standings, not fall back to the lobby.
    await host.reload();

    await expect(host.locator('[data-phase-results]')).toBeVisible({ timeout: 20_000 });
    await expect(standingsRow(host, robin)).toBeVisible({ timeout: 20_000 });
    await expect(standingsRow(host, quincy)).toBeVisible();
    await expect(host.locator('[data-enter-code]')).toBeHidden();
  });
});
