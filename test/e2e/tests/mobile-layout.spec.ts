import type { Page } from './fixtures';
import { test, expect } from './fixtures';
import { seedQuiz, playThroughQuiz, installPlaythroughClock } from './helpers';
import { adminStatePath } from '../e2e-auth';

// #844 — a narrow iPhone (~320px CSS width) surfaced layout breaks: the
// shared top bar wrapped account-cluster labels mid-word and the solo
// leaderboard table overflowed the viewport on a long display name. These
// specs pin the no-horizontal-overflow invariant at 320px and that the
// top-bar links stay on one line. 1px tolerance for sub-pixel rounding.
const NARROW = { width: 320, height: 720 } as const;

// hasHorizontalOverflow reports whether the document scrolls sideways:
// scrollWidth exceeding clientWidth means content spilled past the viewport.
async function horizontalOverflow(page: Page): Promise<{ scrollWidth: number; clientWidth: number }> {
  return page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
  }));
}

// fontsReady waits for the web fonts to finish loading so layout
// measurements run against final metrics. Before Inter/Orbitron load the
// browser renders a wider fallback font, which can transiently overflow the
// viewport; measuring in that window flaked under CI load.
async function fontsReady(page: Page): Promise<void> {
  await page.evaluate(() => document.fonts.ready);
}

test.describe('narrow-viewport layout (#844)', () => {
  test.use({ viewport: NARROW });

  test('home page does not overflow horizontally at 320px', async ({ page }) => {
    await page.goto('/');
    // Wait for the hero so the measurement runs against final layout.
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
    await fontsReady(page);

    const m = await horizontalOverflow(page);
    expect(
      m.scrollWidth,
      `documentElement.scrollWidth (${m.scrollWidth}) > clientWidth (${m.clientWidth}) — home page overflows at 320px`,
    ).toBeLessThanOrEqual(m.clientWidth + 1);
  });

  test('top-bar account-cluster links stay on one line at 320px', async ({ page }) => {
    await page.goto('/');

    // The brand link keeps its accessible name even with the wordmark text
    // hidden below sm — the role locator must still resolve it.
    await expect(page.getByRole('link', { name: 'Top Banana!' })).toBeVisible();
    await fontsReady(page);

    // Anonymous home renders the Log in + Manage quizzes cluster links.
    // whitespace-nowrap must keep each on a single line: a one-line link
    // is shorter than two stacked lines, so assert the rendered height is
    // below a two-line threshold and the computed white-space is nowrap.
    for (const name of ['Log in', 'Manage quizzes']) {
      const link = page.getByRole('link', { name });
      await expect(link).toBeVisible();

      const whiteSpace = await link.evaluate((el) => getComputedStyle(el).whiteSpace);
      expect(whiteSpace, `${name} link should not wrap mid-label`).toBe('nowrap');

      const box = await link.boundingBox();
      const lineHeight = await link.evaluate((el) => parseFloat(getComputedStyle(el).lineHeight));
      expect(box, `${name} link should have a bounding box`).not.toBeNull();
      // A wrapped label renders ~2x line-height tall; one line stays under
      // 1.5x. lineHeight is the text line-height in px.
      expect(
        box!.height,
        `${name} link height (${box!.height}) suggests it wrapped to two lines (lineHeight ${lineHeight})`,
      ).toBeLessThan(lineHeight * 1.5);
    }
  });
});

// The solo leaderboard needs a finished game with a long display name, which
// requires seeding a quiz as the shared admin and then playing it through
// anonymously after claiming a 30+ char name. Mirrors claim.spec.ts test 4.
test.describe('solo leaderboard narrow-viewport layout (#844)', () => {
  test.use({ storageState: adminStatePath(), viewport: NARROW });

  test('solo leaderboard does not overflow horizontally on a long display name at 320px', async ({ page, browserName }) => {
    // A full anonymous playthrough spans four questions of feedback.
    test.setTimeout(45_000);

    // The title must not contain the word "Leaderboard": the shared
    // playthrough helper gates on getByRole('heading', { name: 'Leaderboard' }),
    // which a quiz-title heading carrying that substring would also match.
    const quizTitle = `E2E Mobile Standings ${browserName}`;
    // A 30+ char unbroken name is the worst case the fixed-layout table must
    // wrap instead of widening the table past the viewport.
    const longName = `seed-player-178021737008387144-${browserName}`;

    await seedQuiz(page, quizTitle);
    await page.context().clearCookies();
    // The later playthrough fast-forwards per-question timers via the
    // virtual clock; install before any navigation so the SPA's
    // setInterval/setTimeout calls land on it from first paint.
    await installPlaythroughClock(page);

    // Claim the long name on the start screen, then play through so the
    // leaderboard row for the current player carries it.
    await page.goto('/client/');
    await expect(page.locator('.claim-cta:visible')).toBeVisible();
    await page.getByRole('button', { name: 'Set your name' }).click();
    const modal = page.locator('[role="dialog"]');
    await expect(modal).toBeVisible();
    await modal.locator('input#claim-name-modal').fill(longName);
    await modal.getByRole('button', { name: 'Save' }).click();
    await expect(modal).toBeHidden();

    await playThroughQuiz(page, quizTitle);

    // The current player's row carries the long name and must be visible.
    const playerRow = page.locator('table.player-table tbody tr[aria-current="true"]');
    await expect(playerRow).toBeVisible();
    await expect(playerRow).toContainText(longName);
    await fontsReady(page);

    const m = await horizontalOverflow(page);
    expect(
      m.scrollWidth,
      `documentElement.scrollWidth (${m.scrollWidth}) > clientWidth (${m.clientWidth}) — solo leaderboard overflows at 320px on a long name`,
    ).toBeLessThanOrEqual(m.clientWidth + 1);

    // The long-named finisher now appears in the home "Most active players"
    // list. That name is a single unbroken token, so the truncate must clip
    // it; without min-w-0 on the flex item it widened the row past the
    // viewport (#844). Checked here because this test already has a finisher
    // with a deterministic long name, rather than relying on incidental data.
    await page.goto('/');
    await fontsReady(page);
    const home = await horizontalOverflow(page);
    expect(
      home.scrollWidth,
      `home scrollWidth (${home.scrollWidth}) > clientWidth (${home.clientWidth}) — home overflows on a long active-player name`,
    ).toBeLessThanOrEqual(home.clientWidth + 1);
  });
});
