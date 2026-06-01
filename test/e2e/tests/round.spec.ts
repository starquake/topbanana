import { test, expect } from './fixtures';
import type { Page } from './fixtures';
import { importQuiz } from './helpers';
import type { ImportRound } from './helpers';
import { adminStatePath } from '../e2e-auth';

// Seed both quizzes as the shared admin via the JSON importer, then clear
// the admin cookie so the round playthrough runs anonymous.
test.use({ storageState: adminStatePath() });

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
// roundWithSummary builds a single named round titled "Round 1" (matching
// the default round's title) carrying the given summary and two questions.
// Importing one named round replaces the default round, so the played quiz
// has exactly one round whose intro/recap cards carry the summary copy the
// boundary-card assertions key on.
function roundWithSummary(summary: string): ImportRound {
  return {
    title: 'Round 1',
    summary,
    questions: [
      {
        text: 'What is 2+2?',
        options: [
          { text: '4', correct: true },
          { text: '3', correct: false },
          { text: '5', correct: false },
          { text: '6', correct: false },
        ],
      },
      {
        text: 'Capital of France?',
        options: [
          { text: 'Paris', correct: true },
          { text: 'London', correct: false },
          { text: 'Madrid', correct: false },
          { text: 'Rome', correct: false },
        ],
      },
    ],
  };
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
  test.setTimeout(60_000);

  const manualQuiz = `E2E Round Manual ${browserName}`;
  const autoQuiz = `E2E Round Auto ${browserName}`;

  // Manual-skip quiz: default per-question window (long enough that a
  // Continue click beats the auto-advance), with a summary on its single
  // round so the intro card shows copy.
  await importQuiz(page, {
    title: manualQuiz,
    description: 'E2E round manual quiz',
    rounds: [roundWithSummary('Halfway through!')],
  });

  // Auto-advance quiz: a short per-question window shrinks the boundary
  // window so the intro card auto-advances quickly. 2s is long enough
  // to reliably observe the card + its countdown bar before it leaves,
  // yet short enough to keep the suite fast. The round boundary cards
  // only emit when the round carries a non-empty summary (server gate),
  // so this quiz needs a summary too.
  await importQuiz(page, {
    title: autoQuiz,
    description: 'E2E round auto quiz',
    timeLimitSeconds: 2,
    rounds: [roundWithSummary('On your marks!')],
  });

  // Drop the admin cookie so the player session is anonymous.
  await page.context().clearCookies();

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

// The intro and recap cards run an anime.js entrance (#575). runAnim
// no-ops under reduced motion, so the cards' resting CSS must already be
// fully visible — a regression that starts them hidden in CSS and relies
// on anime to fade them in would leave reduced-motion players staring at
// a blank screen. Emulating reduced motion before play and asserting both
// cards plus their figures are visible pins that the from-state is
// anime-driven, not CSS.
test('round boundary cards stay visible under reduced motion', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  await page.emulateMedia({ reducedMotion: 'reduce' });

  const quizTitle = `E2E Round Reduced Motion ${browserName}`;
  await importQuiz(page, {
    title: quizTitle,
    description: 'E2E reduced-motion round quiz',
    rounds: [roundWithSummary('Steady on!')],
  });

  await page.context().clearCookies();
  await startQuiz(page, quizTitle);

  // Intro card and its content are visible with motion disabled.
  const introCard = page.getByTestId('round-intro-card');
  await expect(introCard).toBeVisible({ timeout: 10_000 });
  await expect(introCard).toHaveCSS('opacity', '1');
  await expect(page.getByTestId('round-title')).toContainText('Round 1');
  await expect(page.getByTestId('round-summary')).toContainText('Steady on!');
  await expect(introCard.getByTestId('round-countdown')).toBeVisible();
  await page.getByTestId('round-continue').click();

  const q1Option = page.getByRole('button', { name: '4' });
  await expect(q1Option).toBeVisible({ timeout: 10_000 });
  await q1Option.click();
  await expect(page.locator('.splash-correct')).toBeVisible();

  const q2Option = page.getByRole('button', { name: 'Paris' });
  await expect(q2Option).toBeVisible({ timeout: 10_000 });
  await q2Option.click();
  await expect(page.locator('.splash-correct')).toBeVisible();

  // Recap card and its staggered figures are fully visible with motion
  // disabled — the score, correct/total, and running total all show.
  const recapCard = page.getByTestId('round-recap-card');
  await expect(recapCard).toBeVisible({ timeout: 10_000 });
  await expect(recapCard).toHaveCSS('opacity', '1');
  const recapScore = page.getByTestId('round-recap-score');
  await expect(recapScore).toBeVisible();
  await expect(recapScore).toHaveCSS('opacity', '1');
  await expect(page.getByTestId('round-recap-correct')).toContainText('2 of 2 correct');
  await expect(page.getByTestId('round-recap-correct')).toHaveCSS('opacity', '1');
  await expect(page.getByTestId('round-score')).toBeVisible();
  await expect(page.getByTestId('round-score')).toHaveCSS('opacity', '1');
});
