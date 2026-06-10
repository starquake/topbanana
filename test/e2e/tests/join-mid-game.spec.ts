import { adminStatePath } from '../e2e-auth';
import { test, expect } from './fixtures';
import { importQuiz, setQuizMode, claimAndJoin, endHostedSession } from './helpers';
import type { Page } from '@playwright/test';

// join-mid-game (#852): the host big screen keeps the join code + URL visible
// while a quiz is running, so a latecomer can still join mid-game. The lobby
// already shows the full QR + code card; once the quiz starts that card is gone,
// so a compact one-line strip ([data-join-hint]) carries the code through the
// in-game phases. The backend already accepts joins in every phase except
// finished (#836), so a brand-new player joining a question-phase room reaches
// the current question and can answer it.
//
// The host setup (seed + make live + open session + start) runs as the shared
// admin in its own context so the big screen is the real host TV; the players
// stay anonymous. Phase transitions are server-driven by the session runner.

// seedLiveQuiz seeds a quiz as the shared admin and flips it to mode='live' so
// it is hostable (the importer creates solo quizzes; setQuizMode flips it). A
// single known-answer question lets the latecomer answer the current question.
//
// The per-quiz answer window is set generously (120s) on purpose: the latecomer
// only starts their cold join AFTER the question is already on the big screen,
// so the default 10s window could close mid-join under CI load before they can
// answer. 120s makes the mid-game-join assertion deterministic without slowing
// the test (the session is ended right after the pick locks in, #852).
async function seedLiveQuiz(host: Page, title: string, questionText: string, correct: string): Promise<void> {
  await importQuiz(host, {
    title,
    description: 'E2E seeded quiz',
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

test.describe('join mid-game', () => {
  test('the big screen keeps the join code visible in-game and a new player joins and answers the current question', async ({ page, baseURL }) => {
    test.setTimeout(90_000);

    const stamp = Date.now();
    const quizTitle = `Join Mid-Game ${stamp}`;
    const questionText = 'What is 1+1?';
    const correct = 'two';
    // Player names are global on players.display_name (#716), so use unique
    // names to avoid colliding with a parallel spec on the worker DB.
    const firstPlayer = `Early-${stamp}`;
    const latecomer = `Late-${stamp}`;

    // Host side: seed the quiz, make it live, and open the big screen at
    // /host/{code} as the admin (storageState) in its own context so the host TV
    // is real and the player pages stay anonymous.
    const hostContext = await page.context().browser()!.newContext({ storageState: adminStatePath(), baseURL });
    const host = await hostContext.newPage();

    // The latecomer joins from its own anonymous context (its own session cookie
    // -> a distinct anonymous player) via the join UI.
    const lateContext = await page.context().browser()!.newContext({ storageState: undefined, baseURL });
    const late = await lateContext.newPage();

    let joinCode = '';
    try {
      await seedLiveQuiz(host, quizTitle, questionText, correct);

      // Open the TV by hosting the seeded live quiz from the admin quiz view.
      await host.goto('/admin/quizzes');
      await host.getByRole('link', { name: quizTitle }).click();
      await expect(host).toHaveURL(/\/admin\/quizzes\/\d+$/);
      await host.getByRole('button', { name: 'Host live' }).click();
      await expect(host).toHaveURL(/\/host\/[A-Z0-9]{6}$/);
      joinCode = host.url().split('/host/')[1];
      expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

      // The lobby keeps the full QR + code card and HIDES the compact mid-game
      // strip - it only appears once the quiz is running.
      await expect(host.locator('[data-join-hint]')).toBeHidden();

      // A first player joins via the API (the default anonymous context, a
      // distinct session/player from the latecomer's lateContext) so the host
      // start has a non-empty roster; the runner then advances round_intro ->
      // question.
      await claimAndJoin(page.request, joinCode, firstPlayer);

      // Host starts the game now. The runner drives round_intro -> question on
      // its own beat; the TV swaps phases off the SSE tick.
      await host.getByRole('button', { name: 'Start now' }).click();

      // The TV reaches the live question phase.
      await expect(host.locator('[data-phase-question]')).toBeVisible({ timeout: 20_000 });
      await expect(host.locator('[data-question-text]')).toHaveText(questionText);

      // Acceptance: the compact join strip is now visible in-game and carries
      // the room code (and the bare join URL), so a latecomer reading the TV can
      // still join.
      const joinHint = host.locator('[data-join-hint]');
      await expect(joinHint).toBeVisible();
      await expect(joinHint).toContainText(joinCode);

      // A brand-new anonymous player joins mid-game via the deep link join UI -
      // not the API - to prove the player surface accepts a mid-game join.
      await late.goto(`/join/${joinCode}`);
      await late.getByTestId('join-name-input').fill(latecomer);
      await late.getByTestId('join-name-submit').click();

      // The latecomer is carried straight into the current question (the room is
      // already in the question phase), reaching the same question text.
      await expect(late.getByTestId('question-view')).toBeVisible({ timeout: 20_000 });
      await expect(late.getByTestId('question-text')).toHaveText(questionText);

      // The answer window is open and the latecomer can answer the current
      // question: the option button is enabled and clickable.
      await expect(late.getByTestId('question-options')).toBeVisible({ timeout: 15_000 });
      const correctButton = late.getByTestId('question-options').getByRole('button', { name: correct });
      await expect(correctButton).toBeEnabled({ timeout: 15_000 });
      await correctButton.click();

      // The pick locks in for the latecomer, confirming a mid-game joiner can
      // answer the current question from the moment they arrive.
      await expect(correctButton).toHaveAttribute('data-picked', 'true', { timeout: 15_000 });
    } finally {
      if (joinCode) await endHostedSession(host, joinCode);
      await lateContext.close();
      await hostContext.close();
    }
  });
});
