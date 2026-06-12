import { test, expect } from './fixtures';
import { seedQuiz, installPlaythroughClock, QUIZ_QUESTIONS } from './helpers';
import { adminStatePath } from '../e2e-auth';

// Seed the quiz as the shared admin (via the JSON importer), then clear
// the admin cookie before each playthrough so the gameplay runs anonymous.
test.use({ storageState: adminStatePath() });

test('admin sets up a multi-question quiz, then a player plays it through to the results screen', async ({ page, browser, browserName }) => {
  // The full anonymous playthrough still spans four questions of feedback
  // and reveal beats; keep a generous budget for slow CI runners. Setup is
  // now a single import request rather than the authoring UI.
  test.setTimeout(60_000);

  // Per-project unique names so chromium and firefox runs don't collide on the
  // shared server's SQLite file.
  const quizTitle = `E2E Player Quiz ${browserName}`;

  // ---- Seed the quiz, then drop the admin cookie so play is anonymous.
  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();
  // Install Playwright's virtual clock before any navigation so the
  // per-question reveal beat (#247) and feedback pause (#233) can be
  // fast-forwarded by page.clock.runFor instead of paying wall time.
  await installPlaythroughClock(page);

  // ---- Player flow: visit /quizzes (the public list, #284), click the
  // quiz card to land on /play/{slug-id}, then walk every question by
  // clicking the first option each time. Predict success/danger
  // feedback per the spec.
  await page.goto('/quizzes');
  await expect(page.getByRole('link', { name: quizTitle })).toBeVisible();
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);

  // #234 — the start screen surfaces the quiz leaderboard before the
  // player clicks Start. On a fresh quiz the empty-state copy is the
  // only thing that should appear under the "Leaderboard" heading.
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  await expect(page.getByText('No finishers yet')).toBeVisible();

  // #308 — the SPA shell's body must use min-h-dvh, not min-h-screen,
  // so mobile browsers can't scroll the URL bar away mid-game. The
  // start-screen content fits comfortably on a desktop viewport;
  // pinning scrollHeight <= innerHeight + 1 here would fail loudly
  // if someone re-introduces min-h-screen on the player-client body.
  const startMeasurement = await page.evaluate(() => ({
    scrollHeight: document.documentElement.scrollHeight,
    innerHeight: window.innerHeight,
  }));
  expect(startMeasurement.scrollHeight,
    `start-screen documentElement.scrollHeight (${startMeasurement.scrollHeight}) > window.innerHeight (${startMeasurement.innerHeight}) — player SPA overflows the viewport on a fresh quiz`,
  ).toBeLessThanOrEqual(startMeasurement.innerHeight + 1);

  await page.getByRole('button', { name: 'Start Game' }).click();

  // The reveal beat (#247) holds the answer buttons hidden for ~3s
  // after the first question lands. The progress bar carries that
  // phase visually by filling 0 → 100 in cyan (.progress-reveal),
  // then switching to .progress-answer once buttons appear. Asserting
  // the reveal class is on the bar pins the gate to the happy path.
  await expect(page.locator('progress.progress-reveal')).toBeVisible();

  // The HUD's Score chip (#253) carries the running total. Its value
  // sits in the second `.hud-chip` (the first is the Q n/total chip),
  // and the digit lives in a `.hud-chip-value` span underneath. Pulled
  // out so the loop below can re-read it after every answer (#234).
  const scoreChipValue = page.locator('.hud-chip', { hasText: 'Score' }).locator('.hud-chip-value');

  // Walk every question. We always click the first option; whether that picks
  // a correct answer is determined by the spec (correctIndices includes 0).
  let expectedSuccesses = 0;
  let prevScore = 0;
  // Per-question image assertions were removed alongside the hidden UI
  // in #426; restore them when the image feature work resumes.
  for (const q of QUIZ_QUESTIONS) {
    const choice = q.options[0];
    const wasCorrect = q.correctIndices.includes(0);

    // Pump virtual time forward in small chunks until the option
    // button shows up enabled. The reveal beat (#247) only ticks
    // under virtual time, and the per-question /next fetch runs in
    // real time, so a single fixed runFor would race the fetch -
    // toPass retries while waiting on both.
    const optionButton = page.getByRole('button', { name: choice });
    await expect(async () => {
      await page.clock.runFor(500);
      await expect(optionButton).toBeVisible({ timeout: 100 });
      await expect(optionButton).toBeEnabled({ timeout: 100 });
    }).toPass({ timeout: 10_000 });

    // #234 — before submitting, the running score chip must still
    // reflect only what's been scored so far. Pinning this here
    // catches a regression where a wrong answer or timeout
    // accidentally adds to the total.
    await expect(scoreChipValue).toHaveText(String(prevScore));

    await optionButton.click();

    if (wasCorrect) {
      await expect(page.getByTestId('reveal-verdict')).toHaveText('Correct!');
      expectedSuccesses++;
      // #234 — after a correct answer, the chip MUST have grown.
      // We don't pin a specific value because CalculateScore depends
      // on submit-time vs StartedAt; "strictly greater than prevScore"
      // is the regression-proof invariant.
      const scoreAfter = await scoreChipValue.textContent();
      const scoreAfterNum = parseInt(scoreAfter ?? '0', 10);
      expect(scoreAfterNum,
        `Score chip after correct pick = ${scoreAfterNum}, want > ${prevScore}`,
      ).toBeGreaterThan(prevScore);
      prevScore = scoreAfterNum;
    } else {
      await expect(page.getByTestId('reveal-verdict')).toHaveText('Not quite');
      // #233 — after a wrong pick the correct option(s) light up so
      // the player can learn what was right before the next question
      // loads. This assertion catches the button-level reveal that
      // stays for the rest of the feedback pause alongside the verdict
      // eyebrow. Only fires on questions that actually have a correct
      // option (the "Which animals are mammals?" fixture has
      // correctIndices: [], i.e. no correct answers, so nothing to
      // highlight there).
      if (q.correctIndices.length > 0) {
        await expect(page.locator('.btn-answer-correct').first()).toBeVisible({ timeout: 2000 });
      }
      // #234 — a wrong pick MUST NOT increase the score; pin the
      // chip to its prior value before the next question loads.
      await expect(scoreChipValue).toHaveText(String(prevScore));
    }

    // Feedback pause (#233): resolveAndAdvance schedules
    // setTimeout(2s correct / 3s wrong) before nextQuestion. runFor
    // fires the setTimeout under virtual time so the next iteration's
    // poll picks up once the new question is wired up.
    await page.clock.runFor(3_500);
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

  // Re-visit via the public list (#284). The player has already
  // completed this quiz (#145 enforces one attempt per (player, quiz)),
  // so the leaderboard view takes over on the deep-link with the
  // "Game Finished!" heading and the player's row visible. The lockout
  // banner and the Start button both disappear because the leaderboard
  // view already conveys the "you played this" message.
  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await expect(page.getByRole('heading', { name: 'Game Finished!' })).toBeVisible();
  await expect(page.locator('.player-table')).toBeVisible();
  // The start-screen lockout banner (uses .feedback-banner.feedback-danger)
  // is hidden on the already-played revisit; this test only asserts the
  // gameplay screen state, so the locator below matches that
  // start-screen instance.
  await expect(page.locator('.feedback-banner.feedback-danger')).toBeHidden();
  await expect(page.getByRole('button', { name: 'Start Game' })).toBeHidden();

  // #234 — a brand-new anonymous visitor (fresh browser context, no
  // cookie carryover) deep-linking into the same quiz must see a
  // populated leaderboard with the previous player's row BEFORE they
  // click Start. This is the headline social-proof case.
  const otherContext = await browser.newContext();
  try {
    const otherPage = await otherContext.newPage();
    await otherPage.goto('/quizzes');
    await otherPage.getByRole('link', { name: quizTitle }).click();
    await expect(otherPage).toHaveURL(/\/play\//);

    // The leaderboard heading + at least one populated row must
    // render BEFORE Start Game is clicked. We don't pin the score —
    // CalculateScore depends on timing — only that the row exists
    // and the empty-state copy is gone.
    await expect(otherPage.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
    await expect(otherPage.getByText('No finishers yet')).toBeHidden();
    await expect(otherPage.locator('.player-table tbody tr')).toHaveCount(1);
    await expect(otherPage.getByRole('button', { name: 'Start Game' })).toBeVisible();
  } finally {
    await otherContext.close();
  }
});

// #284 — the public list page at /quizzes lists every quiz, and
// clicking a card lands on its /play/{slug-id} deep link. Pinning this
// because the SPA's dropdown was retired (the picker now lives at
// /quizzes), so the navigation contract has to stay green.
test('public /quizzes lists every quiz and click navigates to play deep-link', async ({ page, browserName }) => {
  test.setTimeout(30_000);

  const quizTitle = `E2E Public List ${browserName}`;

  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();

  await page.goto('/quizzes');
  const card = page.getByRole('link', { name: quizTitle });
  await expect(card).toBeVisible();
  await card.click();
  await expect(page).toHaveURL(/\/play\/[^/]+-\d+$/);
  await expect(page.getByRole('button', { name: 'Start Game' })).toBeVisible();
});
