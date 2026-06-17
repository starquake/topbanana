import { test, expect } from './fixtures';
import type { Page } from './fixtures';
import { importQuiz } from './helpers';
import { adminStatePath } from '../e2e-auth';

// #986 slice 2 - deleting a question or a round from the admin quiz view
// swaps the row/section out in place via htmx, with no full-page reload.
// Round delete cascades to the round's questions (FK ON DELETE CASCADE),
// so the whole section vanishes. Each test seeds a uniquely-titled quiz.
test.use({ storageState: adminStatePath() });

async function markNoReload(page: Page): Promise<void> {
  await page.evaluate(() => {
    (window as unknown as { __noReload?: boolean }).__noReload = true;
  });
}

async function sentinelSurvived(page: Page): Promise<boolean> {
  return page.evaluate(() => (window as unknown as { __noReload?: boolean }).__noReload === true);
}

function twoRoundQuiz(title: string) {
  return {
    title,
    description: 'E2E delete-swap quiz',
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

async function openQuiz(page: Page, title: string): Promise<void> {
  await importQuiz(page, twoRoundQuiz(title));
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: title }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  await expect(page.locator('#questions-list')).toBeVisible();
}

// rowIdSuffix reads the numeric id off a testid like "q-row-42" so the
// spec can target the matching delete modal and the swapped-out row.
async function rowIdSuffix(page: Page, prefix: string, text: string): Promise<string> {
  const row = page.locator(`[data-testid^="${prefix}-"]`).filter({ hasText: text }).first();
  const testid = await row.getAttribute('data-testid');
  const id = testid?.slice(`${prefix}-`.length);
  expect(id).toBeTruthy();
  return id as string;
}

test('deleting a question removes its row in place without a reload', async ({ page, browserName }) => {
  await openQuiz(page, `E2E QView QDelete ${browserName} ${Date.now()}`);

  const id = await rowIdSuffix(page, 'q-row', 'Alpha Q2');
  const row = page.getByTestId(`q-row-${id}`);
  await expect(row).toBeVisible();

  await markNoReload(page);

  await row.getByRole('button', { name: 'Delete question' }).click();
  const modal = page.locator(`#modal-delete-question-${id}`);
  await expect(modal).toBeVisible();
  await modal.getByRole('button', { name: 'Delete' }).click();

  await expect(row).toHaveCount(0);
  await expect(modal).toBeHidden();
  // The other questions stay put - only the targeted row left.
  await expect(page.locator('.q-text', { hasText: 'Alpha Q1' })).toBeVisible();
  expect(await sentinelSurvived(page)).toBe(true);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
});

test('deleting a round removes its section and questions in place without a reload', async ({ page, browserName }) => {
  await openQuiz(page, `E2E QView RDelete ${browserName} ${Date.now()}`);

  const id = await rowIdSuffix(page, 'round-section', 'Round Alpha');
  const section = page.getByTestId(`round-section-${id}`);
  await expect(section).toBeVisible();

  await markNoReload(page);

  await section.getByRole('button', { name: 'Delete round' }).click();
  const modal = page.locator(`#modal-delete-round-${id}`);
  await expect(modal).toBeVisible();
  await modal.getByRole('button', { name: 'Delete' }).click();

  await expect(section).toHaveCount(0);
  await expect(modal).toBeHidden();
  // Round Alpha's questions rode along on the cascade; Round Beta stays.
  await expect(page.locator('.q-text', { hasText: 'Alpha Q1' })).toHaveCount(0);
  await expect(page.locator('section.round-section h3', { hasText: 'Round Beta' })).toBeVisible();
  expect(await sentinelSurvived(page)).toBe(true);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
});

test('the no-JS fallback still deletes a question and 303s', async ({ page, browserName }) => {
  await openQuiz(page, `E2E QView NoJS ${browserName} ${Date.now()}`);
  const quizID = page.url().match(/\/admin\/quizzes\/(\d+)/)?.[1] as string;
  expect(quizID).toBeTruthy();

  const id = await rowIdSuffix(page, 'q-row', 'Alpha Q2');

  const html = await (await page.request.get(`/admin/quizzes/${quizID}`)).text();
  const csrf = html.match(/name="csrf_token"\s+value="([^"]*)"/)?.[1]
    ?? html.match(/value="([^"]*)"\s+name="csrf_token"/)?.[1];
  expect(csrf).toBeTruthy();

  const resp = await page.request.post(`/admin/quizzes/${quizID}/questions/${id}/delete`, {
    form: { csrf_token: csrf as string },
    maxRedirects: 0,
  });
  expect(resp.status()).toBe(303);
  expect(resp.headers()['location']).toBe(`/admin/quizzes/${quizID}`);

  await page.reload();
  await expect(page.getByTestId(`q-row-${id}`)).toHaveCount(0);
});
