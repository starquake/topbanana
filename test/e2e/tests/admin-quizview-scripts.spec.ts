import { test, expect } from './fixtures';
import type { Page } from './fixtures';
import { importQuiz } from './helpers';
import { adminStatePath } from '../e2e-auth';

test.use({ storageState: adminStatePath() });

// The quiz view's copy pill, share modal and post-upload URL cleanup run from
// bundled ES modules, not inline scripts (#1344).

async function openQuizView(page: Page, title: string): Promise<string> {
  await importQuiz(page, {
    title,
    description: 'E2E quiz view scripts',
    questions: [{ text: 'Q1', options: [{ text: 'a', correct: true }, { text: 'b', correct: false }] }],
  }, 'solo', { publish: false });
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: title }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  return new URL(page.url()).pathname;
}

async function stubClipboard(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const written: string[] = [];
    (window as unknown as { __written: string[] }).__written = written;
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: async (text: string) => { written.push(text); } },
      configurable: true,
    });
  });
}

async function clipboardWrites(page: Page): Promise<string[]> {
  return page.evaluate(() => (window as unknown as { __written: string[] }).__written);
}

test('the copy pill writes the absolute play link', async ({ page, browserName }) => {
  await stubClipboard(page);
  await openQuizView(page, `E2E Quizview Copy ${browserName} ${Date.now()}`);

  const pill = page.locator('[data-copy-path]').first();
  const path = await pill.getAttribute('data-copy-path');
  await pill.click();
  await expect(pill).toHaveText('Copied!');
  expect(await clipboardWrites(page)).toEqual([new URL(page.url()).origin + path]);
});

test('the share modal shows the link, copies it and closes on Escape', async ({ page, browserName }) => {
  await stubClipboard(page);
  await openQuizView(page, `E2E Quizview Share ${browserName} ${Date.now()}`);

  await page.getByRole('button', { name: 'Share', exact: true }).click();
  const modal = page.getByRole('dialog', { name: 'Share quiz' });
  await expect(modal).toBeVisible();
  const origin = new URL(page.url()).origin;
  await expect(modal.locator('.share-link')).toHaveText(new RegExp(`^${origin}/play/`));

  await modal.locator('.share-copy').click();
  await expect(modal.locator('.share-feedback')).toHaveText('Link copied to clipboard.');
  expect((await clipboardWrites(page))[0]).toMatch(new RegExp(`^${origin}/play/`));

  await page.keyboard.press('Escape');
  await expect(modal).toBeHidden();
});

test('the post-upload banner strips its counts from the URL', async ({ page, browserName }) => {
  const path = await openQuizView(page, `E2E Quizview Upload ${browserName} ${Date.now()}`);

  await page.goto(`${path}?uploaded=2&failed=1`);
  await expect(page.getByTestId('upload-banner')).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`${path}$`));
});
