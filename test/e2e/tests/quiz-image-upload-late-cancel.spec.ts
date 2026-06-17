import { test, expect } from './fixtures';
import { createQuizWithQuestions, type QuestionSpec } from './helpers';
import { adminStatePath } from '../e2e-auth';

// A cancelled upload must leave nothing in the host's library (#992). The
// two-phase commit inserts the media row not-ready, writes the files, records
// the paths, and only then flips it ready; the library list filters not-ready
// rows. So a cancel that arrives in any window -- before the server reads the
// body, mid-process, or even after the row + files commit but before the ready
// flip -- never surfaces a thumbnail, and the not-ready sweep later drops the
// stranded row.
//
// This spec drops the upload connection from the network layer and asserts the
// library still shows no thumbnail after a fresh server-rendered reload.
// Aborting the route is deterministic across chromium and firefox and stands in
// for the cancel the host triggers (xhr.abort) or a dropped connection.
test.use({ storageState: adminStatePath() });

const PNG_SAMPLE = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAHgAAABQCAIAAABd+SbeAAAAzklEQVR4nOzQURGAMADFsHcw4UhHxfqVXBX0bN+z6XZn7wgYHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjfwAAAP//uxwDnKDt4NgAAAAASUVORK5CYII=',
  'base64',
);

const QUESTIONS: readonly QuestionSpec[] = [
  { text: 'Late cancel host', options: ['a', 'b', 'c', 'd'], correctIndices: [0] },
];

test('a cancelled upload leaves nothing in the library', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Late Cancel ${browserName}`;
  await createQuizWithQuestions(page, quizTitle, QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  // The library starts empty.
  await expect(page.getByTestId('library-thumb')).toHaveCount(0);

  // Drop the upload connection at the network layer: the host's XHR sees an
  // aborted request, the same outcome as clicking Cancel or losing the
  // connection mid-upload.
  await page.route('**/admin/quizzes/*/media', (route) => route.abort());

  await page.locator('input[type="file"][name="images"]').setInputFiles({
    name: 'late.png',
    mimeType: 'image/png',
    buffer: PNG_SAMPLE,
  });

  // The upload JS settles the aborted batch and navigates to the server-
  // rendered quiz view, which paints the post-upload banner. The aborted file
  // is reported as skipped, confirming the cancel registered.
  const banner = page.getByTestId('upload-banner');
  await expect(banner).toBeVisible();
  await expect(banner).toContainText('skipped');

  await page.unroute('**/admin/quizzes/*/media');

  // A cancelled upload renders no library thumbnail.
  await expect(page.getByTestId('library-thumb')).toHaveCount(0);
});
