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

// Editor density (#1244 slice 7). The rail reuses the quiz view's rows and the
// pane reuses the full-page form, so both need editor-only trimming: the
// spoiler and per-row icons belong to the quiz view, and Cancel-as-a-link
// would throw away the whole editing session.
test('the editor rail and pane drop their full-page furniture', async ({ page, browserName }) => {
  const title = `E2E Editor Density ${browserName} ${Date.now()}`;
  await seedQuiz(page, title, QUIZ_QUESTIONS, { publish: false });
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: title }).click();

  // The quiz view keeps both: it is the screen a host presents from.
  await expect(page.locator('.q-spoiler').first()).toBeAttached();
  await expect(page.locator('.q-actions').first()).toBeAttached();

  await page.getByTestId('open-question-editor').click();
  await expect(page).toHaveURL(/\/questions$/);

  // The rail drops them - answers are in the pane, and a row is a selector.
  await expect(page.locator('[data-testid="editor-rail"] .q-spoiler')).toHaveCount(0);
  await expect(page.locator('[data-testid="editor-rail"] .q-actions')).toHaveCount(0);

  await page.locator('article.q-row').first().click();
  await expect(page.locator('#question-editor textarea[name="text"]')).toBeVisible();

  // Save-and-next and Discard replace the form's Save + Cancel-link.
  await expect(page.locator('[data-editor-save-next]')).toBeVisible();
  await expect(page.getByTestId('editor-discard')).toBeVisible();
  await expect(page.locator('#question-editor a', { hasText: 'Cancel' })).toHaveCount(0);

  // Discard reloads the question rather than navigating away.
  const textarea = page.locator('#question-editor textarea[name="text"]');
  const original = await textarea.inputValue();
  await textarea.fill('Scratch edit that should not survive');
  await page.getByTestId('editor-discard').click();
  await expect(page).toHaveURL(/\/questions/);
  await expect.poll(async () => textarea.inputValue()).toBe(original);
});

// Rounds share the pane with questions (#1244 slice 5). Saving a round swaps
// its header back out of band - only the header, not the whole round section,
// which would replace the question list and rebuild its Sortable instance.
test('a round can be edited in the pane and its header updates in place', async ({ page, browserName }) => {
  const title = `E2E Editor Rounds ${browserName} ${Date.now()}`;
  await seedQuiz(page, title, QUIZ_QUESTIONS, { publish: false });
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: title }).click();
  await page.getByTestId('open-question-editor').click();
  await expect(page).toHaveURL(/\/questions$/);

  const roundHead = page.locator('[data-editor-round-row]').first();
  await expect(roundHead).toBeVisible();
  const originalTitle = (await roundHead.locator('h3').textContent())?.trim();

  // Selecting the round header opens the round form, not a question form.
  await roundHead.click();
  const titleInput = page.locator('#question-editor input[name="title"]');
  await expect(titleInput).toBeVisible();

  const renamed = `Renamed round ${Date.now()}`;
  await titleInput.fill(renamed);
  await page.locator('#question-editor button[type="submit"]').first().click();

  // The rail header picks up the new name without a reload.
  await expect.poll(async () => roundHead.locator('h3').textContent()).toContain(renamed);
  expect(renamed).not.toBe(originalTitle);

  // The question rows are still there - the swap replaced only the header.
  await expect(page.locator('article.q-row').first()).toBeVisible();

  // And reordering still works, so the swap left Sortable intact.
  const texts = async () => page.locator('article.q-row .q-text').allTextContents();
  const before = await texts();
  if (browserName !== 'chromium' && before.length > 1) {
    await page.locator('article.q-row').last().locator('[data-question-handle]')
      .dragTo(page.locator('article.q-row').first(), { force: true });
    await expect.poll(async () => (await texts())[0]).toBe(before[before.length - 1]);
  }
});

