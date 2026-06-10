import { test, expect } from './fixtures';
import type { HostSessions } from './fixtures';
import { importQuiz, setQuizMode, claimAndJoin } from './helpers';
import type { Page } from '@playwright/test';

// confirm-and-restart (#853): a host already running a game on quiz A opens
// quiz B's view and clicks "Host live". Because a game is in flight, the control
// opens a confirm modal ("End the current session and start a new one?"). Confirm
// ends A's session and lands the host on a NEW big screen hosting B; Cancel
// leaves the running game untouched.
//
// The whole host flow is driven through the real browser (admin storageState
// passes the host gate); a single anonymous player joins over the API so the
// host can Start the game and put it in flight. Phase transitions are
// server-driven by the runner.

// seedLiveQuiz seeds a quiz as the shared admin and flips it to mode='live' so it
// is hostable. The per-quiz answer window is set generously (120s) so the game
// stays in the in-flight question phase throughout the host's navigation to the
// other quiz - otherwise the default 10s window could close and advance the room
// past the question (or to finished) mid-test, flipping HostHasRunningGame and
// the phase assertions. The session is ended in cleanup, so the long window
// never slows the test.
async function seedLiveQuiz(host: Page, title: string, questionText: string): Promise<void> {
  await importQuiz(host, {
    title,
    description: 'E2E seeded quiz',
    timeLimitSeconds: 120,
    questions: [
      {
        text: questionText,
        options: [
          { text: 'right', correct: true },
          { text: 'wrong-a', correct: false },
          { text: 'wrong-b', correct: false },
          { text: 'wrong-c', correct: false },
        ],
      },
    ],
  });
  setQuizMode(title, 'live');
}

// hostAndStart opens quiz A's view, hits "Host live" to open a room (tracked by
// the factory for teardown), joins one anonymous player over the API, and starts
// the game so it is in flight. Returns the host page plus the running room's join
// code so the test can assert the restart ends it.
async function hostAndStart(
  hostSessions: HostSessions,
  page: Page,
  quizTitle: string,
): Promise<{ host: Page; code: string }> {
  const { host, joinCode: code } = await hostSessions.hostLive(quizTitle);

  // A single anonymous player joins so the host can start the game.
  await claimAndJoin(page.request, code, `Pat-${Date.now()}`);
  await expect(host.locator('[data-player-row]')).toHaveCount(1);

  // Start now puts the game in flight. Wait for the durable question phase (the
  // round intro is too brief to assert on reliably); with the generous window it
  // stays here while the host navigates to the other quiz, so the room is solidly
  // in flight (HostHasRunningGame = true) when quiz B's view loads.
  await host.getByRole('button', { name: 'Start now' }).click();
  await expect(host.locator('[data-phase-question]')).toBeVisible({ timeout: 20_000 });

  return { host, code };
}

test.describe('confirm-and-restart hosting', () => {
  test('confirm ends the running game and lands the host on a new big screen hosting the other quiz', async ({ page, hostSessions }) => {
    test.setTimeout(90_000);

    const stamp = Date.now();
    const quizA = `Restart-A ${stamp}`;
    const quizB = `Restart-B ${stamp}`;

    const host = await hostSessions.adminHost();
    await seedLiveQuiz(host, quizA, 'What is 1+1?');
    await seedLiveQuiz(host, quizB, 'What is 2+2?');

    // A game is running on quiz A.
    const { code: runningCode } = await hostAndStart(hostSessions, page, quizA);

    // Open quiz B's view. Because a game is in flight, "Host live" opens the
    // confirm-and-restart modal rather than submitting straight away.
    await host.goto('/admin/quizzes');
    await host.getByRole('link', { name: quizB }).click();
    await expect(host).toHaveURL(/\/admin\/quizzes\/\d+$/);
    await host.getByRole('button', { name: 'Host live' }).click();

    const modal = host.getByTestId('restart-modal');
    await expect(modal).toBeVisible();
    await expect(modal).toContainText('A live session is already running');

    // Confirm: end the current session and start a new one hosting quiz B. The
    // host lands on a NEW big screen (a different /host/{code}).
    await modal.getByRole('button', { name: 'End and start' }).click();
    await expect(host).toHaveURL(/\/host\/[A-Z0-9]{6}$/);
    const newCode = host.url().split('/host/')[1];
    expect(newCode).not.toBe(runningCode);
    // The restart created this second room out of band (not via a factory open),
    // so register it for teardown too.
    hostSessions.track(host, newCode);

    // The new room is staged on quiz B: the host can start it (the Start
    // control is present, the empty-room pick link is not).
    await expect(host.getByTestId('start-now')).toBeVisible({ timeout: 15_000 });
    await expect(host.getByTestId('pick-quiz-link')).toBeHidden();
  });

  test('cancel leaves the modal without ending the running game', async ({ page, hostSessions }) => {
    test.setTimeout(90_000);

    const stamp = Date.now();
    const quizA = `Restart-Cancel-A ${stamp}`;
    const quizB = `Restart-Cancel-B ${stamp}`;

    const host = await hostSessions.adminHost();
    await seedLiveQuiz(host, quizA, 'What is 1+1?');
    await seedLiveQuiz(host, quizB, 'What is 2+2?');

    const { code: runningCode } = await hostAndStart(hostSessions, page, quizA);

    // Open quiz B's view and open the restart modal, then Cancel.
    await host.goto('/admin/quizzes');
    await host.getByRole('link', { name: quizB }).click();
    await host.getByRole('button', { name: 'Host live' }).click();
    const modal = host.getByTestId('restart-modal');
    await expect(modal).toBeVisible();
    await modal.getByRole('button', { name: 'Cancel' }).click();
    await expect(modal).toBeHidden();

    // The host is still on quiz B's view (no navigation), and the running game
    // on quiz A is untouched: its big screen still loads in the in-flight
    // question phase (the generous window keeps it there).
    await expect(host).toHaveURL(/\/admin\/quizzes\/\d+$/);
    await host.goto(`/host/${runningCode}`);
    await expect(host.locator('[data-phase-question]')).toBeVisible({ timeout: 20_000 });
  });
});
