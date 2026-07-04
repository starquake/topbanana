import { test, expect } from './fixtures';
import type { Page } from './fixtures';
import {
  answerRemainingQuestions,
  installPlaythroughClock,
  QUIZ_QUESTIONS,
  seedQuiz,
} from './helpers';
import { adminStatePath } from '../e2e-auth';

// #1192 - a quiz starts as a draft (editable, host-preview-only) and is
// published through a confirm screen, after which it is locked from edits.
// Act as the shared migration-seeded admin so the seeded quiz is owned by the
// requester (the preview create is owner-gated server-side).
test.use({ storageState: adminStatePath() });

// openQuizView opens the admin quiz view for a title via the list card and
// returns the quiz id read off the view URL.
async function openQuizView(page: Page, title: string): Promise<string> {
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: title }).first().click();
  await page.waitForURL(/\/admin\/quizzes\/\d+$/);
  return /\/admin\/quizzes\/(\d+)/.exec(page.url())![1];
}

test('a host publishes a draft quiz through the confirm screen', async ({ page, browserName }) => {
  const title = `E2E Publish ${browserName} ${Date.now()}`;
  await seedQuiz(page, title, QUIZ_QUESTIONS, { publish: false });
  const quizId = await openQuizView(page, title);

  // Draft state: the status pill and the draft-only controls are present.
  await expect(page.getByTestId('quiz-status')).toHaveText('Draft');
  await expect(page.getByTestId('publish-quiz')).toBeVisible();
  await expect(page.getByTestId('preview-quiz')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Edit quiz' })).toBeVisible();

  // The confirm screen reviews every question with the correct answer marked.
  await page.getByTestId('publish-quiz').click();
  await expect(page).toHaveURL(new RegExp(`/admin/quizzes/${quizId}/publish$`));
  await expect(page.getByTestId('publish-warning')).toBeVisible();
  await expect(page.getByText('What is 2+2?')).toBeVisible();
  await expect(page.locator('li.correct', { hasText: '4' })).toBeVisible();

  // Confirm -> back on the quiz view, now Published and locked from edits.
  await page.getByTestId('publish-confirm').click();
  await page.waitForURL(new RegExp(`/admin/quizzes/${quizId}$`));
  await expect(page.getByTestId('quiz-status')).toHaveText('Published');
  await expect(page.getByTestId('publish-lock-notice')).toBeVisible();

  // The draft-only edit controls are gone; Unpublish is offered instead.
  await expect(page.getByTestId('publish-quiz')).toHaveCount(0);
  await expect(page.getByTestId('preview-quiz')).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Edit quiz' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Add round' })).toHaveCount(0);
  await expect(page.getByTestId('unpublish-quiz')).toBeVisible();
});

test('previewing a draft quiz plays without recording a score', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const title = `E2E Preview ${browserName} ${Date.now()}`;
  await seedQuiz(page, title, QUIZ_QUESTIONS, { publish: false });
  const quizId = await openQuizView(page, title);

  // Install the virtual clock before the SPA boots so the playthrough helper
  // can fast-forward the per-question reveal beat and feedback pause.
  await installPlaythroughClock(page);

  // Launch the preview from the quiz view. The owner stays signed in, so the
  // server-side owner gate on the preview create passes.
  await page.getByTestId('preview-quiz').click();
  await expect(page).toHaveURL(/\/play\/.*\?preview=1$/);

  // The preview chip marks the game as non-scoring.
  await expect(page.getByTestId('preview-banner')).toBeVisible();

  // The whole quiz plays through and finishes on the leaderboard as normal.
  await answerRemainingQuestions(page);

  // The preview recorded no real score: the admin "Played by" list stays empty.
  await page.goto(`/admin/quizzes/${quizId}`);
  await expect(page.getByText('No plays yet')).toBeVisible();
});
