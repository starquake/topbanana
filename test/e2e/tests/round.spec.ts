import { test, expect } from './fixtures';
import type { Page } from './fixtures';
import { registerAdmin, createQuizWithQuestions } from './helpers';
import type { QuestionSpec } from './helpers';

// #548 - questions are grouped into rounds, and each round boundary is
// split into two phases: an intro card shown BEFORE the round's first
// question, and a recap card shown AFTER its questions. The quiz's
// default round holds both questions; authoring a summary on it makes
// its intro card carry copy. The round boundary now carries a countdown
// window (the quiz's default per-question duration): the card
// auto-advances when it expires, and Continue is the manual skip.
//
// This spec covers both exits over the locked /next wire contract:
//   1. Manual skip - the default-window quiz, clicking Continue through
//      intro -> question -> question -> recap -> leaderboard. The click
//      must advance immediately AND cancel the pending auto-advance
//      timer (the default window is long enough that the click beats
//      it).
//   2. Auto-advance - a second quiz with a 1s per-question time limit
//      so the boundary window is ~1s; the intro card leaves on its own
//      WITHOUT a Continue click. Both run under one admin registration
//      so the auto quiz needs no extra ADMIN_EMAILS entry.
const TWO_QUESTIONS: readonly QuestionSpec[] = [
  { text: 'What is 2+2?', options: ['4', '3', '5', '6'], correctIndices: [0] },
  { text: 'Capital of France?', options: ['Paris', 'London', 'Madrid', 'Rome'], correctIndices: [0] },
];

// authorRoundSummary opens the default round's edit form and fills its
// summary so the intro card shows copy during play. Assumes the page is
// on the quiz view (/admin/quizzes/{id}) and the quiz has exactly one
// round (the default "Round 1").
async function authorRoundSummary(page: Page, summary: string): Promise<void> {
  await page.getByRole('link', { name: 'Edit round' }).first().click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+\/rounds\/\d+\/edit$/);
  await page.locator('textarea[name=summary]').fill(summary);
  await page.getByRole('button', { name: 'Save round' }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
}

// setQuizTimeLimit opens the quiz edit form and sets the per-question
// time limit (seconds). The round-boundary auto-advance window length
// is the quiz's default per-question duration, so a 1s limit gives a
// ~1s boundary window the auto-advance assertion can wait out without
// stretching the suite. Assumes the page is on the quiz view.
async function setQuizTimeLimit(page: Page, seconds: number): Promise<void> {
  const quizUrl = page.url();
  await page.goto(`${quizUrl}/edit`);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+\/edit$/);
  await page.locator('#time_limit_seconds').fill(String(seconds));
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
}

// startQuiz drives the anonymous player from the public list into the
// gameplay shell for the named quiz, stopping right after Start Game.
async function startQuiz(page: Page, quizTitle: string): Promise<void> {
  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  await page.getByRole('button', { name: 'Start Game' }).click();
}

test('round boundary cards auto-advance on their countdown and skip on Continue', async ({ page, browserName }) => {
  test.setTimeout(90_000);

  const adminUser = `e2e-admin-round-play-${browserName}`;
  const manualQuiz = `E2E Round Manual ${browserName}`;
  const autoQuiz = `E2E Round Auto ${browserName}`;

  // One admin registration covers both quizzes, so the auto quiz needs
  // no separate ADMIN_EMAILS entry.
  await registerAdmin(page, adminUser);

  // Manual-skip quiz: default per-question window (long enough that a
  // Continue click beats the auto-advance), with a summary authored on
  // the default round so the intro card shows copy.
  await createQuizWithQuestions(page, manualQuiz, TWO_QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  await authorRoundSummary(page, 'Halfway through!');

  // Auto-advance quiz: a short per-question window shrinks the boundary
  // window so the intro card auto-advances quickly. 2s is long enough
  // to reliably observe the card + its countdown bar before it leaves,
  // yet short enough to keep the suite fast. The round boundary cards
  // only emit when the round carries a non-empty summary (server gate),
  // so this quiz needs a summary authored too.
  await createQuizWithQuestions(page, autoQuiz, TWO_QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  await authorRoundSummary(page, 'On your marks!');
  await setQuizTimeLimit(page, 2);

  // Log out so the player session is anonymous.
  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/\/login$/);

  // --- Manual skip path (default window) ---
  await startQuiz(page, manualQuiz);

  // Round intro card shows up BEFORE the first question, with the round
  // title, the authored summary, and the auto-advance countdown bar.
  // Clicking Continue is the manual skip: it advances immediately and
  // cancels the pending auto-advance timer.
  const introCard = page.getByTestId('round-intro-card');
  await expect(introCard).toBeVisible({ timeout: 10_000 });
  await expect(page.getByTestId('round-title')).toContainText('Round 1');
  await expect(page.getByTestId('round-summary')).toContainText('Halfway through!');
  await expect(introCard.getByTestId('round-countdown')).toBeVisible();
  await page.getByTestId('round-continue').click();

  // Q1 - answer the correct option. The generous timeout covers the
  // per-question reveal-countdown beat (#247); the splash assertion
  // gates the next step on the feedback pause completing.
  const q1Option = page.getByRole('button', { name: '4' });
  await expect(q1Option).toBeVisible({ timeout: 10_000 });
  await q1Option.click();
  await expect(page.locator('.splash-correct')).toBeVisible();

  // Q2 - answer the correct option so the round completes.
  const q2Option = page.getByRole('button', { name: 'Paris' });
  await expect(q2Option).toBeVisible({ timeout: 10_000 });
  await q2Option.click();
  await expect(page.locator('.splash-correct')).toBeVisible();

  // Round recap card shows after the round's last question
  // auto-advances. It carries the per-round correct count (2 of 2), a
  // score, and its own countdown bar.
  const recapCard = page.getByTestId('round-recap-card');
  await expect(recapCard).toBeVisible({ timeout: 10_000 });
  await expect(page.getByTestId('round-title')).toContainText('Round 1');
  await expect(page.getByTestId('round-recap-correct')).toContainText('2 of 2 correct');
  await expect(page.getByTestId('round-recap-score')).toBeVisible();
  await expect(page.getByTestId('round-score')).toBeVisible();
  await expect(recapCard.getByTestId('round-countdown')).toBeVisible();

  // Manual Continue on the recap card -> the leaderboard renders (no
  // more questions). The click must skip ahead without the auto-advance
  // timer double-firing.
  await page.getByTestId('round-continue').click();
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible({ timeout: 10_000 });

  // --- Auto-advance path (1s window) ---
  await startQuiz(page, autoQuiz);

  // The intro card appears with its countdown bar, then leaves on its
  // own once the ~1s window expires - WITHOUT a Continue click. The
  // card going hidden is the proof of auto-advance: nothing else
  // removes it without the player clicking Continue, which this path
  // never does. The seen-ack POST + /next round-trip swaps it out, so a
  // generous timeout covers a slow runner.
  const autoIntro = page.getByTestId('round-intro-card');
  await expect(autoIntro).toBeVisible({ timeout: 10_000 });
  await expect(autoIntro.getByTestId('round-countdown')).toBeVisible();
  await expect(autoIntro).toBeHidden({ timeout: 10_000 });
});
