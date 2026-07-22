import type { Page } from './fixtures';
import { test, expect } from './fixtures';
import { openQuizOverflow, seedQuiz, QUIZ_QUESTIONS } from './helpers';
import { adminStatePath } from '../e2e-auth';

// Layout and stacking coverage for the admin app shell (#1245). Every bug this
// file pins was found by eye on a running server, not by the suite: the context
// bar slid under the global top bar, quiz-card action buttons painted over it,
// and a sticky bar swallowed the drop of a dragged question row. Assertions on
// rendered markup cannot see any of that - it takes a real browser measuring
// real boxes.
test.use({ storageState: adminStatePath() });

// fontsReady waits for the web fonts so measurements run against final metrics;
// the fallback font has different metrics and shifts box positions.
async function fontsReady(page: Page): Promise<void> {
  await page.evaluate(() => document.fonts.ready);
}

async function boxOf(page: Page, selector: string) {
  const box = await page.locator(selector).first().boundingBox();
  expect(box, `${selector} should have a layout box`).toBeTruthy();

  return box!;
}

test('the context bar stays visible below the top bar when the page scrolls', async ({ page, browserName }) => {
  await seedQuiz(page, `E2E Shell Sticky ${browserName} ${Date.now()}`, QUIZ_QUESTIONS);
  // Short viewport so the list genuinely overflows: at the default height the
  // admin list does not scroll and the test would pass without exercising it.
  await page.setViewportSize({ width: 1280, height: 400 });
  await page.goto('/admin/quizzes');
  await fontsReady(page);

  const before = await boxOf(page, '.app-bar');

  await page.evaluate(() => window.scrollTo(0, 400));
  await expect.poll(async () => page.evaluate(() => window.scrollY)).toBeGreaterThan(0);

  const after = await boxOf(page, '.app-bar');
  const topbar = await boxOf(page, 'nav[aria-label="Primary"]');

  // Still on screen, and parked directly beneath the top bar rather than
  // scrolled away or slid underneath it.
  expect(after.y).toBeGreaterThanOrEqual(topbar.y + topbar.height - 1);
  expect(after.y).toBeLessThan(before.y + 400);
});

test('the context bar paints above the quiz card action buttons', async ({ page, browserName }) => {
  await seedQuiz(page, `E2E Shell Stacking ${browserName} ${Date.now()}`, QUIZ_QUESTIONS);
  // Short viewport so the list genuinely overflows and the cards scroll up
  // under the bar - the condition that produced the original bug.
  await page.setViewportSize({ width: 1280, height: 400 });
  await page.goto('/admin/quizzes');
  await fontsReady(page);

  await page.evaluate(() => window.scrollTo(0, 400));
  await expect.poll(async () => page.evaluate(() => window.scrollY)).toBeGreaterThan(0);

  // Probe where the collision actually happens: a card's `relative z-10`
  // action cluster sits on the RIGHT of the card, so sampling the bar's centre
  // never overlaps one and would pass no matter what. Take the cluster's own x
  // and the bar's y, i.e. the point the cluster scrolls through.
  const owner = await page.evaluate(() => {
    const bar = document.querySelector('.app-bar');
    const cluster = document.querySelector('article .relative.z-10');
    if (!bar) return 'no-bar';
    if (!cluster) return 'no-cluster';

    const barRect = bar.getBoundingClientRect();
    const clusterRect = cluster.getBoundingClientRect();
    const x = clusterRect.x + clusterRect.width / 2;
    const y = barRect.y + barRect.height / 2;
    const hit = document.elementFromPoint(x, y);

    if (hit?.closest('.app-bar')) return 'app-bar';

    return hit?.closest('article') ? 'quiz-card' : (hit?.tagName.toLowerCase() ?? 'nothing');
  });

  expect(owner).toBe('app-bar');
});

test('the quiz view overflow menu holds the owner-only actions', async ({ page, browserName }) => {
  const title = `E2E Shell Overflow ${browserName} ${Date.now()}`;
  await seedQuiz(page, title, QUIZ_QUESTIONS, { publish: false });
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: title }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  // Share is in the bar, so it is clickable without opening anything.
  await expect(page.getByRole('button', { name: 'Share' })).toBeVisible();

  // The rest are behind the menu and hidden until it is opened.
  await expect(page.getByTestId('export-quiz')).not.toBeVisible();

  await openQuizOverflow(page);
  await expect(page.getByTestId('export-quiz')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Edit quiz' })).toBeVisible();
  await expect(page.getByTestId('delete-quiz')).toBeVisible();
});
