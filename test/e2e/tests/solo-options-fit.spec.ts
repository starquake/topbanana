import type { Page } from '@playwright/test';

import { test, expect } from './fixtures';
import { createQuizWithQuestions, installPlaythroughClock, type QuestionSpec } from './helpers';
import { adminStatePath } from '../e2e-auth';

// An iPhone SE small viewport: 375 logical px wide, and a height that models the
// device after Safari's address bar + toolbar eat into the screen. Headless
// Playwright has no browser chrome, so window.innerHeight == 100svh here - the
// height we set is exactly what the .game-fill column is sized against.
const VIEWPORT = { width: 375, height: 553 } as const;

// A small valid PNG the upload pipeline can decode and re-encode, so the served
// image actually renders (a broken fetch trips the <img> @error handler and
// hides the element, which would mask the layout under test).
const PNG_SAMPLE = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAHgAAABQCAIAAABd+SbeAAAAzklEQVR4nOzQURGAMADFsHcw4UhHxfqVXBX0bN+z6XZn7wgYHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjfwAAAP//uxwDnKDt4NgAAAAASUVORK5CYII=',
  'base64',
);

// A realistic question: four long, single-line option labels plus an attached
// image - the combination that pushed the fourth button below the fold (#954).
const REALISTIC: readonly QuestionSpec[] = [
  {
    text: 'Which planet is shown in this image?',
    options: ['Jupiter the gas giant', 'Saturn with its rings', 'Neptune the ice giant', 'Earth our home world'],
    correctIndices: [0],
  },
];

// authorQuizWithImageQuestion authors the one-question quiz through the admin UI,
// uploads PNG_SAMPLE to the quiz library, and attaches it to the question. Mirrors
// the working flow in question-image.spec. Run at the default desktop viewport -
// the admin UI isn't built for 320px - then resize to play.
async function authorQuizWithImageQuestion(page: Page, quizTitle: string): Promise<void> {
  await createQuizWithQuestions(page, quizTitle, REALISTIC);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await page.locator('input[type="file"][name="image"]').setInputFiles({
    name: 'pic.png',
    mimeType: 'image/png',
    buffer: PNG_SAMPLE,
  });
  await page.getByRole('button', { name: /upload/i }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  const libraryThumb = page.locator('img[alt^="Quiz image"]').first();
  await expect(libraryThumb).toBeVisible();

  await page.getByRole('link', { name: 'Edit question' }).first().click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+\/questions\/\d+\/edit$/);
  const pickerThumb = page
    .getByRole('radiogroup', { name: /attach an image/i })
    .locator('img[alt^="Quiz image"]')
    .first();
  await expect(pickerThumb).toBeVisible();
  await pickerThumb.click();
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.locator('.q-text', { hasText: REALISTIC[0].text })).toBeVisible({ timeout: 15_000 });
}

test.describe('solo gameplay fits the iPhone SE viewport (#954)', () => {
  test.use({ storageState: adminStatePath() });

  test('all four option buttons fit on screen with an attached image', async ({ page, browserName }) => {
    test.setTimeout(60_000);

    const quizTitle = `E2E Solo Fit ${browserName}`;
    await authorQuizWithImageQuestion(page, quizTitle);

    await page.context().clearCookies();
    await page.setViewportSize(VIEWPORT);
    await installPlaythroughClock(page);

    await page.goto('/quizzes');
    await page.getByRole('link', { name: quizTitle }).click();
    await expect(page).toHaveURL(/\/play\//);
    await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
    await page.getByRole('button', { name: 'Start Game' }).click();

    // The reveal beat holds the options hidden for a few seconds; fast-forward
    // it so the question view, image, and option buttons are fully painted.
    await page.clock.runFor(3_500);

    const image = page.getByTestId('question-image');
    await expect(image).toBeVisible();
    await expect.poll(async () => image.evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0);

    const buttons = page.locator('main button[class*="btn-answer"]');
    await expect(buttons).toHaveCount(4);
    await expect(buttons.last()).toBeVisible();

    // Nothing scrolls off the bottom: the document fits the viewport.
    const noOverflow = await page.evaluate(
      () => document.documentElement.scrollHeight <= window.innerHeight + 1,
    );
    expect(noOverflow, 'the gameplay view should not overflow the viewport').toBeTruthy();

    // And the last (fourth) option button's bottom edge sits within the viewport.
    const lastBottom = await buttons.last().evaluate((el) => Math.round(el.getBoundingClientRect().bottom));
    const innerHeight = await page.evaluate(() => window.innerHeight);
    expect(lastBottom, 'the fourth option button should be fully on screen').toBeLessThanOrEqual(innerHeight + 1);
  });
});
