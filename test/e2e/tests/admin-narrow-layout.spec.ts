import type { Page } from './fixtures';
import { test, expect } from './fixtures';
import { seedQuiz } from './helpers';
import { adminStatePath } from '../e2e-auth';

// Two narrow-width admin layout glitches found during release testing: the
// quiz-card delete button overflowed the card on a phone, and the admin top
// bar scrolled sideways on tablet widths. These specs pin the
// no-horizontal-overflow invariant on /admin/quizzes at a tablet width and
// that an admin card's edit/delete controls stay inside the card and clickable
// on a phone. Acts as the shared migration-seeded admin; each test seeds a
// uniquely-titled quiz so parallel workers never collide. 1px tolerance for
// sub-pixel rounding.
test.use({ storageState: adminStatePath() });

// horizontalOverflow reports whether the document scrolls sideways:
// scrollWidth exceeding clientWidth means content spilled past the viewport.
async function horizontalOverflow(page: Page): Promise<{ scrollWidth: number; clientWidth: number }> {
  return page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
  }));
}

// fontsReady waits for the web fonts to finish loading so layout measurements
// run against final metrics; the fallback font is wider and can transiently
// overflow before Inter/Orbitron land.
async function fontsReady(page: Page): Promise<void> {
  await page.evaluate(() => document.fonts.ready);
}

async function quizCardID(page: Page, title: string): Promise<string> {
  const href = await page.getByRole('link', { name: title }).first().getAttribute('href');
  const id = href?.match(/\/admin\/quizzes\/(\d+)/)?.[1];
  expect(id, 'quiz card link should expose the quiz id').toBeTruthy();
  return id as string;
}

for (const width of [768, 800] as const) {
  test(`admin quizzes does not overflow horizontally at ${width}px`, async ({ page, browserName }) => {
    await seedQuiz(page, `E2E Narrow Topbar ${browserName} ${width} ${Date.now()}`);

    await page.setViewportSize({ width, height: 800 });
    await page.goto('/admin/quizzes');
    // The seeded card proves the list rendered before measuring.
    await expect(page.getByRole('heading', { name: 'Quizzes' })).toBeVisible();
    await fontsReady(page);

    const m = await horizontalOverflow(page);
    expect(
      m.scrollWidth,
      `documentElement.scrollWidth (${m.scrollWidth}) > clientWidth (${m.clientWidth}) - admin quizzes overflows at ${width}px`,
    ).toBeLessThanOrEqual(m.clientWidth + 1);
  });
}

test('admin quiz-card edit and delete buttons stay within the card at 390px', async ({ page, browserName }) => {
  const title = `E2E Narrow Card ${browserName} ${Date.now()}`;
  await seedQuiz(page, title);

  await page.setViewportSize({ width: 390, height: 800 });
  await page.goto('/admin/quizzes');
  const quizID = await quizCardID(page, title);

  const card = page.getByTestId(`quiz-card-${quizID}`);
  const edit = page.getByTestId(`quiz-card-edit-${quizID}`);
  const del = page.getByTestId(`quiz-card-delete-${quizID}`);
  await expect(card).toBeVisible();
  await expect(edit).toBeVisible();
  await expect(del).toBeVisible();
  await fontsReady(page);

  const cardBox = await card.boundingBox();
  expect(cardBox, 'card should have a bounding box').not.toBeNull();

  for (const [name, control] of [['edit', edit], ['delete', del]] as const) {
    const box = await control.boundingBox();
    expect(box, `${name} button should have a bounding box`).not.toBeNull();
    // The card article is overflow-hidden, so a control whose right edge runs
    // past the card right edge is clipped (and, off the viewport, unclickable).
    expect(
      box!.x + box!.width,
      `${name} button right edge (${box!.x + box!.width}) spills past card right edge (${cardBox!.x + cardBox!.width})`,
    ).toBeLessThanOrEqual(cardBox!.x + cardBox!.width + 1);
    expect(box!.x, `${name} button left edge should sit within the card`).toBeGreaterThanOrEqual(cardBox!.x - 1);
  }

  // A trial click on the delete control must land: it opens the confirm modal,
  // proving the button is reachable rather than clipped out of the hit area.
  await del.click({ trial: true });
  await del.click();
  await expect(page.locator(`#modal-delete-quiz-${quizID}`)).toBeVisible();
});
