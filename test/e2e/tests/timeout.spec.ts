import { test, expect } from '@playwright/test';
import {
  registerAdmin,
  createQuizWithQuestions,
  startQuizAsAnonymous,
  answerRemainingQuestions,
  QUIZ_QUESTIONS,
} from './helpers';

// PROGRESS_DRAIN_BUDGET_MS is a wall-clock budget for waiting until the
// per-question progress bar reaches 0. Sized to comfortably cover the
// server-side question countdown (currently 10s, see defaultExpiration
// in internal/game/game.go) plus client jitter — but the test does NOT
// assert any specific server-side timeout value; it only needs the
// progress bar to drain before the banner appears. Bumping or lowering
// the server's defaultExpiration only needs this budget to stay larger
// than the new value.
const PROGRESS_DRAIN_BUDGET_MS = 20_000;
const SETTLE_MS = 4_000;

// Letting the timer expire without answering takes one full countdown
// plus the 2s feedback pause plus the next question's render time.
// Following questions are answered immediately (~2s feedback each). The
// setup overhead (registerAdmin + createQuizWithQuestions) pushes this
// well past the Playwright default — bump generously.
test('timing out a question shows the Time out banner and the rest of the quiz still plays through', async ({ page, browserName }) => {
  test.setTimeout(120_000);

  const adminUser = `e2e-admin-timeout-${browserName}`;
  const quizTitle = `E2E Timeout Quiz ${browserName}`;

  await registerAdmin(page, adminUser);
  await createQuizWithQuestions(page, quizTitle);
  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/\/login$/);

  // Start the quiz as an anonymous player.
  await startQuizAsAnonymous(page, quizTitle);

  // First question is on screen; do NOT click any option. Wait for the
  // countdown to expire and the "Time out!" notification to appear.
  const firstQuestion = QUIZ_QUESTIONS[0];
  await expect(page.getByRole('button', { name: firstQuestion.options[0] })).toBeVisible();

  // Progress bar must visibly drain to 0 before the banner appears —
  // this pins the "banner only shows on real expiry, not prematurely"
  // contract. The progress element's `value` attribute is updated by
  // startCountdown on each 100 ms tick.
  await expect(page.locator('progress.progress')).toHaveAttribute(
    'value',
    '0',
    { timeout: PROGRESS_DRAIN_BUDGET_MS },
  );

  const timeoutBanner = page.locator('.notification.is-warning');
  await expect(timeoutBanner).toBeVisible({ timeout: SETTLE_MS });
  await expect(timeoutBanner).toContainText('Time out!');

  // No score line on a timeout — the warning banner shows just the headline.
  await expect(timeoutBanner).not.toContainText('Score:');

  // Answer buttons are gone while feedback is shown; the option that was
  // visible above must not be clickable now. Use the visibility check to
  // confirm the lock-out.
  await expect(page.getByRole('button', { name: firstQuestion.options[0] })).toBeHidden();

  // After the 2s feedback pause, the next question appears. Asserting the
  // second question's first option proves the auto-advance fired.
  const secondQuestion = QUIZ_QUESTIONS[1];
  await expect(page.getByRole('button', { name: secondQuestion.options[0] })).toBeVisible({
    timeout: SETTLE_MS,
  });

  // Play through the remaining questions. The point of this loop is not
  // to verify scoring but to prove the game progresses past the
  // timed-out Q1: a regression where the server refused to advance
  // without an answer row would loop or 5xx inside the helper.
  await answerRemainingQuestions(page, 1);

  // The player's row must be on the leaderboard with a positive numeric
  // score: QUIZ_QUESTIONS[2] (all-correct) and QUIZ_QUESTIONS[3] (idx 0
  // is prime) both score positive when picking option 0. Regex form
  // avoids the stringly comparison to literal '0' so a future number
  // format (e.g. thousands separators) doesn't silently weaken the check.
  const playerRow = page.locator('table tbody tr.is-selected');
  await expect(playerRow).toBeVisible();
  await expect(playerRow.locator('td').nth(2)).toHaveText(/^[1-9]\d*$/);
});
