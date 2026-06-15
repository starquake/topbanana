import { test, expect } from './fixtures';
import { seedQuiz, installPlaythroughClock, QUIZ_QUESTIONS } from './helpers';
import { adminStatePath } from '../e2e-auth';

// The solo client used to render a "Loading question..." fallback for the
// network round trip between feedback pause and the next question landing.
// #982 added pre-fetch during the pause and an atomic swap so the placeholder
// never paints between questions. This spec asserts the fallback's text never
// shows once the first question is on screen.

test.use({ storageState: adminStatePath() });

test('Loading question fallback never paints between solo questions', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E No Flash ${browserName}`;
  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();
  await installPlaythroughClock(page);

  // Pick the quiz, start the game, and wait for Q1's options to mount before
  // installing the probe. The initial /next fetch before Q1 lands is its own
  // window and not in scope here; this spec is about between-question swaps.
  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await page.getByRole('button', { name: 'Start Game' }).click();
  await page.clock.runFor(3_500);
  await expect(page.getByRole('button', { name: QUIZ_QUESTIONS[0].options[0], exact: true })).toBeVisible({ timeout: 10_000 });

  // Install the probe after Q1 is on screen. Tick on every animation frame
  // and record any moment the "Loading question..." fallback paints.
  await page.evaluate(() => {
    const seen: string[] = [];
    (window as unknown as { __loadingFlashSeen: string[] }).__loadingFlashSeen = seen;
    const tick = () => {
      const text = document.body.textContent ?? '';
      if (text.includes('Loading question')) {
        seen.push(new Date().toISOString());
      }
      requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  });

  // Q1 already on screen; answer it and advance through the remaining
  // questions so every transition is observed by the probe.
  for (let i = 0; i < QUIZ_QUESTIONS.length; i++) {
    const q = QUIZ_QUESTIONS[i];
    if (i > 0) {
      await page.clock.runFor(3_500);
      const button = page.getByRole('button', { name: q.options[0], exact: true });
      await expect(button).toBeVisible({ timeout: 10_000 });
    }
    await page.getByRole('button', { name: q.options[0], exact: true }).click();
    // Feedback pause: wrong picks hold longer (3_000ms); pause was 2_000ms
    // for correct. Cover the longer one to be safe.
    await page.clock.runFor(3_500);
  }

  // Finished screen: leaderboard heading visible.
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible({ timeout: 15_000 });

  const flashes = await page.evaluate(
    () => (window as unknown as { __loadingFlashSeen: string[] }).__loadingFlashSeen,
  );
  expect(
    flashes.length,
    `the "Loading question..." fallback painted ${flashes.length} times during the playthrough: ${JSON.stringify(flashes)}`,
  ).toBe(0);
});
