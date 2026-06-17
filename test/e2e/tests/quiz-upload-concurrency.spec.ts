import { test, expect } from './fixtures';
import { createQuizWithQuestions, type QuestionSpec } from './helpers';
import { adminStatePath } from '../e2e-auth';

test.use({ storageState: adminStatePath() });

// A small valid PNG so the upload form's picker accepts the file.
const PNG_SAMPLE = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAHgAAABQCAIAAABd+SbeAAAAzklEQVR4nOzQURGAMADFsHcw4UhHxfqVXBX0bN+z6XZn7wgYHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjfwAAAP//uxwDnKDt4NgAAAAASUVORK5CYII=',
  'base64',
);

const QUESTIONS: readonly QuestionSpec[] = [
  { text: 'Concurrency host', options: ['a', 'b', 'c', 'd'], correctIndices: [0] },
];

// The client caps concurrent media POSTs at this value (quiz-image-upload.js,
// MAX_CONCURRENT_UPLOADS); the rest queue and start as slots free. see #988
const MAX_CONCURRENT_UPLOADS = 3;
const PICK_COUNT = 9;

test('the client caps concurrent media uploads at three', async ({ page, browserName }) => {
  test.setTimeout(90_000);

  // Unique per run so a Playwright retry (or a parallel worker) never recreates
  // the same title against the worker DB, which would redirect the save to the
  // quiz list instead of the new quiz view.
  const quizTitle = `E2E Upload Concurrency ${browserName}-${Date.now()}`;
  await createQuizWithQuestions(page, quizTitle, QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  // Hold every upload POST open at a gate the test controls. While the gate is
  // shut the client can keep at most MAX_CONCURRENT_UPLOADS requests open at
  // once (the rest queue), so the in-flight count is a precise, race-free read
  // of the cap - it does not depend on upload-row timing or the post-batch
  // navigation, both of which the earlier row-count assertion raced.
  let inFlight = 0;
  let maxInFlight = 0;
  let completed = 0;
  let openGate = (): void => {};
  const gate = new Promise<void>((resolve) => {
    openGate = resolve;
  });

  await page.route('**/admin/quizzes/*/media', async (route) => {
    if (route.request().method() !== 'POST') {
      await route.continue();
      return;
    }
    inFlight++;
    maxInFlight = Math.max(maxInFlight, inFlight);
    await gate;
    inFlight--;
    completed++;
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ uploaded: [{ filename: 'x.png', id: completed }], failed: [] }),
    });
  });

  const files = Array.from({ length: PICK_COUNT }, (_, i) => ({
    name: `pick-${i}.png`,
    mimeType: 'image/png',
    buffer: PNG_SAMPLE,
  }));
  await page.locator('input[type="file"][name="images"]').setInputFiles(files);

  // The cap saturates at exactly MAX_CONCURRENT_UPLOADS held requests; the other
  // six picked files wait in the client queue and render no row (a row appears
  // only when an upload actually starts).
  await expect.poll(() => inFlight).toBe(MAX_CONCURRENT_UPLOADS);
  await expect(page.getByTestId('upload-row')).toHaveCount(MAX_CONCURRENT_UPLOADS);

  // No surplus request escapes the cap while the gate is shut: give the client a
  // beat and confirm it still holds exactly the cap, never a fourth.
  await page.waitForTimeout(300);
  expect(inFlight).toBe(MAX_CONCURRENT_UPLOADS);

  // Release the gate: the queue drains, every picked file is POSTed exactly once,
  // and the batch settles to the banner. Concurrency never exceeded the cap.
  openGate();
  const banner = page.getByTestId('upload-banner');
  await expect(banner).toBeVisible({ timeout: 60_000 });
  await expect(banner).toContainText(`${PICK_COUNT} images uploaded`);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+#images$/);

  expect(completed).toBe(PICK_COUNT);
  expect(maxInFlight).toBe(MAX_CONCURRENT_UPLOADS);
});
