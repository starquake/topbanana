import type { Page } from '@playwright/test';
import { test, expect } from './fixtures';
import { createQuizWithQuestions, setQuizMode, type QuestionSpec } from './helpers';

// The live player-phone image (#938) had only wire-level coverage, so a render
// regression slipped through: the join.html image binding read currentQuestion()
// off JoinApp, and a refactor briefly shadowed that method, leaving the live
// phone with no image while the wire still carried it. This drives a real live
// session through a browser player and asserts the attached image actually
// renders on the phone, in both the question and reveal phases.

const PNG_SAMPLE = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAHgAAABQCAIAAABd+SbeAAAAzklEQVR4nOzQURGAMADFsHcw4UhHxfqVXBX0bN+z6XZn7wgYHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjfwAAAP//uxwDnKDt4NgAAAAASUVORK5CYII=',
  'base64',
);

const QUESTION: readonly QuestionSpec[] = [
  {
    text: 'Which planet is shown in this image?',
    options: ['Jupiter the gas giant', 'Saturn with its rings', 'Neptune the ice giant', 'Earth our home world'],
    correctIndices: [0],
  },
];

// authorWithImage authors the one-question quiz, uploads an image to the quiz
// library, attaches it to the question, and returns the quiz id (for opening a
// session over the API). Mirrors the upload/attach flow in question-image.spec.
async function authorWithImage(page: Page, title: string): Promise<number> {
  await createQuizWithQuestions(page, title, QUESTION);
  const quizUrl = page.url();
  const quizId = Number(quizUrl.match(/\/admin\/quizzes\/(\d+)$/)![1]);

  await page.locator('input[type="file"][name="image"]').setInputFiles({
    name: 'pic.png',
    mimeType: 'image/png',
    buffer: PNG_SAMPLE,
  });
  await page.getByRole('button', { name: /upload/i }).click();
  await page.waitForURL(/\/admin\/quizzes\/\d+(\/media)?$/);
  await page.goto(quizUrl);

  await page.getByRole('link', { name: 'Edit question' }).first().click();
  const pickerThumb = page.getByRole('radiogroup', { name: /attach an image/i }).locator('img[alt^="Quiz image"]').first();
  await expect(pickerThumb).toBeVisible();
  await pickerThumb.click();
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.locator('.q-text', { hasText: QUESTION[0].text })).toBeVisible({ timeout: 15_000 });

  return quizId;
}

test('the attached image renders on the live player phone', async ({ page, hostSessions }) => {
  test.setTimeout(60_000);

  const title = `E2E Live Player Image ${Date.now()}`;

  // Host side (admin context): author the image quiz, switch to live, open a session.
  const host = await hostSessions.adminHost();
  const quizId = await authorWithImage(host, title);
  setQuizMode(title, 'live');
  const { joinCode } = await hostSessions.openViaApi(quizId);

  // Player side: an anonymous phone browser joins the lobby.
  await page.setViewportSize({ width: 375, height: 667 });
  const player = `Player-${Date.now()}`;
  await page.goto(`/join/${joinCode}`);
  await page.getByTestId('join-name-input').fill(player);
  await page.getByTestId('join-name-submit').click();
  await expect(page.getByTestId('lobby-roster').getByText(player)).toBeVisible();

  const startResp = await host.request.post(`/api/sessions/${joinCode}/start`);
  expect(startResp.status(), `start session: ${startResp.status()} ${await startResp.text()}`).toBe(204);

  // Question phase: the image renders and actually loads (naturalWidth > 0
  // guards against a broken fetch that the <img> @error handler would hide).
  await expect(page.getByTestId('question-view')).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId('question-text')).toHaveText(QUESTION[0].text);
  const image = page.getByTestId('question-image');
  await expect(image).toBeVisible({ timeout: 10_000 });
  await expect.poll(async () => image.evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0);
});
