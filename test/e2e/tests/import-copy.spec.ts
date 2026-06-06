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

  // #752 - the play-mode selector has no pre-selected option, so the
  // browser blocks the submit until the admin picks solo or live. Pin
  // the no-default state and that picking a mode clears the constraint.
  test('play-mode selector requires an explicit choice before submit', async ({ page }) => {
    await page.goto('/admin/quizzes/import');

    const modeSelect = page.locator('select[name="mode"]');
    await expect(modeSelect).toBeVisible();
    // No mode is pre-selected: the empty placeholder option is current.
    await expect(modeSelect).toHaveValue('');

    // A required control with no value is invalid, so the browser refuses
    // to submit the form.
    const submitButton = page.getByRole('button', { name: 'Import quiz' });
    await submitButton.click();
    await expect(page).toHaveURL(/\/admin\/quizzes\/import$/);
    const invalidBeforeChoice = await modeSelect.evaluate(
      (el) => (el as HTMLSelectElement).validity.valueMissing,
    );
    expect(invalidBeforeChoice).toBe(true);

    // Picking a mode satisfies the constraint.
    await modeSelect.selectOption('live');
    const invalidAfterChoice = await modeSelect.evaluate(
      (el) => (el as HTMLSelectElement).validity.valueMissing,
    );
    expect(invalidAfterChoice).toBe(false);
  });
});
