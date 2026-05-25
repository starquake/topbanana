import { test, expect } from './fixtures';
import { registerAdmin, createQuizWithQuestions } from './helpers';
import type { QuestionSpec } from './helpers';

// #167 slice 2 — the player SPA renders a break card between questions
// and lets the player click Continue to acknowledge it. Two questions
// plus a break between them keeps the play loop short while still
// covering the question -> break -> question transition.
const TWO_QUESTIONS: readonly QuestionSpec[] = [
  // Q1 — option[0] is correct, so the running score after Q1 must be
  // strictly positive on the break card.
  { text: 'What is 2+2?', options: ['4', '3', '5', '6'], correctIndices: [0] },
  // Q2 — option[0] is also correct so the test can finish cleanly.
  { text: 'Capital of France?', options: ['Paris', 'London', 'Madrid', 'Rome'], correctIndices: [0] },
];

test('player sees a break card between questions and continues past it', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const adminUser = `e2e-admin-break-play-${browserName}`;
  const quizTitle = `E2E Break Play ${browserName}`;

  // Admin setup: register, create the quiz, then insert a break
  // between Q1 and Q2 via the admin UI. The Insert-after dropdown is
  // the same "Question 1" option used in admin.spec.ts.
  await registerAdmin(page, adminUser);
  await createQuizWithQuestions(page, quizTitle, TWO_QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await page.getByRole('link', { name: /add break/i }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+\/breaks\/new$/);
  await page.locator(':is(input, textarea)[name=text]').fill('Halfway through!');
  await page.locator('select[name=position]').selectOption({ label: 'Question 1: What is 2+2?' });
  await page.getByRole('button', { name: 'Save break' }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  // Log out so the player session is anonymous.
  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/\/login$/);

  // Player flow: visit the public list, click the quiz card, Start.
  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  await page.getByRole('button', { name: 'Start Game' }).click();

  // Q1 — answer with the correct option so the running score banks
  // some points before the break card lands.
  const q1Option = page.getByRole('button', { name: '4' });
  await expect(q1Option).toBeVisible({ timeout: 10_000 });
  await q1Option.click();
  await expect(page.locator('.splash-correct')).toBeVisible();

  // Break card. The auto-advance from Q1 fires after the feedback
  // pause (~2s on correct, #233) so give the locator a generous
  // budget. The card carries the headline text and a Continue
  // button; the running score must be > 0 (Q1 was a correct pick).
  const breakCard = page.locator('[data-testid="break-card"]');
  await expect(breakCard).toBeVisible({ timeout: 10_000 });
  await expect(breakCard.getByText('Halfway through!')).toBeVisible();
  const breakScore = page.locator('[data-testid="break-score"]');
  await expect(breakScore).toBeVisible();
  // The break-score span carries the running total in its <span>.
  const scoreText = await breakScore.locator('span').textContent();
  const scoreNum = parseInt(scoreText ?? '0', 10);
  expect(scoreNum,
    `running score on break card = ${scoreNum}, want > 0 after correct Q1`,
  ).toBeGreaterThan(0);

  // The progress bar carries the per-question countdown. On a break
  // card there is no countdown — assert the bar is not visible so a
  // future regression that re-uses the question template for breaks
  // fails loudly here. (The bar lives inside the question template's
  // <template x-if="question && !breakItem">, so x-if removes it from
  // the DOM entirely while the break is showing.)
  await expect(page.locator('progress.progress-reveal')).toBeHidden();
  await expect(page.locator('progress.progress-answer')).toBeHidden();

  // Continue past the break.
  await page.locator('[data-testid="break-continue"]').click();
  await expect(breakCard).toBeHidden({ timeout: 10_000 });

  // Q2 — the next item after the break ack. Answer correctly so the
  // game finishes cleanly.
  const q2Option = page.getByRole('button', { name: 'Paris' });
  await expect(q2Option).toBeVisible({ timeout: 10_000 });
  await q2Option.click();
  await expect(page.locator('.splash-correct')).toBeVisible();

  // After the last answer auto-advances, /next returns 404 and the
  // SPA flips to finished. The leaderboard view replaces the
  // gameplay view.
  await expect(page.getByRole('heading', { name: 'Game Finished!' })).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
});
