import { test, expect } from './fixtures';
import { createQuizWithQuestions, type QuestionSpec } from './helpers';
import { adminStatePath } from '../e2e-auth';

// After a host uploads an image on the quiz view, the page should keep them
// near the image library instead of jumping back to the top. The fix is
// server-side: the upload redirect carries an #images fragment that the
// browser scrolls to. This spec asserts the post-upload scroll position lands
// on the library section.
test.use({ storageState: adminStatePath() });

const PNG_SAMPLE = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAHgAAABQCAIAAABd+SbeAAAAzklEQVR4nOzQURGAMADFsHcw4UhHxfqVXBX0bN+z6XZn7wgYHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjRkeMjhgdMTpidMToiNERoyNGR4yOGB0xOmJ0xOiI0RGjI0ZHjI4YHTE6YnTE6IjREaMjfwAAAP//uxwDnKDt4NgAAAAASUVORK5CYII=',
  'base64',
);

// A four-question quiz so the page is tall enough that the images section sits
// well below the viewport top - then a working scroll lands at a positive Y.
const FOUR_QUESTIONS: readonly QuestionSpec[] = [
  { text: 'Q1 long text to push the page height down a bit', options: ['a', 'b', 'c', 'd'], correctIndices: [0] },
  { text: 'Q2 long text to push the page height down a bit', options: ['a', 'b', 'c', 'd'], correctIndices: [1] },
  { text: 'Q3 long text to push the page height down a bit', options: ['a', 'b', 'c', 'd'], correctIndices: [2] },
  { text: 'Q4 long text to push the page height down a bit', options: ['a', 'b', 'c', 'd'], correctIndices: [3] },
];

test('uploading an image keeps the quiz view scrolled to the library section', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Upload Scroll ${browserName}`;
  await createQuizWithQuestions(page, quizTitle, FOUR_QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  // Start at the top so any post-upload scroll comes from the redirect, not
  // from prior interaction state.
  await page.evaluate(() => window.scrollTo(0, 0));
  await expect.poll(async () => page.evaluate(() => window.scrollY)).toBe(0);

  await page.locator('input[type="file"][name="image"]').setInputFiles({
    name: 'pic.png',
    mimeType: 'image/png',
    buffer: PNG_SAMPLE,
  });
  await page.getByRole('button', { name: /upload/i }).click();

  // The redirect lands on the same path with an #images fragment; the browser
  // then scrolls to the library section. Anchor the assertion on both URL and
  // a visible library thumb so we know the upload actually succeeded.
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+(\?uploaded=\d+&failed=\d+)?#images$/);
  await expect(page.locator('img[alt^="Quiz image"]').first()).toBeVisible();

  // The library section is positioned below the page top, so a fragment scroll
  // moves the page down; a regression to the old no-fragment redirect would
  // leave scrollY at 0.
  await expect.poll(async () => page.evaluate(() => window.scrollY)).toBeGreaterThan(0);

  // The images section header should be inside the viewport after the scroll.
  const imagesSection = page.locator('section#images');
  const sectionTop = await imagesSection.evaluate((el) => el.getBoundingClientRect().top);
  const viewportHeight = await page.evaluate(() => window.innerHeight);
  expect(sectionTop).toBeGreaterThanOrEqual(0);
  expect(sectionTop).toBeLessThan(viewportHeight);
});
