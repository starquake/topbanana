import { test, expect } from './fixtures';
import type { Locator, Page } from './fixtures';
import { importQuiz } from './helpers';
import { adminStatePath } from '../e2e-auth';

// Act as the shared migration-seeded admin via storageState rather than
// registering a new admin per test: each test seeds its own uniquely-titled
// quiz, so they never collide on a display name or quiz title.
test.use({ storageState: adminStatePath() });

// #199 - drag-and-drop reorder for rounds and questions on the admin quiz
// view. SortableJS runs in its default native-HTML5-DnD mode (mouse) with its
// built-in touch handling for touch devices; the Up/Down buttons and
// move-to-round <select> stay as the no-JS / keyboard fallback. Each drop
// POSTs to the reorder endpoint and swaps in the server-rendered
// #questions-list partial, so the assertions key on the PERSISTED order after
// a reload, never on intermediate drag state.
//
// DRIVING THE DRAG: native HTML5 drag-and-drop is only reliably scriptable in
// Firefox, via Playwright's locator.dragTo (which emits the real
// dragstart/dragover/drop sequence). Chromium under Playwright does not
// dispatch a usable synthetic native-DnD gesture, so the three drag specs run
// on Firefox only; the fallback spec runs on every project. This is a
// test-harness limitation, not a product gap: the drag wiring is identical
// across browsers, and the full drop -> POST -> partial-swap -> persisted-order
// chain is exercised end to end on Firefox.

// dragReorder drags the grip handle onto the target. force:true bypasses the
// actionability check the drifting drag-clone can otherwise fail.
async function dragReorder(handle: Locator, target: Locator): Promise<void> {
  await handle.dragTo(target, { targetPosition: { x: 60, y: 10 }, force: true });
}

async function questionTexts(page: Page): Promise<string[]> {
  return page.locator('article.q-row .q-text').allTextContents();
}

async function roundTitles(page: Page): Promise<string[]> {
  return page.locator('section.round-section h3').allTextContents();
}

// roundSection scopes to a section by its heading. A plain hasText filter is
// ambiguous here: every question's move-to-round <select> lists ALL round
// titles as <option>s, so e.g. Round Alpha's text also contains "Round Beta".
// Filtering on the section's own <h3> heading avoids that collision.
function roundSection(page: Page, title: string): Locator {
  return page
    .locator('section.round-section')
    .filter({ has: page.getByRole('heading', { name: title }) });
}

function twoRoundQuiz(title: string) {
  return {
    title,
    description: 'E2E drag-and-drop reorder quiz',
    rounds: [
      {
        title: 'Round Alpha',
        questions: [
          { text: 'Alpha Q1', options: [{ text: 'a', correct: true }, { text: 'b', correct: false }] },
          { text: 'Alpha Q2', options: [{ text: 'a', correct: true }, { text: 'b', correct: false }] },
          { text: 'Alpha Q3', options: [{ text: 'a', correct: true }, { text: 'b', correct: false }] },
        ],
      },
      {
        title: 'Round Beta',
        questions: [
          { text: 'Beta Q1', options: [{ text: 'a', correct: true }, { text: 'b', correct: false }] },
          { text: 'Beta Q2', options: [{ text: 'a', correct: true }, { text: 'b', correct: false }] },
        ],
      },
    ],
  };
}

// openQuiz seeds a uniquely-titled two-round quiz as the shared admin and
// opens its quiz view. The title is unique per test + project so parallel
// workers and the four tests in this file never collide.
async function openQuiz(page: Page, title: string): Promise<void> {
  const doc = twoRoundQuiz(title);
  await importQuiz(page, doc);
  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: doc.title }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  await expect(page.locator('#questions-list')).toBeVisible();
}

test('drag a question to a new position within its round persists after reload', async ({ page, browserName }) => {
  test.skip(browserName === 'chromium', 'native DnD is not scriptable in Chromium under Playwright; covered on Firefox');
  test.setTimeout(60_000);
  await openQuiz(page, `E2E Reorder Within ${browserName}`);

  expect(await questionTexts(page)).toEqual(['Alpha Q1', 'Alpha Q2', 'Alpha Q3', 'Beta Q1', 'Beta Q2']);

  // Drag Alpha Q3's handle up onto Alpha Q1; it lands ahead of the others
  // in the round.
  const q3 = page.locator('article.q-row', { hasText: 'Alpha Q3' });
  const q1 = page.locator('article.q-row', { hasText: 'Alpha Q1' });
  await dragReorder(q3.locator('[data-question-handle]'), q1);

  // The server swap renumbers positions; poll until the new order settles.
  await expect.poll(async () => (await questionTexts(page)).slice(0, 3)).toEqual(['Alpha Q3', 'Alpha Q1', 'Alpha Q2']);

  await page.reload();
  expect(await questionTexts(page)).toEqual(['Alpha Q3', 'Alpha Q1', 'Alpha Q2', 'Beta Q1', 'Beta Q2']);
});

