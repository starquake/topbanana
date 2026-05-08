import { test, expect } from '@playwright/test';

const password = 'correctbatterystaple';

test('admin sets up a quiz, then a player plays it through to the results screen', async ({ page, browserName }) => {
  // Per-project unique names so chromium and firefox runs don't collide on the
  // shared server's SQLite file.
  const adminUser = `e2e-admin-player-${browserName}`;
  const quizTitle = `E2E Player Quiz ${browserName}`;

  // ---- Admin setup: register, create quiz, add one question, mark "4" correct.
  await page.goto('/register');
  await page.locator('input[name=username]').fill(adminUser);
  await page.locator('input[name=password]').fill(password);
  await page.locator('button[type=submit]').click();
  await expect(page).toHaveURL(/\/admin\/quizzes$/);

  await page.goto('/admin/quizzes/new');
  await page.locator('input[name=title]').fill(quizTitle);
  await page.locator('input[name=description]').fill('E2E player-flow quiz');
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await page.getByRole('link', { name: /add question/i }).click();
  await page.locator('input[name=text]').fill('What is 2+2?');
  await page.locator('input[name=position]').fill('1');
  await page.locator('input[name="option[0].text"]').fill('3');
  await page.locator('input[name="option[1].text"]').fill('4');
  await page.locator('input[name="option[2].text"]').fill('5');
  await page.locator('input[name="option[3].text"]').fill('6');
  await page.locator('input[name="option[1].correct"]').check();
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  // Log out so the player session is anonymous. The navbar form posts to
  // /logout and the server 303s back to /login.
  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/\/login$/);

  // ---- Player flow: select the quiz we just created, play one question,
  // wait for auto-advance, and assert we land on the results screen.
  await page.goto('/client/');

  // Alpine fetches the quiz list asynchronously, so wait for our title to
  // appear as a real <option> before selecting it. Selecting by label avoids
  // depending on quiz IDs (the SQLite file accumulates state across specs).
  const select = page.locator('select');
  await expect(select.locator('option', { hasText: quizTitle })).toHaveCount(1);
  await select.selectOption({ label: quizTitle });

  await page.getByRole('button', { name: 'Start Game' }).click();

  // Click the correct answer. The button is rendered inside the .buttons div.
  await page.getByRole('button', { name: '4' }).click();

  // Submitting an answer renders a green notification with feedback for ~2s
  // before the client auto-advances to nextQuestion().
  await expect(page.locator('.notification.is-success')).toBeVisible();

  // After the auto-advance, getNextQuestion() returns 404, the client flips
  // to `finished`, and the results view renders. Give it a generous timeout
  // because the feedback delay (2s) plus countdown logic add up.
  await expect(page.getByRole('heading', { name: 'Game Finished!' })).toBeVisible({ timeout: 10_000 });

  // The results table should have at least one player row, and the score
  // column for that row must not be 0 — we picked the correct answer, so
  // scoring being broken (always-0) needs to fail the test.
  const firstRow = page.locator('table tbody tr').first();
  await expect(firstRow).toBeVisible();
  await expect(firstRow.locator('td').nth(1)).not.toHaveText('0');
});
