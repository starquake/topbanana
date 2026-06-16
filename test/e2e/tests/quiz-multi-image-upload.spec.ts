import { test, expect } from './fixtures';
import { createQuizWithQuestions, type QuestionSpec } from './helpers';
import { adminStatePath } from '../e2e-auth';

test.use({ storageState: adminStatePath() });

// A small valid PNG so the upload pipeline can decode + re-encode it.
const PNG_SAMPLE = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAHgAAABQCAIAAABd+SbeAAAAzklEQVR4nOzQURGAMADFsHcw4UhHxfqVXBX0bN+z6XZn7wgYHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjfwAAAP//uxwDnKDt4NgAAAAASUVORK5CYII=',
  'base64',
);

const QUESTIONS: readonly QuestionSpec[] = [
  { text: 'Multi upload host', options: ['a', 'b', 'c', 'd'], correctIndices: [0] },
];

test('uploading multiple images at once adds each to the library and shows a confirmation', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Multi Upload ${browserName}`;
  await createQuizWithQuestions(page, quizTitle, QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await page.locator('input[type="file"][name="images"]').setInputFiles([
    { name: 'first.png', mimeType: 'image/png', buffer: PNG_SAMPLE },
    { name: 'second.png', mimeType: 'image/png', buffer: PNG_SAMPLE },
    { name: 'third.png', mimeType: 'image/png', buffer: PNG_SAMPLE },
  ]);

  const banner = page.getByTestId('upload-banner');
  await expect(banner).toBeVisible({ timeout: 45_000 });
  await expect(banner).toContainText('3 images uploaded');
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+#images$/);

  // The library grid now carries three thumbnails.
  await expect(page.getByTestId('library-thumb')).toHaveCount(3);
});

test('a mix of valid and unsupported uploads partial-succeeds and surfaces the skip count', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Partial Upload ${browserName}`;
  await createQuizWithQuestions(page, quizTitle, QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await page.locator('input[type="file"][name="images"]').setInputFiles([
    { name: 'good.png', mimeType: 'image/png', buffer: PNG_SAMPLE },
    { name: 'bad.txt', mimeType: 'text/plain', buffer: Buffer.from('not an image') },
  ]);

  const banner = page.getByTestId('upload-banner');
  await expect(banner).toBeVisible({ timeout: 45_000 });
  await expect(banner).toContainText('1 image uploaded');
  await expect(banner).toContainText('1 skipped');
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+#images$/);

  // The valid file landed; the unsupported file did not.
  await expect(page.getByTestId('library-thumb')).toHaveCount(1);
});
