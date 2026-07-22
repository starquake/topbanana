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

// The editor's save is an out-of-band row swap (#1244 slice 2): the form comes
// back for the pane and the rail row follows, deliberately WITHOUT re-rendering
// #questions-list. quiz-reorder.js destroys and rebuilds its SortableJS
// instances on every list swap, so this sequence - select, edit, save, then
// drag - is the only thing that catches a stale binding left behind.
test('editing and saving in the pane leaves drag reorder working', async ({ page, browserName }) => {
  test.skip(browserName === 'chromium', 'native DnD is not scriptable in Chromium under Playwright; covered on Firefox');

  const title = `E2E Editor Save ${browserName} ${Date.now()}`;
  await seedQuiz(page, title, QUIZ_QUESTIONS, { publish: false });
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: title }).click();
  await page.getByTestId('open-question-editor').click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+\/questions$/);

  const rows = page.locator('article.q-row');
  const texts = async () => rows.locator('.q-text').allTextContents();
  const before = await texts();
  expect(before.length).toBeGreaterThan(1);

  // Select the first question; the pane fills with its form.
  await rows.first().click();
  const questionText = page.locator('#question-editor textarea[name="text"]');
  await expect(questionText).toBeVisible();

  const edited = `Edited in the pane ${Date.now()}`;
  await questionText.fill(edited);
  await page.locator('#question-editor button[type="submit"]').first().click();

  // The rail row picks up the new text via the out-of-band swap, with no reload.
  await expect.poll(async () => (await texts())[0]).toContain(edited);

  // Now drag: if the OOB swap left Sortable bound to a detached row, this
  // silently does nothing.
  await rows.last().locator('[data-question-handle]').dragTo(rows.first(), { force: true });
  await expect.poll(async () => (await texts())[0]).toBe(before[before.length - 1]);
});

// Keyboard, selection, and dirty state in the editor (#1244 slice 3). These
// only exist in a browser: the module is delegated off htmx swaps, so nothing
// server-side can tell you whether ArrowDown actually moved the selection.
test('the editor supports keyboard selection, dirty state, and save', async ({ page, browserName }) => {
  const title = `E2E Editor Keys ${browserName} ${Date.now()}`;
  await seedQuiz(page, title, QUIZ_QUESTIONS, { publish: false });
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: title }).click();
  await page.getByTestId('open-question-editor').click();
  await expect(page).toHaveURL(/\/questions$/);

  const rows = page.locator('article.q-row');
  const selected = page.locator('article.q-row[aria-current="true"]');
  const saveState = page.getByTestId('editor-savestate');

  await expect(saveState).toHaveText('All changes saved');

  // Clicking selects, and the rail marks it.
  await rows.first().click();
  await expect(selected).toHaveCount(1);
  await expect(page.locator('#question-editor textarea[name="text"]')).toBeVisible();

  // ArrowDown moves the selection to the next question.
  const firstText = await selected.locator('.q-text').textContent();
  await page.locator('body').click({ position: { x: 5, y: 5 } });
  await page.keyboard.press('ArrowDown');
  await expect.poll(async () => selected.locator('.q-text').textContent()).not.toBe(firstText);

  // Editing marks the pane dirty; saving with Ctrl+S clears it.
  const textarea = page.locator('#question-editor textarea[name="text"]');
  await expect(textarea).toBeVisible();
  const edited = `Keyboard edit ${Date.now()}`;
  await textarea.fill(edited);
  await expect(saveState).toHaveText('Unsaved changes');

  await page.keyboard.press('Control+s');
  await expect(saveState).toHaveText('All changes saved');
  // The saved text reached the rail via the out-of-band row swap.
  await expect(page.locator('article.q-row .q-text', { hasText: edited })).toHaveCount(1);
});