test('drag a question into a different round persists after reload', async ({ page, browserName }) => {
  test.skip(browserName === 'chromium', 'native DnD is not scriptable in Chromium under Playwright; covered on Firefox');
  test.setTimeout(60_000);
  await openQuiz(page, `E2E Reorder Cross ${browserName}`);

  // Drag Alpha Q1 into Round Beta, dropping onto Beta Q1.
  const alphaQ1 = page.locator('article.q-row', { hasText: 'Alpha Q1' });
  const betaQ1 = page.locator('article.q-row', { hasText: 'Beta Q1' });
  await dragReorder(alphaQ1.locator('[data-question-handle]'), betaQ1);

  await page.reload();

  // Alpha Q1 now lives under Round Beta. Scope each lookup to a section so a
  // stray match in the other round would fail the assertion.
  const betaSection = roundSection(page, 'Round Beta');
  await expect(betaSection.locator('.q-text', { hasText: 'Alpha Q1' })).toBeVisible();

  const alphaSection = roundSection(page, 'Round Alpha');
  await expect(alphaSection.locator('.q-text', { hasText: 'Alpha Q1' })).toHaveCount(0);
});

test('drag a round to a new position persists after reload', async ({ page, browserName }) => {
  test.skip(browserName === 'chromium', 'native DnD is not scriptable in Chromium under Playwright; covered on Firefox');
  test.setTimeout(60_000);
  await openQuiz(page, `E2E Reorder RoundMove ${browserName}`);

  expect(await roundTitles(page)).toEqual(['Round Alpha', 'Round Beta']);

  // Drag Round Beta's header handle up onto Round Alpha to swap them.
  const beta = roundSection(page, 'Round Beta');
  const alpha = roundSection(page, 'Round Alpha');
  await dragReorder(beta.locator('[data-round-handle]'), alpha);

  await expect.poll(async () => roundTitles(page)).toEqual(['Round Beta', 'Round Alpha']);

  await page.reload();
  expect(await roundTitles(page)).toEqual(['Round Beta', 'Round Alpha']);
});

test('a failed reorder POST snaps the list back and shows an error', async ({ page, browserName }) => {
  test.skip(browserName === 'chromium', 'native DnD is not scriptable in Chromium under Playwright; covered on Firefox');
  test.setTimeout(60_000);
  await openQuiz(page, `E2E Reorder Failure ${browserName}`);

  const original = ['Alpha Q1', 'Alpha Q2', 'Alpha Q3', 'Beta Q1', 'Beta Q2'];
  expect(await questionTexts(page)).toEqual(original);

  // Force the reorder POST to fail so the client must revert its optimistic
  // move. The snapshot is captured at drag START (pre-move), so the list must
  // return to the original order, not stay where SortableJS dropped it.
  await page.route('**/questions/*/position', (route) => route.fulfill({ status: 500, body: 'nope' }));

  const q3 = page.locator('article.q-row', { hasText: 'Alpha Q3' });
  const q1 = page.locator('article.q-row', { hasText: 'Alpha Q1' });
  await dragReorder(q3.locator('[data-question-handle]'), q1);

  await expect(page.locator('[data-reorder-error]')).toBeVisible();
  await expect.poll(async () => questionTexts(page)).toEqual(original);
});

test('the Up/Down and move-to-round fallbacks still reorder without drag', async ({ page, browserName }) => {
  test.setTimeout(60_000);
  await openQuiz(page, `E2E Reorder Fallback ${browserName}`);

  // Move Alpha Q2 up with the Up button (the htmx fallback path). It swaps
  // the #questions-list partial in place, no drag involved.
  const alphaQ2 = page.locator('article.q-row', { hasText: 'Alpha Q2' });
  await alphaQ2.getByRole('button', { name: 'Move up' }).click();
  await expect.poll(async () => (await questionTexts(page)).slice(0, 2)).toEqual(['Alpha Q2', 'Alpha Q1']);

  // Move Beta Q1 into Round Alpha via the move-to-round <select> fallback.
  // This form is a plain POST that 303-redirects back to the quiz view, so
  // wait for that navigation to settle before asserting the new grouping.
  const betaQ1 = page.locator('article.q-row', { hasText: 'Beta Q1' });
  await betaQ1.locator('select[name="round_id"]').selectOption({ label: 'Round Alpha' });
  await Promise.all([
    page.waitForURL(/\/admin\/quizzes\/\d+$/),
    betaQ1.getByRole('button', { name: 'Move', exact: true }).click(),
  ]);

  const alphaSection = roundSection(page, 'Round Alpha');
  await expect(alphaSection.locator('.q-text', { hasText: 'Beta Q1' })).toBeVisible();
});
