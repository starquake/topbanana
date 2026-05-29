import { test, expect } from './fixtures';
import { registerAdmin, createQuizWithQuestions, markEmailVerified } from './helpers';

// Pre-game navigation on the player SPA: a signed-in player can reach
// /profile via the account link, and a deep-linked /play/{slug} start
// screen exposes a link to the /quizzes catalog.

// Test 1 — a signed-in player sees the account link and it routes to
// /profile. Registers a plain player rather than relying on
// registerAdmin so the test is robust to ordering (the "first registrant
// becomes admin" slot may already be taken by another spec in the same
// worker), mirroring claim.spec.ts's authenticated-player test.
test('signed-in player can reach /profile via the account link on the play SPA', async ({ page, browserName }) => {
  const username = `e2e-pregame-authn-${browserName}-${Date.now()}`;
  await page.goto('/register');
  await page.locator('input[name=username]').fill(username);
  await page.locator('input[name=email]').fill(`${username}@example.test`);
  await page.locator('input[name=password]').fill('correct-battery-13');
  await page.locator('input[name=password_confirm]').fill('correct-battery-13');
  await page.locator('button[type=submit]').click();
  // The post-register redirect target varies with this worker DB's state
  // (admin bootstrap -> /admin/quizzes, verify gate -> /verify-email/pending,
  // otherwise /), so just wait for the registration to land somewhere off
  // /register. /profile sits behind the verify-email gate, so stamp
  // email_verified_at directly (the same trick registerAdmin uses) so the
  // account link lands on the profile page rather than the pending screen.
  await expect(page).not.toHaveURL(/\/register$/);
  markEmailVerified(username);

  await page.goto('/client/');

  // The account link shows the player's username and links to /profile.
  // It is gated on isAuthenticated() so an anonymous visitor never sees
  // it; the claim CTA covers that case instead.
  const accountLink = page.getByTestId('account-profile-link');
  await expect(accountLink).toBeVisible();
  await expect(accountLink).toHaveText(username);

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
test('deep-linked play start screen exposes a link to the quizzes catalog', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const adminUser = `e2e-admin-pregame-nav-${browserName}`;
  const quizTitle = `E2E Pregame Nav ${browserName}`;

  await registerAdmin(page, adminUser);
  await createQuizWithQuestions(page, quizTitle);
  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/\/login$/);

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
