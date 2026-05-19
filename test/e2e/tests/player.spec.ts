import { test, expect } from '@playwright/test';
import { registerAdmin, createQuizWithQuestions, QUIZ_QUESTIONS } from './helpers';

test('admin sets up a multi-question quiz, then a player plays it through to the results screen', async ({ page, browserName }) => {
  // Four questions × ~3s reveal delay (#247) × ~2s/3s feedback + ~10s admin
  // setup + browser overhead push this test well past Playwright's 30s
  // default. Bump explicitly so a slow CI run doesn't trip the timeout on
  // a successful path.
  test.setTimeout(90_000);

  // Per-project unique names so chromium and firefox runs don't collide on the
  // shared server's SQLite file.
  const adminUser = `e2e-admin-player-${browserName}`;
  const quizTitle = `E2E Player Quiz ${browserName}`;

  // ---- Admin setup: register, then create the quiz with all four variants.
  await registerAdmin(page, adminUser);
  await createQuizWithQuestions(page, quizTitle);

  // Log out so the player session is anonymous. The navbar form posts to
  // /logout and the server 303s back to /login.
  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/\/login$/);

  // ---- Player flow: pick the quiz, then walk every question by clicking the
  // first option each time. Predict success/danger feedback per the spec.
  await page.goto('/client/');

  // Alpine fetches the quiz list asynchronously, so wait for our title to
  // appear as a real <option> before selecting it. Selecting by label avoids
  // depending on quiz IDs (the SQLite file accumulates state across specs).
  const select = page.locator('select');
  await expect(select.locator('option', { hasText: quizTitle })).toHaveCount(1);
  await select.selectOption({ label: quizTitle });

  await page.getByRole('button', { name: 'Start Game' }).click();

  // The reveal beat (#247) holds the answer buttons hidden for ~3s
  // after the first question lands. The progress bar carries that
  // phase visually by filling 0 → 100 in cyan (.progress-reveal),
  // then switching to .progress-answer once buttons appear. Asserting
  // the reveal class is on the bar pins the gate to the happy path.
  await expect(page.locator('progress.progress-reveal')).toBeVisible();

  // Walk every question. We always click the first option; whether that picks
  // a correct answer is determined by the spec (correctIndices includes 0).
  let expectedSuccesses = 0;
  const figureImg = page.locator('figure.image img');
  for (const q of QUIZ_QUESTIONS) {
    const choice = q.options[0];
    const wasCorrect = q.correctIndices.includes(0);

    // Wait for the new question to be live before asserting on its image so
    // we don't read state from the previous question's render. The timeout
    // must span the prior question's feedback pause (up to 3s on a wrong
    // pick, #233) plus this question's reveal-countdown (3s, #247) —
    // 10s gives headroom for slow CI.
    const optionButton = page.getByRole('button', { name: choice });
    await expect(optionButton).toBeVisible({ timeout: 10_000 });

    if (q.expectImageVisible === true) {
      await expect(figureImg).toBeVisible();
    } else if (q.expectImageVisible === false) {
      await expect(figureImg).toBeHidden();
    }

    await optionButton.click();

    if (wasCorrect) {
      await expect(page.locator('.feedback-success')).toBeVisible();
      expectedSuccesses++;
    } else {
      await expect(page.locator('.feedback-danger')).toBeVisible();
      // #233 — after a wrong pick the correct option(s) light up so
      // the player can learn what was right before the next question
      // loads. Only assert the reveal on questions that actually have
      // a correct option (the "Which animals are mammals?" fixture
      // entry has correctIndices: [], i.e. no correct answers, so
      // there's nothing for the client to highlight there).
      if (q.correctIndices.length > 0) {
        await expect(page.locator('.btn-answer-correct').first()).toBeVisible();
      }
    }
  }

  // After the auto-advance from the last answer, getNextQuestion() returns
  // 404, the client flips to `finished`, and the results view renders. Give
  // it a generous timeout because each feedback delay (~2s) plus countdown
  // logic adds up over four questions.
  await expect(page.getByRole('heading', { name: 'Game Finished!' })).toBeVisible({ timeout: 15_000 });

  // The leaderboard table renders rank/player/score; the player just played, so
  // their row must be marked with aria-current="true". The score column for that row
  // must not be 0 — Q3 (all correct) and Q4 (idx 0 is prime) both yield a hit
  // when picking the first option, so scoring being broken (always-0) needs
  // to fail the test.
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  const playerRow = page.locator('table tbody tr[aria-current="true"]');
  await expect(playerRow).toBeVisible();
  await expect(playerRow.locator('td').nth(2)).not.toHaveText('0');

  // The off-leaderboard "Your score" standing (#181) must NOT show when
  // the player already has a visible row on the leaderboard — the
  // hasOffLeaderboardStanding gate keys on the absence of an
  // isCurrentPlayer entry. Asserting it is hidden here pins the gate
  // against an always-on regression; the populated-card case is covered
  // by the Go service + handler tests where seeding 11+ rows is cheap.
  await expect(page.locator('.standing-card')).toBeHidden();

  // Lock in the prediction: picking option[0] of every QUIZ_QUESTIONS entry
  // currently hits Q3 (all correct) and Q4 (idx 0 is prime) — exactly 2
  // successes. If a future spec edit shifts that count, this assertion fails
  // loudly so the score-not-zero guard above can't silently degrade.
  expect(expectedSuccesses).toBe(2);

  // Re-visit the start screen. The player has already completed this
  // quiz (#145 enforces one attempt per (player, quiz)), so the
  // leaderboard view takes over with the "Game Finished!" heading and
  // the player's row visible. The lockout banner and the Start button
  // both disappear because the leaderboard view already conveys the
  // "you played this" message; only the quiz picker stays visible so
  // the player can pick a different quiz to play.
  await page.goto('/client/');
  await page.locator('select').selectOption({ label: quizTitle });
  await expect(page.getByRole('heading', { name: 'Game Finished!' })).toBeVisible();
  await expect(page.locator('.player-table')).toBeVisible();
  await expect(page.locator('.feedback-danger')).toBeHidden();
  await expect(page.getByRole('button', { name: 'Start Game' })).toBeHidden();
  // Quiz picker still visible — the player can pick another quiz.
  await expect(page.locator('select')).toBeVisible();
});
