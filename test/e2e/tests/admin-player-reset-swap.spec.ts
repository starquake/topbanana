import { test, expect } from './fixtures';
import type { Page } from './fixtures';
import {
  seedQuiz,
  playThroughQuiz,
  installPlaythroughClock,
} from './helpers';
import { adminStatePath } from '../e2e-auth';

// #986 slice 3 - resetting a player's attempt from the admin quiz view
// swaps their row out of the played-by list in place via htmx, with no
// full-page reload. A reset deletes the player's game on the quiz, so
// they drop off the list entirely. The admin page seeds the quiz and
// drives the reset; a separate anonymous context plays the quiz so a
// real player row exists to reset.
test.use({ storageState: adminStatePath() });

async function markNoReload(page: Page): Promise<void> {
  await page.evaluate(() => {
    (window as unknown as { __noReload?: boolean }).__noReload = true;
  });
}

async function sentinelSurvived(page: Page): Promise<boolean> {
  return page.evaluate(() => (window as unknown as { __noReload?: boolean }).__noReload === true);
}

test('resetting a player removes their row in place without a reload', async ({ page, browser, browserName }) => {
  test.setTimeout(45_000);

  const title = `E2E Reset Quiz ${browserName} ${Date.now()}`;
  await seedQuiz(page, title);

  // A fresh anonymous context plays the quiz so a played-by row exists.
  const playerContext = await browser.newContext();
  try {
    const playerPage = await playerContext.newPage();
    await installPlaythroughClock(playerPage);
    await playThroughQuiz(playerPage, title);
  } finally {
    await playerContext.close();
  }

  // Back on the admin quiz view, the player now shows in the played-by
  // list. Find the quiz id, then the single player row.
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: title }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  const row = page.locator('[data-testid^="player-row-"]').first();
  await expect(row).toBeVisible();
  const testid = await row.getAttribute('data-testid');
  const playerID = testid?.slice('player-row-'.length);
  expect(playerID).toBeTruthy();

  await markNoReload(page);

  await row.getByRole('button', { name: /^Reset/ }).click();
  const modal = page.locator(`#modal-reset-player-${playerID}`);
  await expect(modal).toBeVisible();
  await modal.getByRole('button', { name: 'Reset' }).click();

  await expect(page.getByTestId(`player-row-${playerID}`)).toHaveCount(0);
  await expect(modal).toBeHidden();
  expect(await sentinelSurvived(page)).toBe(true);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
});

test('the no-JS fallback still resets a player and 303s', async ({ page, browser, browserName }) => {
  test.setTimeout(45_000);

  const title = `E2E Reset NoJS ${browserName} ${Date.now()}`;
  await seedQuiz(page, title);

  const playerContext = await browser.newContext();
  try {
    const playerPage = await playerContext.newPage();
    await installPlaythroughClock(playerPage);
    await playThroughQuiz(playerPage, title);
  } finally {
    await playerContext.close();
  }

  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: title }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  const quizID = page.url().match(/\/admin\/quizzes\/(\d+)/)?.[1] as string;
  expect(quizID).toBeTruthy();

  const testid = await page.locator('[data-testid^="player-row-"]').first().getAttribute('data-testid');
  const playerID = testid?.slice('player-row-'.length);
  expect(playerID).toBeTruthy();

  const html = await (await page.request.get(`/admin/quizzes/${quizID}`)).text();
  const csrf = html.match(/name="csrf_token"\s+value="([^"]*)"/)?.[1]
    ?? html.match(/value="([^"]*)"\s+name="csrf_token"/)?.[1];
  expect(csrf).toBeTruthy();

  const resp = await page.request.post(`/admin/quizzes/${quizID}/players/${playerID}/reset`, {
    form: { csrf_token: csrf as string },
    maxRedirects: 0,
  });
  expect(resp.status()).toBe(303);
  expect(resp.headers()['location']).toBe(`/admin/quizzes/${quizID}`);

  await page.reload();
  await expect(page.getByTestId(`player-row-${playerID}`)).toHaveCount(0);
});