// Slice 6 retires the standalone question page: the old URL is a permanent
// redirect into the editor, and "Add question" opens the blank form in the
// pane rather than navigating away mid-session.
test('the old question edit URL redirects into the editor', async ({ page, browserName }) => {
  const title = `E2E Editor Retire ${browserName} ${Date.now()}`;
  await seedQuiz(page, title, QUIZ_QUESTIONS, { publish: false });
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: title }).click();
  const quizUrl = page.url();
  const quizId = quizUrl.match(/\/admin\/quizzes\/(\d+)/)?.[1];
  expect(quizId, 'quiz id from the URL').toBeTruthy();

  await page.getByTestId('open-question-editor').click();
  const firstRow = page.locator('article.q-row').first();
  const questionId = await firstRow.getAttribute('data-question-id');

  // Visiting the retired URL directly lands in the editor with that question
  // already open.
  await page.goto(`/admin/quizzes/${quizId}/questions/${questionId}/edit`);
  await expect(page).toHaveURL(new RegExp(`/admin/quizzes/${quizId}/questions\\?q=${questionId}$`));
  await expect(page.locator('#question-editor textarea[name="text"]')).toBeVisible();

  // Add question fills the pane instead of navigating.
  await page.locator('[data-editor-add-question]').first().click();
  await expect(page).toHaveURL(/\/questions(\?|$)/);
  await expect(page.locator('#question-editor textarea[name="text"]')).toHaveValue('');
});

// Adding a round from the editor (#1257). The rail could create questions but
// not rounds, so authoring meant bouncing out to the quiz view. A new round has
// no header to graft onto, so the save re-renders the whole rail out of band -
// which rebuilds every Sortable instance, hence the drag check at the end.
test('a round can be added from the editor rail', async ({ page, browserName }) => {
  const title = `E2E Add Round ${browserName} ${Date.now()}`;
  await seedQuiz(page, title, QUIZ_QUESTIONS, { publish: false });
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: title }).click();
  await page.getByTestId('open-question-editor').click();
  await expect(page).toHaveURL(/\/questions$/);

  const roundsBefore = await page.locator('section.round-section').count();

  await page.getByTestId('editor-add-round').click();
  // The blank round form fills the pane rather than navigating away.
  await expect(page).toHaveURL(/\/questions$/);
  const titleInput = page.locator('#question-editor input[name="title"]');
  await expect(titleInput).toBeVisible();
  await expect(titleInput).toHaveValue('');

  const roundName = `Bonus round ${Date.now()}`;
  await titleInput.fill(roundName);
  await page.locator('#question-editor button[type="submit"]').first().click();

  // The rail gains the section without a reload.
  await expect.poll(async () => page.locator('section.round-section').count())
    .toBe(roundsBefore + 1);
  await expect(page.locator('section.round-section h3', { hasText: roundName })).toBeVisible();

  // The rail was re-rendered wholesale, so Sortable had to rebind: drag still works.
  if (browserName !== 'chromium') {
    const texts = async () => page.locator('article.q-row .q-text').allTextContents();
    const before = await texts();
    if (before.length > 1) {
      await page.locator('article.q-row').last().locator('[data-question-handle]')
        .dragTo(page.locator('article.q-row').first(), { force: true });
      await expect.poll(async () => (await texts())[0]).toBe(before[before.length - 1]);
    }
  }
});

// Duplicating a question from the editor (#1246). The copy lands directly after
// its source and opens in the pane. Like adding a round, the rail re-renders
// wholesale because the new row has nothing to graft onto - hence the drag
// check, which is what catches a Sortable that failed to rebind.
test('a question can be duplicated from the editor', async ({ page, browserName }) => {
  const title = `E2E Duplicate ${browserName} ${Date.now()}`;
  await seedQuiz(page, title, QUIZ_QUESTIONS, { publish: false });
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: title }).click();
  await page.getByTestId('open-question-editor').click();

  const rows = page.locator('article.q-row');
  const texts = async () => rows.locator('.q-text').allTextContents();
  const before = await texts();

  await rows.first().click();
  await expect(page.locator('#question-editor textarea[name="text"]')).toBeVisible();
  const sourceText = before[0];

  await page.getByTestId('editor-duplicate').click();

  // One more row, and the copy sits immediately behind its source.
  await expect.poll(async () => (await texts()).length).toBe(before.length + 1);
  const after = await texts();
  expect(after[0]).toBe(sourceText);
  expect(after[1]).toBe(sourceText);

  // The pane holds the copy, ready to edit.
  await expect(page.locator('#question-editor textarea[name="text"]')).toHaveValue(sourceText);

  // The rail was replaced wholesale, so Sortable had to rebind.
  if (browserName !== 'chromium') {
    await rows.last().locator('[data-question-handle]')
      .dragTo(rows.first(), { force: true });
    await expect.poll(async () => (await texts())[0]).toBe(after[after.length - 1]);
  }
});

