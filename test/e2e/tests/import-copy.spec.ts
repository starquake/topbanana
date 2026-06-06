import { test, expect } from './fixtures';
import { adminStatePath } from '../e2e-auth';

// #600 - the quiz-import page has a Copy button that puts the prompt block on
// the clipboard and flips its label to "Copied". We assert the visible DOM
// state, not navigator.clipboard.readText: clipboard-read permission is not
// grantable in Firefox, and the suite runs e2e (firefox) as a required check.
test.describe('quiz import copy button', () => {
  test.use({ storageState: adminStatePath() });

  test('clicking Copy shows the Copied feedback state', async ({ page }) => {
    await page.goto('/admin/quizzes/import');

    const copyButton = page.locator('[data-copy-target="#llm-prompt-text"]');
    await expect(copyButton).toBeVisible();
    await expect(copyButton).toContainText('Copy');

    await copyButton.click();
    await expect(copyButton).toContainText('Copied');
  });

  // #733 - the example JSON now sits in its own copyable code block with a
  // matching Copy button, separate from the prompt block above it.
  test('the example JSON block has its own working Copy button', async ({ page }) => {
    await page.goto('/admin/quizzes/import');

    const exampleBlock = page.locator('#quiz-example-text');
    await expect(exampleBlock).toBeVisible();
    await expect(exampleBlock).toContainText('"European Capitals"');

    const copyButton = page.locator('[data-copy-target="#quiz-example-text"]');
    await expect(copyButton).toBeVisible();
    await expect(copyButton).toContainText('Copy');

    await copyButton.click();
    await expect(copyButton).toContainText('Copied');
  });
});
