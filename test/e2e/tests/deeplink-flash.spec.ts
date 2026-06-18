import { test, expect, Route, Page } from './fixtures';
import { seedQuiz, playThroughQuiz, installPlaythroughClock } from './helpers';
import { adminStatePath } from '../e2e-auth';

// Seed the quiz as the shared admin (via the JSON importer), then clear the
// admin cookie before each playthrough so the gameplay runs anonymous.
test.use({ storageState: adminStatePath() });

// Deep-linking into a quiz the player has ALREADY completed must NOT flash
// the quiz title/description before the leaderboard takes over. The
// deep-link header is gated on `startStateResolved`, which only flips true
// once checkAlreadyPlayed() has run its two probes; for a completed quiz that
// probe also flips `finished` true, so the header is never eligible to paint.
// Without the gate the header rendered optimistically on load and then
// vanished when `finished` landed — a visible title -> leaderboard layout
// shift (the bug this test pins).
test('deep-link to an already-completed quiz never flashes the quiz header', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Deeplink Flash ${browserName}`;

  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();
  // The playthrough fast-forwards per-question timers via the virtual
  // clock; install before any navigation so the SPA's
  // setInterval/setTimeout calls land on it from first paint.
  await installPlaythroughClock(page);

  // Play the quiz to completion so the (player, quiz) pair is locked to the
  // already-completed state for the revisit below.
  await playThroughQuiz(page, quizTitle);
  await expect(page.getByRole('heading', { name: 'Game Finished!' })).toBeVisible();

  // Capture the play deep-link the SPA settled on so the revisit hits the
  // same /play/{slug-id} that flips `finished` on load.
  const playUrl = new URL(page.url()).pathname;
  expect(playUrl).toMatch(/^\/play\/[^/]+-\d+$/);

  // Install a MutationObserver BEFORE any page script runs (addInitScript
  // re-applies on every navigation in this context) so it observes the SPA
  // boot from the very first frame. It records whether the deep-link header
  // ever entered the DOM — the deterministic signal the header would have
  // flashed under the bug. The flag lives on window so the assertion below
  // can read it after the page has settled.
  await page.addInitScript(() => {
    const w = window as unknown as { __deepLinkHeaderSeen?: boolean };
    w.__deepLinkHeaderSeen = false;
    const check = (root: ParentNode) => {
      if (root.querySelector('[data-testid="deep-link-header"]')) {
        w.__deepLinkHeaderSeen = true;
      }
    };
    const start = () => {
      check(document);
      const observer = new MutationObserver(() => check(document));
      observer.observe(document.documentElement, { childList: true, subtree: true });
    };
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', start);
    } else {
      start();
    }
  });

  // Revisit the already-completed quiz via its deep link.
  await page.goto(playUrl);

  // End state: the leaderboard view has taken over. "Game Finished!" and the
  // populated table render; the deep-link header is NOT present.
  await expect(page.getByRole('heading', { name: 'Game Finished!' })).toBeVisible();
  await expect(page.locator('.player-table')).toBeVisible();
  await expect(page.locator('[data-testid="deep-link-header"]')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Start Game' })).toBeHidden();

  // The header must never have appeared during the boot -> resolve window.
  // This is the "never flashed" invariant; the MutationObserver above is the
  // robust deterministic way to assert it — it catches a header that mounts
  // then unmounts faster than Playwright's polling could ever sample.
  const headerEverSeen = await page.evaluate(
    () => (window as unknown as { __deepLinkHeaderSeen?: boolean }).__deepLinkHeaderSeen ?? false,
  );
  expect(headerEverSeen, 'deep-link header flashed in then out on an already-completed revisit').toBe(false);
});

// The not-yet-completed deep link must still show the quiz title/description.
// The flash fix gates the header on startStateResolved, so this guards
// against over-correcting: a fresh deep link must still surface the header
// (once checkAlreadyPlayed resolves it), just without a flash.
test('deep-link to a not-completed quiz still shows the quiz header', async ({ page, browserName }) => {
  test.setTimeout(30_000);

  const quizTitle = `E2E Deeplink Fresh ${browserName}`;

  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();

  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);

  // The header resolves once checkAlreadyPlayed has run; the quiz is fresh,
  // so `finished` stays false and the title/description paint and remain.
  const header = page.locator('[data-testid="deep-link-header"]');
  await expect(header).toBeVisible();
  await expect(header.getByRole('heading', { name: quizTitle })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Start Game' })).toBeVisible();
});

// checkAlreadyPlayed() resolves the start state in two serial fetches:
// GET /leaderboard, then GET /my-game (which flips `finished` for a completed
// quiz). #676 only gated the title/description block on startStateResolved, so
// the leaderboard table and the start-screen action cluster still painted in
// the gap between the two fetches and then jumped when `finished` (or the
// deep-link header) landed above them — the reopened-#675 layout shift.
//
// These two specs hold the second fetch (/my-game) open via route
// interception. While it is pending, NONE of the start-state-resolved blocks
// may be visible: the whole resolved view must paint atomically once
// startStateResolved flips, so it appears once in its final position rather
// than building up piecemeal. The hold makes the transition observable instead
// of a sub-frame race.

// holdMyGame intercepts GET /my-game and returns a resolver the test calls to
// release the held response. Until then the request never completes, pinning
// the SPA in the start-state-pending window so the assertions below are not a
// timing race.
async function holdMyGame(
  page: Page,
  body: string,
  status = 200,
): Promise<() => void> {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(/\/api\/quizzes\/[^/]+\/my-game$/, async (route: Route) => {
    await gate;
    await route.fulfill({ status, contentType: 'application/json', body });
  });
  return release;
}

test('deep-link to an already-completed quiz paints the leaderboard once, not before the finished header', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Deeplink Hold Completed ${browserName}`;

  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();
  await installPlaythroughClock(page);

  await playThroughQuiz(page, quizTitle);
  await expect(page.getByRole('heading', { name: 'Game Finished!' })).toBeVisible();

  const playUrl = new URL(page.url()).pathname;

  // Hold /my-game (the completed-state probe) open for the revisit.
  const release = await holdMyGame(page, JSON.stringify({ gameId: 'held', completed: true }));

  await page.goto(playUrl, { waitUntil: 'commit' });

  // Positive anchor: the anonymous claim CTA renders independent of
  // startStateResolved (it gates only on !isAuthenticated() && !gameId), so
  // waiting for it proves Alpine has booted and we are genuinely in the
  // start-state-pending window — not merely observing a pre-boot blank page,
  // which would make the "hidden" assertions below pass for the wrong reason.
  await expect(page.getByTestId('claim-cta-name')).toBeVisible();

  // While /my-game is pending, startStateResolved is false: the leaderboard,
  // the "Game Finished!" header, and the start-screen action cluster must all
  // stay hidden so nothing paints in a non-final position. Under the reopened
  // #675 bug the leaderboard and Start cluster painted here (the leaderboard
  // fetch resolves before /my-game), so these assertions fail without the fix.
  await expect(page.locator('[data-testid="leaderboard-section"]')).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Game Finished!' })).toBeHidden();
  await expect(page.getByRole('button', { name: 'Start Game' })).toBeHidden();
  await expect(page.locator('[data-testid="deep-link-header"]')).toHaveCount(0);

  // Release the probe: the resolved completed view now paints in one tick.
  release();

  await expect(page.getByRole('heading', { name: 'Game Finished!' })).toBeVisible();
  await expect(page.locator('[data-testid="leaderboard-section"]')).toBeVisible();
  // Structural sanity check: the finished header sits above the leaderboard.
  const finishedBox = await page.getByRole('heading', { name: 'Game Finished!' }).boundingBox();
  const tableBox = await page.locator('[data-testid="leaderboard-section"]').boundingBox();
  expect(finishedBox).not.toBeNull();
  expect(tableBox).not.toBeNull();
  expect(finishedBox!.y).toBeLessThan(tableBox!.y);
  await expect(page.locator('[data-testid="deep-link-header"]')).toHaveCount(0);
});

