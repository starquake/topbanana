import { test, expect } from './fixtures';
import { seedQuiz, attachQuizImage, installPlaythroughClock, QUIZ_QUESTIONS } from './helpers';
import { adminStatePath } from '../e2e-auth';

// The solo question image sits centered in the flex-1 spacer between the
// question text and the answer buttons. The buttons hide during the pre-answer
// reveal beat (#247); they used to hide with x-show (display:none), which
// collapsed the pad's box and let the spacer grow, dropping the image ~44px and
// jumping it back up when the buttons reappeared. The pad now hides with
// `invisible` (visibility:hidden), keeping its box so the image holds still.
//
// The virtual clock freezes the reveal beat, so `revealing` stays true after the
// question mounts until the test pumps time - a stable window to measure the
// image with the buttons hidden, then again once they appear.

test.use({ storageState: adminStatePath() });

test('the question image does not shift when the answer buttons appear', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  // Date.now() keeps the title unique per attempt so a Playwright retry (which
  // reuses the worker DB) does not 409 on re-import.
  const quizTitle = `E2E Image Shift ${browserName} ${Date.now()}`;
  await seedQuiz(page, quizTitle);
  // Give every question a (broken) image so the image slot renders its
  // placeholder - enough to measure the slot's vertical position.
  attachQuizImage(quizTitle);

  await page.context().clearCookies();
  await installPlaythroughClock(page);

  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await page.getByRole('button', { name: 'Start Game' }).click();

  const imageSlot = page.getByTestId('question-image-placeholder');
  const firstOption = page.getByRole('button', { name: QUIZ_QUESTIONS[0].options[0], exact: true });

  // Reveal beat: the image slot is on screen and the buttons are still hidden.
  await expect(imageSlot).toBeVisible({ timeout: 10_000 });
  await expect(firstOption).toBeHidden();
  const beatBox = await imageSlot.boundingBox();

  // Pump the beat so the answer window opens and the buttons appear.
  await expect(async () => {
    await page.clock.runFor(500);
    await expect(firstOption).toBeVisible({ timeout: 100 });
  }).toPass({ timeout: 10_000 });
  const answerBox = await imageSlot.boundingBox();

  expect(beatBox).not.toBeNull();
  expect(answerBox).not.toBeNull();
  // The image must hold its vertical position when the buttons appear (pre-fix
  // it dropped ~44px during the beat and jumped up here).
  expect(
    Math.abs(answerBox!.y - beatBox!.y),
    `image shifted ${Math.abs(answerBox!.y - beatBox!.y)}px when the answer buttons appeared`,
  ).toBeLessThanOrEqual(1);
});
