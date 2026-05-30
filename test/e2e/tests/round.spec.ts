import { test, expect } from './fixtures';
import { registerAdmin, createQuizWithQuestions } from './helpers';
import type { QuestionSpec } from './helpers';

// #444 - questions are grouped into rounds. When a round carries an
// authored summary, the player SPA renders a round-summary card once
// every question in the round has been answered, before the next round
// (or the final leaderboard). Two questions in the quiz's default
// round, plus a summary on that round, keeps the play loop short while
// still covering the question -> round-summary -> leaderboard
// transition.
const TWO_QUESTIONS: readonly QuestionSpec[] = [
  { text: 'What is 2+2?', options: ['4', '3', '5', '6'], correctIndices: [0] },
  { text: 'Capital of France?', options: ['Paris', 'London', 'Madrid', 'Rome'], correctIndices: [0] },
];

test('player sees a round-summary card after the round and continues to the leaderboard', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const adminUser = `e2e-admin-round-play-${browserName}`;
  const quizTitle = `E2E Round Play ${browserName}`;

  // Admin setup: register, create the quiz, then author a summary on
  // the default round via the admin UI so its boundary card shows
  // during play. A round with no summary is skipped by the iterator.
  await registerAdmin(page, adminUser);
  await createQuizWithQuestions(page, quizTitle, TWO_QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await page.getByRole('link', { name: 'Edit round' }).first().click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+\/rounds\/\d+\/edit$/);
  await page.locator('textarea[name=summary]').fill('Halfway through!');
  await page.getByRole('button', { name: 'Save round' }).click();
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

  // Q1 - answer the correct option so the running score banks points.
  const q1Option = page.getByRole('button', { name: '4' });
  await expect(q1Option).toBeVisible();
  await q1Option.click();

  // Q2 - answer the correct option so the round completes.
  const q2Option = page.getByRole('button', { name: 'Paris' });
  await expect(q2Option).toBeVisible();
  await q2Option.click();

  // Round-summary card shows up after the round's last question.
  const roundCard = page.getByTestId('round-card');
  await expect(roundCard).toBeVisible();
  await expect(page.getByTestId('round-title')).toContainText('Round 1');
  await expect(page.getByTestId('round-score')).toBeVisible();
  await expect(roundCard).toContainText('Halfway through!');

  // Continue -> the leaderboard renders (no more questions).
  await page.getByTestId('round-continue').click();
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
});