test('deep-link to a not-completed quiz paints the header and leaderboard together, not the leaderboard first', async ({ page, browserName }) => {
  test.setTimeout(30_000);

  const quizTitle = `E2E Deeplink Hold Fresh ${browserName}`;

  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();

  // Discover the deep link, then revisit it with /my-game held to a 404
  // (not-completed) so the resolve transition is observable.
  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  const url = new URL(page.url()).pathname;

  const release = await holdMyGame(page, 'null', 404);

  await page.goto(url, { waitUntil: 'commit' });

  // Positive anchor: the anonymous claim CTA renders independent of
  // startStateResolved, so waiting for it proves Alpine booted and we are in
  // the pending window before the "hidden" assertions run.
  await expect(page.getByTestId('claim-cta-name')).toBeVisible();

  // While /my-game is pending, the deep-link header, the leaderboard, and the
  // Start cluster must all stay hidden — no piecemeal build-up. Under the bug
  // the leaderboard painted here (its fetch resolves before /my-game).
  await expect(page.locator('[data-testid="deep-link-header"]')).toHaveCount(0);
  await expect(page.locator('[data-testid="leaderboard-section"]')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Start Game' })).toBeHidden();

  release();

  // The resolved fresh view paints the header ABOVE the leaderboard, together.
  // The fresh quiz has no finishers, so the leaderboard renders its empty
  // state, not a table — anchor on the section, not the table row.
  const header = page.locator('[data-testid="deep-link-header"]');
  await expect(header).toBeVisible();
  await expect(page.getByRole('button', { name: 'Start Game' })).toBeVisible();
  await expect(page.locator('[data-testid="leaderboard-section"]')).toBeVisible();
  const headerBox = await header.boundingBox();
  const tableBox = await page.locator('[data-testid="leaderboard-section"]').boundingBox();
  expect(headerBox).not.toBeNull();
  expect(tableBox).not.toBeNull();
  expect(headerBox!.y).toBeLessThan(tableBox!.y);
});
