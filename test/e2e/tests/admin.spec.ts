import { test, expect } from './fixtures';
import { registerAdmin, createQuizWithQuestions, QUIZ_QUESTIONS } from './helpers';

// #246 — admin spoiler toggle. Options sit inside a <details> closed by
// default so the admin can present the quiz on screen without exposing
// answers; the summary toggles per question, independent of siblings.
test('admin quiz view hides answer options behind a per-question spoiler toggle', async ({ page, browserName }) => {
  const username = `e2e-admin-spoiler-${browserName}`;
  const quizTitle = `E2E Spoiler Quiz ${browserName}`;

  await registerAdmin(page, username);
  await createQuizWithQuestions(page, quizTitle);

  // Spoiler is collapsed by default — option text in the DOM is hidden by
  // the native <details> behaviour. Pin the first row's first option so a
  // future regression (spoiler removed, or default flipped to open) trips
  // the assertion.
  const firstRow = page.locator('article.q-row').nth(0);
  const firstOption = firstRow.locator('.q-options li').first();
  await expect(firstOption).toBeHidden();

  // Each row has its own <summary>; click the first one and only the
  // first row's options should reveal. The other rows stay collapsed —
  // the toggle is per-question, not page-wide.
  const firstSummary = firstRow.locator('summary.q-spoiler-summary');
  await expect(firstSummary).toBeVisible();
  await firstSummary.click();
  await expect(firstOption).toBeVisible();

  const secondRow = page.locator('article.q-row').nth(1);
  await expect(secondRow.locator('.q-options li').first()).toBeHidden();

  // Summary label swaps via CSS on [open] — closed shows "Show spoilers",
  // open shows "Hide spoilers". Click again and confirm we collapse.
  await expect(firstSummary).toContainText('Hide spoilers');
  await firstSummary.click();
  await expect(firstOption).toBeHidden();
  await expect(firstSummary).toContainText('Show spoilers');
});

test('register, create a quiz with varied questions, and see them on the quiz view', async ({ page, browserName }) => {
  // Each browser project runs against the same shared server, so use unique
  // names per project. ADMIN_USERNAMES (in playwright.config.ts) whitelists
  // these usernames so registration promotes them to admin.
  const username = `e2e-admin-create-${browserName}`;
  const quizTitle = `E2E Admin Quiz ${browserName}`;

  await registerAdmin(page, username);
  await createQuizWithQuestions(page, quizTitle);

  // After the last addQuestion the quiz view is loaded. The redesign
  // replaced the old questions <table> with a card-style article grid;
  // each question lives inside an <article class="q-row">. Correct
  // options carry the `correct` class on the <li>. We count via the DOM
  // selector (not getByLabel) because the sr-only "Correct" span sits
  // inside a closed <details class="q-spoiler"> (#246) and is therefore
  // excluded from the accessibility tree until the spoiler is opened.
  for (const [index, q] of QUIZ_QUESTIONS.entries()) {
    const row = page.locator('article.q-row').nth(index);
    await expect(row).toContainText(q.text);
    await expect(row.locator('.q-options li.correct')).toHaveCount(q.correctIndices.length);
  }

  // The new quiz title should appear in the admin quiz list. The list is
  // a Tailwind card grid; we key off the link role.
  await page.goto('/admin/quizzes');
  await expect(page.getByRole('link', { name: quizTitle })).toBeVisible();

  // The "Top Banana!" brand in the navbar should be a real link that lands
  // on /admin — guards against regressing back to href="#".
  await page.getByRole('link', { name: 'Top Banana!' }).click();
  await expect(page).toHaveURL(/\/admin$/);

  // Open the share modal on the quiz view and confirm the rendered share
  // URL points at /play/<slug>-<id>. Don't try to verify the clipboard
  // (browser permission model varies); just check the user-visible link.
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  await page.getByRole('button', { name: 'Share' }).click();
  const shareLinkText = await page.locator('.share-link').textContent();
  expect(shareLinkText).toMatch(/\/play\/[a-z0-9-]+-\d+$/);
});
