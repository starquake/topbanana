import { test, expect } from './fixtures';
import { createQuizWithQuestions, type QuestionSpec } from './helpers';
import { adminStatePath } from '../e2e-auth';

test.use({ storageState: adminStatePath() });

// A small valid PNG the upload pipeline can decode + re-encode.
const PNG_SAMPLE = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAHgAAABQCAIAAABd+SbeAAAAzklEQVR4nOzQURGAMADFsHcw4UhHxfqVXBX0bN+z6XZn7wgYHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjfwAAAP//uxwDnKDt4NgAAAAASUVORK5CYII=',
  'base64',
);

// Larger than the default 10 MB image cap (MEDIA_IMAGE_MAX_BYTES), so the client
// guard rejects it before any bytes leave the browser. The content never reaches
// the decoder, so a buffer of zeros with a .png name is enough.
const OVERSIZE_PNG = Buffer.alloc(11 * 1024 * 1024);

const QUESTIONS: readonly QuestionSpec[] = [
  { text: 'Size guard host', options: ['a', 'b', 'c', 'd'], correctIndices: [0] },
];

test('an oversized image is skipped client-side with no network upload', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Upload Size Guard ${browserName}-${Date.now()}`;
  await createQuizWithQuestions(page, quizTitle, QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  // Record every media upload POST that reaches the wire. The oversized file
  // must never appear here: exactly one POST (the valid file) proves the client
  // guard blocked the over-cap file before its XHR opened.
  const mediaPosts: string[] = [];
  page.on('request', (req) => {
    if (req.method() === 'POST' && /\/admin\/quizzes\/\d+\/media$/.test(req.url())) {
      mediaPosts.push(req.url());
    }
  });

  await page.locator('input[type="file"][name="images"]').setInputFiles([
    { name: 'huge.png', mimeType: 'image/png', buffer: OVERSIZE_PNG },
    { name: 'ok.png', mimeType: 'image/png', buffer: PNG_SAMPLE },
  ]);

  // The batch settles to the banner: the valid file landed, the oversized one
  // counts as skipped (the server never saw it).
  const banner = page.getByTestId('upload-banner');
  await expect(banner).toBeVisible({ timeout: 45_000 });
  await expect(banner).toContainText('1 image uploaded');
  await expect(banner).toContainText('1 skipped');
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+#images$/);

  // Only the valid file reached the library, and only the valid file was POSTed.
  await expect(page.getByTestId('library-thumb')).toHaveCount(1);
  expect(mediaPosts).toHaveLength(1);
});
