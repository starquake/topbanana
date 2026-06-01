import { test, expect } from './fixtures';
import {
  seedQuiz,
  startQuizAsAnonymous,
  answerRemainingQuestions,
  QUIZ_QUESTIONS,
} from './helpers';
import { adminStatePath } from '../e2e-auth';

// Seed the quiz as the shared admin via the JSON importer, then clear the
// admin cookie so the timeout flow runs anonymous.
test.use({ storageState: adminStatePath() });

// PROGRESS_DRAIN_BUDGET_MS is a wall-clock budget for waiting until the
// per-question progress bar reaches 0. Sized to comfortably cover the
// server-side question countdown (currently 10s, see defaultExpiration
// in internal/game/game.go) plus client jitter — but the test does NOT
// assert any specific server-side timeout value; it only needs the
// progress bar to drain before the banner appears. Bumping or lowering
// the server's defaultExpiration only needs this budget to stay larger
// than the new value.
// PROGRESS_DRAIN_BUDGET_MS now also has to cover the reveal-countdown
// (#247) before the per-question progress bar even appears, on top of
// the existing 10s answer window. The e2e config shrinks the reveal
// to 500ms (REVEAL_DELAY env), but the budget stays generous so a
// future config tweak that lengthens it doesn't silently flake here.
const PROGRESS_DRAIN_BUDGET_MS = 20_000;
// SETTLE_MS gates the post-timeout waits (feedback banner appearing,
// next question's button appearing). The next-question path spans the
// 2s feedback pause plus the reveal-countdown (#247, 500ms in e2e via
// REVEAL_DELAY), so 8s gives comfortable headroom.
const SETTLE_MS = 8_000;

// Letting the timer expire without answering takes one full countdown
// plus the 2s feedback pause plus the next question's render time.
// Following questions are answered immediately (~2s feedback each). The
// full countdown plus playthrough keeps this above the Playwright default,
// but with import-based setup a moderate budget suffices.
test('timing out a question shows the Time out banner and the rest of the quiz still plays through', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Timeout Quiz ${browserName}`;

  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();

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

  // The full-screen verdict splash (#253) carries the time-out
  // message — `splash-timeout` is the class GameApp.showSplash
  // applies for the timeout case. It auto-clears at ~950 ms so the
  // assertions below check while it is still on screen.
  const timeoutSplash = page.locator('.splash-timeout');
  await expect(timeoutSplash).toBeVisible({ timeout: SETTLE_MS });
  await expect(timeoutSplash).toContainText('Time out!');

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
  const playerRow = page.locator('table tbody tr[aria-current="true"]');
  await expect(playerRow).toBeVisible();
  await expect(playerRow.locator('td').nth(2)).toHaveText(/^[1-9]\d*$/);
});
