import { test, expect } from './fixtures';
import type { Page } from './fixtures';
import { seedQuiz } from './helpers';
import { adminStatePath } from '../e2e-auth';

// #986 slice 1 - deleting a quiz from the admin list swaps its card out
// in place via htmx, with no full-page reload. Act as the shared
// migration-seeded admin; each test seeds a uniquely-titled quiz so
// parallel workers never collide.
test.use({ storageState: adminStatePath() });

// markNoReload stamps a sentinel on window that a full-page navigation
// would wipe. After the htmx swap it must survive, proving the action
// did not reload the page.
async function markNoReload(page: Page): Promise<void> {
  await page.evaluate(() => {
    (window as unknown as { __noReload?: boolean }).__noReload = true;
  });
}

async function sentinelSurvived(page: Page): Promise<boolean> {
  return page.evaluate(() => (window as unknown as { __noReload?: boolean }).__noReload === true);
}

test('deleting a quiz removes its card in place without a reload', async ({ page, browserName }) => {
  const title = `E2E Delete Quiz ${browserName} ${Date.now()}`;
  // A published quiz cannot be deleted (#1192), so keep this one a draft.
  await seedQuiz(page, title, undefined, { publish: false });

  await page.goto('/admin/quizzes');
  const card = page.getByRole('link', { name: title }).first();
  await expect(card).toBeVisible();

  // Resolve the quiz id off the card link so we can target its testid.
  const href = await card.getAttribute('href');
  const quizID = href?.match(/\/admin\/quizzes\/(\d+)/)?.[1];
  expect(quizID).toBeTruthy();
  const cardTile = page.getByTestId(`quiz-card-${quizID}`);
  await expect(cardTile).toBeVisible();

  await markNoReload(page);

  // Open the confirm modal and delete. Scope the trigger to this card so
  // a sibling quiz's identical control is never matched.
  await cardTile.getByRole('button', { name: 'Delete quiz' }).click();
  const modal = page.locator(`#modal-delete-quiz-${quizID}`);
  await expect(modal).toBeVisible();
  await modal.getByRole('button', { name: 'Delete' }).click();

  // The card is gone and the modal closed, with the sentinel intact -
  // the swap removed the row without navigating.
  await expect(cardTile).toHaveCount(0);
  await expect(modal).toBeHidden();
  expect(await sentinelSurvived(page)).toBe(true);
  await expect(page).toHaveURL(/\/admin\/quizzes(\?.*)?$/);
});

test('the no-JS fallback still deletes the quiz and 303s', async ({ page }) => {
  const title = `E2E Delete Quiz NoJS ${Date.now()}`;
  // A published quiz cannot be deleted (#1192), so keep this one a draft.
  await seedQuiz(page, title, undefined, { publish: false });

  await page.goto('/admin/quizzes');
  const href = await page.getByRole('link', { name: title }).first().getAttribute('href');
  const quizID = href?.match(/\/admin\/quizzes\/(\d+)/)?.[1];
  expect(quizID).toBeTruthy();

  // POST the plain form (no Hx-Request header) the way a no-JS browser
  // would: the server must delete and 303 back to the list.
  const formResp = await page.request.get('/admin/quizzes');
  const html = await formResp.text();
  const csrf = html.match(/name="csrf_token"\s+value="([^"]*)"/)?.[1]
    ?? html.match(/value="([^"]*)"\s+name="csrf_token"/)?.[1];
  expect(csrf).toBeTruthy();

  const resp = await page.request.post(`/admin/quizzes/${quizID}/delete`, {
    form: { csrf_token: csrf as string },
    maxRedirects: 0,
  });
  expect(resp.status()).toBe(303);
  expect(resp.headers()['location']).toBe('/admin/quizzes');

  // The quiz is gone from the rendered list.
  await page.goto('/admin/quizzes');
  await expect(page.getByRole('link', { name: title })).toHaveCount(0);
});
