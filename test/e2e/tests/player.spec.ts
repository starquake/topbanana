import { test, expect } from '@playwright/test';
import { registerAdmin, createQuizWithQuestions, QUIZ_QUESTIONS } from './helpers';

test('admin sets up a multi-question quiz, then a player plays it through to the results screen', async ({ page, browserName }) => {
  // Four questions × ~2s feedback + ~10s admin setup + browser overhead push
  // this test close to Playwright's 30s default. Bump explicitly so a slow CI
  // run doesn't trip the timeout on a successful path.
  test.setTimeout(60_000);

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

  // Walk every question. We always click the first option; whether that picks
  // a correct answer is determined by the spec (correctIndices includes 0).
  let expectedSuccesses = 0;
  const figureImg = page.locator('figure.image img');
  for (const q of QUIZ_QUESTIONS) {
    const choice = q.options[0];
    const wasCorrect = q.correctIndices.includes(0);

    // Wait for the new question to be live before asserting on its image so
    // we don't read state from the previous question's render.
    const optionButton = page.getByRole('button', { name: choice });
    await expect(optionButton).toBeVisible();

    if (q.expectImageVisible === true) {
      await expect(figureImg).toBeVisible();
    } else if (q.expectImageVisible === false) {
      await expect(figureImg).toBeHidden();
    }

    await optionButton.click();

    if (wasCorrect) {
      await expect(page.locator('.notification.is-success')).toBeVisible();
      expectedSuccesses++;
    } else {
      await expect(page.locator('.notification.is-danger')).toBeVisible();
    }
  }

  // After the auto-advance from the last answer, getNextQuestion() returns
  // 404, the client flips to `finished`, and the results view renders. Give
  // it a generous timeout because each feedback delay (~2s) plus countdown
  // logic adds up over four questions.
  await expect(page.getByRole('heading', { name: 'Game Finished!' })).toBeVisible({ timeout: 15_000 });

  // The leaderboard table renders rank/player/score; the player just played, so
  // their row must be marked with .is-selected. The score column for that row
  // must not be 0 — Q3 (all correct) and Q4 (idx 0 is prime) both yield a hit
  // when picking the first option, so scoring being broken (always-0) needs
  // to fail the test.
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  const playerRow = page.locator('table tbody tr.is-selected');
  await expect(playerRow).toBeVisible();
  await expect(playerRow.locator('td').nth(2)).not.toHaveText('0');

  // Lock in the prediction: picking option[0] of every QUIZ_QUESTIONS entry
  // currently hits Q3 (all correct) and Q4 (idx 0 is prime) — exactly 2
  // successes. If a future spec edit shifts that count, this assertion fails
  // loudly so the score-not-zero guard above can't silently degrade.
  expect(expectedSuccesses).toBe(2);
});
