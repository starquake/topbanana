import { test, expect } from './fixtures';
import { createQuizWithQuestions, type QuestionSpec } from './helpers';
import { adminStatePath } from '../e2e-auth';

// #950 — clicking a library thumbnail on the admin quiz view opens a modal
// showing the full-resolution image, dismissible via Escape, backdrop click,
// and a close button.
test.use({ storageState: adminStatePath() });

// Same 120x80 PNG the question-image spec uses — small enough to keep the
// upload fast, valid enough to round-trip through the decode pipeline.
const PNG_SAMPLE = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAHgAAABQCAIAAABd+SbeAAAAzklEQVR4nOzQURGAMADFsHcw4UhHxfqVXBX0bN+z6XZn7wgYHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjfwAAAP//uxwDnKDt4NgAAAAASUVORK5CYII=',
  'base64',
);

const SINGLE_QUESTION: readonly QuestionSpec[] = [
  { text: 'Question one', options: ['A', 'B', 'C', 'D'], correctIndices: [0] },
];

test('clicking a library thumbnail opens the full image in a modal', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Image Modal ${browserName}`;
  await createQuizWithQuestions(page, quizTitle, SINGLE_QUESTION);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  // Picking a file fires the auto-upload XHR; on completion the JS reloads
  // the quiz view so the library grid + delete modals reflect the new row.
  await page.locator('input[type="file"][name="images"]').setInputFiles({
    name: 'pic.png',
    mimeType: 'image/png',
    buffer: PNG_SAMPLE,
  });
  await expect(page.getByTestId('library-thumb').first()).toBeVisible({ timeout: 30_000 });

  // The library renders one tile; the alt carries the media id we expect the
  // full-size src to match.
  const libraryThumb = page.getByTestId('library-thumb').first();
  await expect(libraryThumb).toBeVisible();
  const thumbAlt = (await libraryThumb.getAttribute('alt')) ?? '';
  const mediaId = thumbAlt.replace(/^Quiz image /, '');
  expect(mediaId).toMatch(/^\d+$/);

  const modal = page.getByTestId('image-modal');
  const modalImage = modal.locator('img').first();

  // ---- Open: click the thumbnail tile (a <button>, not a link to /media/*).
  await page.getByRole('button', { name: `View image ${mediaId} full size` }).click();
  await expect(modal).toBeVisible();
  // Asserts both that the modal shows the full image (not the thumb endpoint)
  // and that the right tile mapped to its own src.
  await expect(modalImage).toHaveAttribute('src', `/media/${mediaId}`);
  await expect(modalImage).toBeVisible();
  // The image actually loaded - naturalWidth > 0 rules out a broken endpoint.
  await expect
    .poll(async () => modalImage.evaluate((img: HTMLImageElement) => img.naturalWidth))
    .toBeGreaterThan(0);

  // ---- Close on Escape.
  await page.keyboard.press('Escape');
  await expect(modal).toBeHidden();

  // ---- Close on backdrop click. The native <dialog> ::backdrop fills the
  // viewport; a click outside the centred dialog bounding box lands on the
  // backdrop, which the browser routes as a click event with target === dialog.
  await page.getByRole('button', { name: `View image ${mediaId} full size` }).click();
  await expect(modal).toBeVisible();
  const box = await modal.boundingBox();
  if (!box) throw new Error('image modal has no bounding box');
  await page.mouse.click(Math.max(4, box.x / 2), Math.max(4, box.y / 2));
  await expect(modal).toBeHidden();

  // ---- Close via the explicit close button.
  await page.getByRole('button', { name: `View image ${mediaId} full size` }).click();
  await expect(modal).toBeVisible();
  await page.getByRole('button', { name: 'Close image viewer' }).click();
  await expect(modal).toBeHidden();
});