// Editor pane polish (#1258). The pane never said which question was open, and
// it carried furniture that only made sense on a standalone page.
test('the editor pane names the open question and drops page furniture', async ({ page, browserName }) => {
  const title = `E2E Pane Polish ${browserName} ${Date.now()}`;
  await seedQuiz(page, title, QUIZ_QUESTIONS, { publish: false });
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: title }).click();
  await page.getByTestId('open-question-editor').click();

  await page.locator('article.q-row').first().click();
  await expect(page.locator('#question-editor textarea[name="text"]')).toBeVisible();

  // The pane says which question is open, and which round it is in.
  const head = page.locator('#question-editor .editor-pane-head');
  await expect(head).toBeVisible();
  await expect(head).toContainText('Question 01');

  // The readonly Position field pointed at a screen that no longer reorders.
  await expect(page.locator('#question-editor #position')).toHaveCount(0);

  // Round rows in the rail lose their icon cluster and summary box; the round
  // is editable in the pane instead.
  const rail = page.locator('[data-testid="editor-rail"]');
  await expect(rail.locator('a[aria-label="Edit round"]')).toHaveCount(0);
  await expect(rail.getByText('Round summary')).toHaveCount(0);

  // Selecting the round still opens it, so nothing was lost by hiding them.
  await page.locator('[data-editor-round-row]').first().click();
  await expect(page.locator('#question-editor input[name="title"]')).toBeVisible();
});

// One pane at a time on a narrow screen (#1259). Below the breakpoint the rail
// stacked above the pane, so picking a question left the form far below the
// fold with nothing to say it had moved.
test('the editor shows one pane at a time on a phone', async ({ page, browserName }) => {
  const title = `E2E Editor Narrow ${browserName} ${Date.now()}`;
  await seedQuiz(page, title, QUIZ_QUESTIONS, { publish: false });
  await page.setViewportSize({ width: 390, height: 780 });
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: title }).click();
  await page.getByTestId('open-question-editor').click();
  await expect(page).toHaveURL(/\/questions$/);

  const rail = page.getByTestId('editor-rail');
  const pane = page.getByTestId('question-editor');
  const back = page.getByTestId('editor-back');

  // Nothing selected: the rail is the screen.
  await expect(rail).toBeVisible();
  await expect(pane).toBeHidden();
  await expect(back).toBeHidden();

  // Selecting swaps to the pane, with a way back.
  await page.locator('article.q-row').first().click();
  await expect(pane).toBeVisible();
  await expect(rail).toBeHidden();
  await expect(back).toBeVisible();
  await expect(page.locator('#question-editor textarea[name="text"]')).toBeVisible();

  // Back returns to the rail.
  await back.click();
  await expect(rail).toBeVisible();
  await expect(pane).toBeHidden();

  // A deep link lands straight in the pane.
  const questionId = await page.locator('article.q-row').first().getAttribute('data-question-id');
  await page.goto(page.url().replace(/\?.*$/, '') + `?q=${questionId}`);
  await expect(pane).toBeVisible();
  await expect(rail).toBeHidden();

  // Desktop shows both, and the back control is meaningless there.
  await page.setViewportSize({ width: 1280, height: 800 });
  await expect(rail).toBeVisible();
  await expect(pane).toBeVisible();
  await expect(back).toBeHidden();
});

// The top bar's height and the context bar's sticky offset are coupled by hand
// (#1248): .app-bar is `sticky top-[3rem]`, matched to the nav's min-h-[3rem].
// Change one without the other and the context bar leaves a gap or slides
// under. This pins the relationship rather than either number.
test('the context bar sits flush under the top bar', async ({ page, browserName }) => {
  await seedQuiz(page, `E2E Bar Coupling ${browserName} ${Date.now()}`, QUIZ_QUESTIONS);
  await page.setViewportSize({ width: 1280, height: 400 });
  await page.goto('/admin/quizzes');
  await fontsReady(page);

  await page.evaluate(() => window.scrollTo(0, 400));
  await expect.poll(async () => page.evaluate(() => window.scrollY)).toBeGreaterThan(0);

  const topbar = await boxOf(page, 'nav[aria-label="Primary"]');
  const bar = await boxOf(page, '.app-bar');

  // Flush: no gap above it, and not overlapping into the top bar. 2px of
  // tolerance for sub-pixel rounding.
  const gap = bar.y - (topbar.y + topbar.height);
  expect(Math.abs(gap), `context bar should sit flush under the top bar, gap was ${gap}px`)
    .toBeLessThanOrEqual(2);
});
