import { test, expect } from './fixtures';
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
