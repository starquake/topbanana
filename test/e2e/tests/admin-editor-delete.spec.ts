import { test, expect } from './fixtures';
import type { Page } from './fixtures';
import { importQuiz } from './helpers';
import { adminStatePath } from '../e2e-auth';

// Deleting a question or round from the two-pane editor (#1260). The quiz view
// no longer renders the play sequence, so delete moved here: an hx-confirm
// gates it, and the handler clears the pane and refreshes the rail out of band
// (which rebuilds Sortable - hence the drag check). Round delete cascades to
// its questions via the FK. Was admin-quizview-delete-swap.spec.ts, when delete
// lived on the quiz view (#986).
test.use({ storageState: adminStatePath() });

function twoRoundQuiz(title: string) {
  return {
    title,
    description: 'E2E editor delete quiz',
    rounds: [
      {
        title: 'Round Alpha',
        questions: [
          { text: 'Alpha Q1', options: [{ text: 'a', correct: true }, { text: 'b', correct: false }] },
          { text: 'Alpha Q2', options: [{ text: 'a', correct: true }, { text: 'b', correct: false }] },
        ],
      },
      {
        title: 'Round Beta',
        questions: [
          { text: 'Beta Q1', options: [{ text: 'a', correct: true }, { text: 'b', correct: false }] },
        ],
      },
    ],
  };
}

// openEditor imports a draft two-round quiz and opens its question editor.
async function openEditor(page: Page, title: string): Promise<void> {
  await importQuiz(page, twoRoundQuiz(title), 'solo', { publish: false });
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: title }).click();
  await page.getByTestId('open-question-editor').click();
  await expect(page.locator('#questions-list')).toBeVisible();
}

async function acceptNextConfirm(page: Page): Promise<void> {
  page.once('dialog', (d) => d.accept());
}

const questionTexts = (page: Page) => page.locator('article.q-row .q-text').allTextContents();

test('deleting a question refreshes the rail and clears the pane', async ({ page, browserName }) => {
  await openEditor(page, `E2E Editor QDelete ${browserName} ${Date.now()}`);

  const target = page.locator('article.q-row', { hasText: 'Alpha Q2' });
  await target.click();
  await expect(page.locator('#question-editor textarea[name="text"]')).toHaveValue('Alpha Q2');

  await acceptNextConfirm(page);
  await page.getByTestId('editor-delete-question').click();

  // The row is gone from the rail, the others stay, and the pane resets.
  await expect.poll(async () => (await questionTexts(page)).includes('Alpha Q2')).toBe(false);
  await expect(page.locator('article.q-row', { hasText: 'Alpha Q1' })).toBeVisible();
  await expect(page.getByTestId('question-editor')).toContainText('Pick a question or round');

  // The rail was re-rendered, so Sortable had to rebind: drag still works.
  if (browserName !== 'chromium') {
    const before = await questionTexts(page);
    await page.locator('article.q-row').last().locator('[data-question-handle]')
      .dragTo(page.locator('article.q-row').first(), { force: true });
    await expect.poll(async () => (await questionTexts(page))[0]).toBe(before[before.length - 1]);
  }
});

test('deleting a round takes its questions with it', async ({ page, browserName }) => {
  await openEditor(page, `E2E Editor RDelete ${browserName} ${Date.now()}`);

  // Select Round Alpha's header, which opens the round form in the pane.
  await page.locator('[data-editor-round-row]', { hasText: 'Round Alpha' }).first().click();
  await expect(page.locator('#question-editor input[name="title"]')).toHaveValue('Round Alpha');

  await acceptNextConfirm(page);
  await page.getByTestId('editor-delete-round').click();

  // Round Alpha and its questions are gone; Round Beta remains.
  await expect.poll(async () => page.locator('section.round-section').count()).toBe(1);
  await expect(page.locator('article.q-row', { hasText: 'Alpha Q1' })).toHaveCount(0);
  await expect(page.locator('section.round-section h3', { hasText: 'Round Beta' })).toBeVisible();
  await expect(page.getByTestId('question-editor')).toContainText('Pick a question or round');
});

test('the no-JS fallback still deletes a question and 303s', async ({ page, browserName }) => {
  await openEditor(page, `E2E Editor NoJS ${browserName} ${Date.now()}`);
  const quizID = page.url().match(/\/admin\/quizzes\/(\d+)/)?.[1] as string;
  expect(quizID).toBeTruthy();

  const questionID = await page.locator('article.q-row', { hasText: 'Alpha Q2' })
    .first().getAttribute('data-question-id');
  expect(questionID).toBeTruthy();

  // A plain form post (no HX-Request) still deletes and 303s to the quiz view.
  const html = await (await page.request.get(`/admin/quizzes/${quizID}/questions`)).text();
  const csrf = html.match(/name="csrf_token"\s+value="([^"]*)"/)?.[1]
    ?? html.match(/value="([^"]*)"\s+name="csrf_token"/)?.[1];
  expect(csrf).toBeTruthy();

  const resp = await page.request.post(`/admin/quizzes/${quizID}/questions/${questionID}/delete`, {
    form: { csrf_token: csrf as string },
    maxRedirects: 0,
  });
  expect(resp.status()).toBe(303);
  expect(resp.headers()['location']).toBe(`/admin/quizzes/${quizID}`);

  await page.reload();
  await expect(page.locator('article.q-row', { hasText: 'Alpha Q2' })).toHaveCount(0);
});
