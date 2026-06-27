import { join } from 'node:path';

import { test, expect } from './fixtures';
import { seedQuiz, execSqlite } from './helpers';
import type { Locator } from '@playwright/test';

// #1117: modals that set aria-modal="true" must contain focus. The shared
// focus-trap helper (frontend/shared/focusTrap.js) moves focus into the dialog
// on open, cycles Tab / Shift+Tab within it, and restores focus to the opener
// on close. These specs exercise both player-client modals end to end.

// makeQuizLive flips a seeded quiz to mode='live' and returns its id, mirroring
// the sqlite3 shortcut the other live specs use.
function makeQuizLive(title: string): number {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; cannot mark a quiz live');
  }
  const dbFile = join(dataDir, `e2e-${test.info().parallelIndex}.db`);
  const escapedTitle = title.replace(/'/g, "''");
  const output = execSqlite(
    dbFile,
    `UPDATE quizzes SET mode = 'live' WHERE title = '${escapedTitle}'; SELECT id FROM quizzes WHERE title = '${escapedTitle}';`,
  );
  const lines = output.split('\n');
  const id = Number.parseInt(lines[lines.length - 1], 10);
  if (!Number.isInteger(id)) {
    throw new Error(`makeQuizLive(${title}): could not resolve quiz id from ${JSON.stringify(output)}`);
  }
  return id;
}

// containsActiveElement reports whether focus currently sits inside the dialog —
// the core promise of aria-modal="true".
function containsActiveElement(dialog: Locator): Promise<boolean> {
  return dialog.evaluate((el) => el.contains(document.activeElement));
}

test('the claim-name modal traps Tab and restores focus to its opener', async ({ page }) => {
  await page.goto('/client/');

  // The start-screen "Set your name" button is the opener; focus must return
  // here after the modal closes.
  const opener = page.getByRole('button', { name: 'Set your name' });
  await opener.click();

  const modal = page.locator('[role="dialog"]');
  await expect(modal).toBeVisible();

  // On open the helper lands focus on the [data-autofocus] name input rather
  // than the first focusable (the header close button).
  const input = modal.locator('input#claim-name-modal');
  await expect(input).toBeFocused();

  const closeButton = modal.getByRole('button', { name: 'close' });
  const saveButton = modal.getByRole('button', { name: 'Save' });

  // Tab past the last control wraps back to the first, staying inside.
  await saveButton.focus();
  await page.keyboard.press('Tab');
  await expect(closeButton).toBeFocused();
  expect(await containsActiveElement(modal)).toBe(true);

  // Shift+Tab past the first control wraps to the last, staying inside.
  await closeButton.focus();
  await page.keyboard.press('Shift+Tab');
  await expect(saveButton).toBeFocused();
  expect(await containsActiveElement(modal)).toBe(true);

  // Closing returns focus to the opener.
  await modal.getByRole('button', { name: 'Cancel' }).click();
  await expect(modal).toBeHidden();
  await expect(opener).toBeFocused();
});

test('the exit-session modal traps Tab and restores focus to its opener', async ({ page, hostSessions }) => {
  const quizTitle = `Focus Trap Exit ${Date.now()}`;
  const player = `Trap-${Date.now()}`;

  const host = await hostSessions.adminHost();
  await seedQuiz(host, quizTitle);
  const quizID = makeQuizLive(quizTitle);
  const { joinCode } = await hostSessions.openViaApi(quizID);

  await page.goto(`/join/${joinCode}`);
  await page.getByTestId('join-name-input').fill(player);
  await page.getByTestId('join-name-submit').click();
  await expect(page.getByTestId('lobby-view')).toBeVisible();

  const opener = page.getByTestId('exit-session-open');
  await opener.click();

  const modal = page.getByTestId('exit-session-modal');
  await expect(modal).toBeVisible();

  // On open the helper lands focus on the [data-autofocus] Cancel button — the
  // safe default for a destructive confirm.
  const cancelButton = modal.getByRole('button', { name: 'Cancel' });
  await expect(cancelButton).toBeFocused();

  const closeButton = modal.getByRole('button', { name: 'close' });
  const confirmButton = modal.getByTestId('exit-session-confirm');

  // Tab past the last control wraps back to the first, staying inside.
  await confirmButton.focus();
  await page.keyboard.press('Tab');
  await expect(closeButton).toBeFocused();
  expect(await containsActiveElement(modal)).toBe(true);

  // Shift+Tab past the first control wraps to the last, staying inside.
  await closeButton.focus();
  await page.keyboard.press('Shift+Tab');
  await expect(confirmButton).toBeFocused();
  expect(await containsActiveElement(modal)).toBe(true);

  // Closing returns focus to the opener.
  await cancelButton.click();
  await expect(modal).toBeHidden();
  await expect(opener).toBeFocused();
});
