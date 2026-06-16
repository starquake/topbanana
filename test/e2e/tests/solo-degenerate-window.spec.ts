import { test, expect } from './fixtures';
import { seedQuiz, startQuizAsAnonymous } from './helpers';
import { adminStatePath } from '../e2e-auth';

// Seed the quiz as the shared admin via the JSON importer, then clear the
// admin cookie so the playthrough runs anonymous.
test.use({ storageState: adminStatePath() });

// A solo question payload should never carry a non-positive answer window
// (expiredAt <= startedAt), but clock weirdness, a resumed-expired window, or a
// malformed edge payload could produce one. GameApp.startCountdown keys
// advancement purely on the progress bar draining to zero, so without a guard a
// total <= 0 makes the ratio never cross zero and the countdown spins forever
// with the bar stuck and handleTimeout never firing. The guard resolves a
// degenerate window straight to the timed-out state instead.
//
// This rewrites every /questions/next response so expiredAt == startedAt
// (total = 0) with startedAt already in the past, which the client never
// emits, then asserts the solo surface lands the "Time up" verdict and
// auto-advances rather than freezing on a stuck bar.
test('solo countdown resolves a non-positive answer window instead of spinning', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Degenerate Window ${browserName}`;

  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();

  // Rewrite every /questions/next response: collapse its answer window to zero
  // length with a past startedAt. Fetching the real response first keeps the
  // rest of the payload shape (question, options, ids) intact so only the
  // timestamps are degenerate.
  await page.route('**/api/games/*/questions/next', async (route) => {
    const response = await route.fetch();
    if (response.status() !== 200) {
      await route.fulfill({ response });
      return;
    }
    const body = await response.json();
    const past = new Date(Date.now() - 5_000).toISOString();
    body.startedAt = past;
    body.expiredAt = past;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(body),
    });
  });

  await startQuizAsAnonymous(page, quizTitle);

  // The first question's degenerate window must drive the timed-out verdict
  // promptly rather than leaving the countdown spinning. The reveal beat falls
  // straight through to startCountdown (startedAt is in the past), the guard
  // fires handleTimeout, and the solo surface announces "Time up"
  // (screen-reader-only verdict, #767). Without the guard the countdown would
  // spin forever and this verdict would never appear.
  const verdict = page.getByTestId('reveal-verdict');
  await expect(verdict).toHaveText('Time up', { timeout: 10_000 });

  // The HUD chip ("Q n/total") must advance past the first question. Every
  // question carries the degenerate window, so reaching the second one proves
  // the guarded timeout drives the auto-advance instead of freezing the loop -
  // the exact spin-forever symptom the guard prevents.
  const hud = page.locator('.hud-chip').first();
  await expect(hud).toContainText('Q 2/', { timeout: 15_000 });
});
