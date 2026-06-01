import { test, expect } from './fixtures';
import { registerForPending, login, seedQuiz, markEmailVerified } from './helpers';
import { adminStatePath } from '../e2e-auth';

// Pre-game navigation on the player SPA: a signed-in player can reach
// /profile via the account link, and a deep-linked /play/{slug} start
// screen exposes a link to the /quizzes catalog.

// Test 1 — a signed-in player sees the account link and it routes to
// /profile. Registers a plain player rather than relying on
// registerAdmin so the test is robust to ordering (the "first registrant
// becomes admin" slot may already be taken by another spec in the same
// worker), mirroring claim.spec.ts's authenticated-player test.
test('signed-in player can reach /profile via the account link on the play SPA', async ({ page, browserName }) => {
  const displayName = `e2e-pregame-authn-${browserName}-${Date.now()}`;
  // The hard gate (#574) means register no longer signs the player in.
  // Verify the row directly, then log in so the SPA sees an
  // authenticated, verified player.
  await registerForPending(page, displayName);
  markEmailVerified(displayName);
  await login(page, displayName);

  await page.goto('/client/');

  // The account link shows the player's displayName and links to /profile.
  // It is gated on isAuthenticated() so an anonymous visitor never sees
  // it; the claim CTA covers that case instead.
  const accountLink = page.getByTestId('account-profile-link');
  await expect(accountLink).toBeVisible();
  await expect(accountLink).toHaveText(displayName);

  // The claim CTA must stay hidden for this signed-in player — the two
  // affordances are mutually exclusive.
  await expect(page.locator('.claim-cta')).not.toBeVisible();

  // Plain navigation, not an Alpine event, so wait on the URL change.
  await Promise.all([
    page.waitForURL(/\/profile$/),
    accountLink.click(),
  ]);
});

// Test 2 — an anonymous deep-linked /play/{slug} start screen exposes a
// link to the /quizzes catalog. The non-deep-link /client/ entry already
// surfaces "Browse all quizzes" as its primary affordance; this pins the
// secondary escape hatch that lets a deep-link visitor reach the catalog
// without going home first.
test.describe('deep-linked play start screen', () => {
  // Seed the quiz as the shared admin via the JSON importer; the deep-link
  // visit then runs anonymous after clearing the admin cookie.
  test.use({ storageState: adminStatePath() });

  test('deep-linked play start screen exposes a link to the quizzes catalog', async ({ page, browserName }) => {
    test.setTimeout(30_000);

    const quizTitle = `E2E Pregame Nav ${browserName}`;

    await seedQuiz(page, quizTitle);
    await page.context().clearCookies();

    // Land on the deep link via the public list, the same path a shared
    // quiz link takes a visitor down.
    await page.goto('/quizzes');
    await page.getByRole('link', { name: quizTitle }).click();
    await expect(page).toHaveURL(/\/play\/[^/]+-\d+$/);

    // The deep-link start screen shows the quiz title + Start cluster. The
    // browse link sits below as a secondary affordance.
    await expect(page.getByRole('button', { name: 'Start Game' })).toBeVisible();
    const browseLink = page.getByTestId('browse-quizzes-link');
    await expect(browseLink).toBeVisible();

    await Promise.all([
      page.waitForURL(/\/quizzes$/),
      browseLink.click(),
    ]);
  });
});
