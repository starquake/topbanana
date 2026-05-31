import { test, expect } from './fixtures';
import { registerAdmin, createQuizWithQuestions } from './helpers';
import type { QuestionSpec } from './helpers';

// #548 - questions are grouped into rounds, and each round boundary is
// split into two phases: an intro card shown BEFORE the round's first
// question, and a recap card shown AFTER its questions. The quiz's
// default round holds both questions; authoring a summary on it makes
// its intro card carry copy. The test covers the
// intro -> question -> question -> recap -> leaderboard transition over
// the locked /next wire contract.
const TWO_QUESTIONS: readonly QuestionSpec[] = [
  { text: 'What is 2+2?', options: ['4', '3', '5', '6'], correctIndices: [0] },
  { text: 'Capital of France?', options: ['Paris', 'London', 'Madrid', 'Rome'], correctIndices: [0] },
];

test('player sees a round intro then a recap and continues to the leaderboard', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const adminUser = `e2e-admin-round-play-${browserName}`;
  const quizTitle = `E2E Round Play ${browserName}`;

  // Admin setup: register, create the quiz, then author a summary on
  // the default round via the admin UI so its intro card shows copy
  // during play.
  await registerAdmin(page, adminUser);
  await createQuizWithQuestions(page, quizTitle, TWO_QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  // The quiz has exactly one round (the default "Round 1"); .first()
  // resolves the only Edit round link.
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

  // Round intro card shows up BEFORE the first question, carrying the
  // round title and the authored summary copy. Continue acks the intro
  // phase and loads Q1.
  const introCard = page.getByTestId('round-intro-card');
  await expect(introCard).toBeVisible({ timeout: 10_000 });
  await expect(page.getByTestId('round-title')).toContainText('Round 1');
  await expect(page.getByTestId('round-summary')).toContainText('Halfway through!');
  await page.getByTestId('round-continue').click();

  // Q1 - answer the correct option. The generous timeout covers the
  // per-question reveal-countdown beat (#247); the splash assertion
  // gates the next step on the feedback pause completing so the click
  // on Q2 lands in its own answer window.
  const q1Option = page.getByRole('button', { name: '4' });
  await expect(q1Option).toBeVisible({ timeout: 10_000 });
  await q1Option.click();
  await expect(page.locator('.splash-correct')).toBeVisible();

  // Q2 - answer the correct option so the round completes.
  const q2Option = page.getByRole('button', { name: 'Paris' });
  await expect(q2Option).toBeVisible({ timeout: 10_000 });
  await q2Option.click();
  await expect(page.locator('.splash-correct')).toBeVisible();

  // Round recap card shows up after the round's last question
  // auto-advances. It carries the per-round correct count (2 of 2) and
  // a score. Generous timeout for the feedback pause + fetch.
  const recapCard = page.getByTestId('round-recap-card');
  await expect(recapCard).toBeVisible({ timeout: 10_000 });
  await expect(page.getByTestId('round-title')).toContainText('Round 1');
  await expect(page.getByTestId('round-recap-correct')).toContainText('2 of 2 correct');
  await expect(page.getByTestId('round-recap-score')).toBeVisible();
  await expect(page.getByTestId('round-score')).toBeVisible();

  // Continue -> the leaderboard renders (no more questions).
  await page.getByTestId('round-continue').click();
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible({ timeout: 10_000 });
});
