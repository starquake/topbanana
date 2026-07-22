import { test, expect } from './fixtures';
import {
  createQuizWithQuestions,
  openQuizOverflow,
  QUIZ_QUESTIONS,
  registerAdmin,
} from './helpers';

// #246 — admin spoiler toggle. Options sit inside a <details> closed by
// default so the admin can present the quiz on screen without exposing
// answers; the summary toggles per question, independent of siblings.
test('admin quiz view hides answer options behind a per-question spoiler toggle', async ({ page, browserName }) => {
  const displayName = `e2e-admin-spoiler-${browserName}`;
  const quizTitle = `E2E Spoiler Quiz ${browserName}`;

  await registerAdmin(page, displayName);
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

test('admin can add, edit, and delete a round on a quiz', async ({ page, browserName }) => {
  // #444 — round CRUD through the admin UI. Questions are grouped into
  // rounds; every quiz starts with a default 'Round 1', so the created
  // round is a second section. Selectors scope to its .round-section to
  // stay unambiguous against the default round.
  const displayName = `e2e-admin-rounds-${browserName}`;
  const quizTitle = `E2E Rounds Quiz ${browserName}`;

  await registerAdmin(page, displayName);
  await createQuizWithQuestions(page, quizTitle);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  // Add a round with a name and a summary.
  await page.getByRole('link', { name: /add round/i }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+\/rounds\/new$/);
  await page.locator('input[name=title]').fill('Picture Round');
  await page.locator('textarea[name=summary]').fill('Welcome, take a breath');
  await page.getByRole('button', { name: 'Save round' }).click();

  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  await expect(page.getByRole('heading', { name: 'Picture Round' })).toBeVisible();
  await expect(page.getByText('Welcome, take a breath')).toBeVisible();

  // The round summary renders above the round's question list (#731). Compare
  // each element's vertical position within the round section.
  const newRound = page.locator('.round-section')
    .filter({ has: page.getByRole('heading', { name: 'Picture Round' }) });
  const summaryBox = await newRound.getByText('Welcome, take a breath').boundingBox();
  const questionsBox = await newRound.locator('[data-question-list]').boundingBox();
  expect(summaryBox).not.toBeNull();
  expect(questionsBox).not.toBeNull();
  expect(summaryBox!.y).toBeLessThan(questionsBox!.y);

  // Edit the round - rename it and change the summary.
  const pictureSection = page.locator('.round-section')
    .filter({ has: page.getByRole('heading', { name: 'Picture Round' }) });
  await pictureSection.getByRole('link', { name: 'Edit round' }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+\/rounds\/\d+\/edit$/);
  await page.locator('input[name=title]').fill('Music Round');
  await page.locator('textarea[name=summary]').fill('Take a deep breath');
  await page.getByRole('button', { name: 'Save round' }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  await expect(page.getByRole('heading', { name: 'Music Round' })).toBeVisible();
  await expect(page.getByText('Take a deep breath')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Picture Round' })).toBeHidden();

  // Delete the round via its per-section modal.
  const musicSection = page.locator('.round-section')
    .filter({ has: page.getByRole('heading', { name: 'Music Round' }) });
  await musicSection.getByRole('button', { name: 'Delete round' }).click();
  // The page also carries a top-level "Delete" quiz button, so scope the
  // confirm click to the open delete-round dialog.
  const deleteRoundDialog = page.getByRole('dialog', { name: 'Delete round' })
    .filter({ visible: true });
  await deleteRoundDialog.getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Music Round' })).toBeHidden();
});

test('register, create a quiz with varied questions, and see them on the quiz view', async ({ page, browserName }) => {
  // Each browser project runs against the same shared server, so use unique
  // names per project. ADMIN_EMAILS (in playwright.config.ts) whitelists
  // these emails so registration promotes them to admin.
  const displayName = `e2e-admin-create-${browserName}`;
  const quizTitle = `E2E Admin Quiz ${browserName}`;

  await registerAdmin(page, displayName);
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
  await openQuizOverflow(page);
  await page.getByRole('button', { name: 'Share' }).click();
  const shareLinkText = await page.locator('.share-link').textContent();
  expect(shareLinkText).toMatch(/\/play\/[a-z0-9-]+-\d+$/);
});
