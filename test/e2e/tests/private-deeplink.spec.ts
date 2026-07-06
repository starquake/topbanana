import { test, expect } from './fixtures';
import type { Page } from './fixtures';
import { seedQuiz, setQuizVisibility, publishQuiz, QUIZ_QUESTIONS } from './helpers';
import { adminStatePath } from '../e2e-auth';

// #1214 - a private or unlisted quiz never appears on the public quiz list
// (GET /api/quizzes), so the player client cannot resolve its
// /play/{slug}-{id} deep link from the list alone. It must fall back to
// GET /api/quizzes/{slugID}; without that fallback the start screen showed
// "That quiz isn't available" and rendered no title or leaderboard even though
// the quiz plays fine. Runs signed in as the shared admin: a private quiz is
// readable only by an authenticated player, and the admin owns the seeded quiz.
test.use({ storageState: adminStatePath() });

// deepLinkPathFor opens the admin quiz view (the admin list shows every quiz,
// including private/unlisted) and reads the /play/{slug}-{id} deep link off the
// copy-link chip, so a spec need not reconstruct the server-derived slug.
async function deepLinkPathFor(page: Page, title: string): Promise<string> {
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: title, exact: true }).first().click();
  await page.waitForURL(/\/admin\/quizzes\/\d+$/);
  const path = await page.locator('[data-copy-path]').first().getAttribute('data-copy-path');
  if (!path) throw new Error(`deepLinkPathFor(${title}): no data-copy-path on quiz view`);
  return path;
}

for (const visibility of ['private', 'unlisted'] as const) {
  test(`deep-link to a ${visibility} quiz shows the start screen, not "not available"`, async ({ page, browserName }) => {
    const title = `E2E ${visibility} deeplink ${browserName} ${Date.now()}`;
    // Seed as a draft so the visibility can be set before publishing (a
    // published quiz locks its settings), then flip visibility and publish so
    // the quiz is playable but absent from the public list.
    await seedQuiz(page, title, QUIZ_QUESTIONS, { publish: false });
    setQuizVisibility(title, visibility);
    publishQuiz(title);

    const playPath = await deepLinkPathFor(page, title);

    await page.goto(playPath);

    // The deep-link header resolves via the single-quiz metadata endpoint even
    // though the quiz is absent from the public list.
    const header = page.getByTestId('deep-link-header');
    await expect(header).toBeVisible();
    await expect(header.getByRole('heading', { name: title })).toBeVisible();

    // The "not available" note stays absent, Start Game is offered, and the
    // leaderboard region renders (its empty state for a quiz with no plays).
    await expect(page.getByTestId('deep-link-unavailable')).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Start Game' })).toBeVisible();
    await expect(page.getByTestId('leaderboard-section')).toBeVisible();
  });
}
