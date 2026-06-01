import { test, expect } from './fixtures';
import { registerForPending, login, markEmailVerified, seedQuiz, playThroughQuiz } from './helpers';
import { adminStatePath } from '../e2e-auth';

// Petname format: Title-cased Adjective-Adjective-Noun, e.g. "Steamy-Farty-Bear".
// EnsurePlayer middleware generates one of these for every fresh anonymous
// visitor on the first /api/players/me round-trip.
const PETNAME_PATTERN = /^[A-Z][a-z]+-[A-Z][a-z]+-[A-Z][a-z]+$/;

// Test 1 — petname card visible for fresh anonymous visitor.
test('start screen shows a Playing as card with an auto-generated petname for a fresh anonymous visitor', async ({ page }) => {
  // Playwright's default per-test context has fresh cookies, so navigating
  // to /client/ triggers EnsurePlayer to mint a new anonymous row whose
  // displayName is the generated petname.
  await page.goto('/client/');

  // Two `.claim-cta` blocks live in the DOM at once — start-screen and
  // leaderboard — because `x-show` on their parent toggles CSS, not
  // mount state. Scope to the visible one.
  const card = page.locator('.claim-cta:visible');
  await expect(card).toBeVisible();

  // Petname format: Adjective-Adjective-Noun, each segment Title-cased.
  const name = await card.getByTestId('claim-cta-name').textContent();
  expect(name).toMatch(PETNAME_PATTERN);

  // Button label defaults to the "no name picked yet" branch.
  await expect(page.getByRole('button', { name: 'Set your name' })).toBeVisible();
});

// Test 2 — claim updates the card in place, no navigation.
test('submitting a name via the start-screen modal updates the Playing as card in place', async ({ page, browserName }) => {
  await page.goto('/client/');

  // Capture the auto-generated petname so we can prove it was replaced.
  // Two `.claim-cta` nodes are mounted at once (start-screen + leaderboard,
  // both kept in DOM by `x-show`); scope to the visible one.
  const card = page.locator('.claim-cta:visible');
  await expect(card).toBeVisible();
  const petname = await card.getByTestId('claim-cta-name').textContent();
  expect(petname).toMatch(PETNAME_PATTERN);

  // Open the shared modal via the start-screen affordance.
  await page.getByRole('button', { name: 'Set your name' }).click();
  const modal = page.locator('[role="dialog"]');
  await expect(modal).toBeVisible();

  // Unique-per-run name so chromium and firefox don't collide on the shared
  // SQLite file, and reruns against a populated DB still work.
  const chosenName = `Claimed-${browserName}-${Date.now()}`;
  const input = modal.locator('input#claim-name-modal');
  await input.fill(chosenName);
  await modal.getByRole('button', { name: 'Save' }).click();

  // The modal closes on successful PATCH, and the card re-renders with the
  // chosen name plus the "already claimed" branch of the button label.
  await expect(modal).toBeHidden();
  await expect(card.getByTestId('claim-cta-name')).toHaveText(chosenName);
  await expect(card.getByTestId('claim-cta-name')).not.toHaveText(petname ?? '');
  await expect(page.getByRole('button', { name: 'Change your name' })).toBeVisible();
  // The "Set your name" span is also still in the DOM (gated by x-show), so
  // assert on its visibility rather than DOM count.
  await expect(page.getByRole('button', { name: 'Set your name' })).not.toBeVisible();

  // No navigation — still on /client/. Use a regex tolerant of an optional
  // trailing slash so the test doesn't depend on a redirect quirk.
  await expect(page).toHaveURL(/\/client\/?$/);
});

// Tests 3 and 4 seed a quiz as the shared admin via the JSON importer,
// then play it anonymously after clearing the admin cookie. The other
// claim tests in this file stay fully anonymous (no admin storageState).
test.describe('claim modal over a played quiz', () => {
  test.use({ storageState: adminStatePath() });

  // Test 3 — fresh anonymous visitor sees the claim modal auto-open after the
  // leaderboard renders.
  test('claim modal auto-opens on top of the leaderboard for a fresh anonymous visitor', async ({ page, browserName }) => {
    // A full anonymous playthrough spans four questions of feedback; setup
    // is one import, so a moderate budget suffices.
    test.setTimeout(45_000);

    const quizTitle = `E2E Claim Quiz ${browserName}`;

    await seedQuiz(page, quizTitle);
    await page.context().clearCookies();

    // Anonymous player walks the quiz. The pre-leaderboard claim card is
    // shown because the player is still on the auto-petname; once finished,
    // the modal auto-opens on top of the leaderboard.
    await playThroughQuiz(page, quizTitle);

    // Modal is visible — gate is `!hasCustomName()`, which is true for a fresh
    // anonymous visitor who never PATCHed /api/players/me.
    const modal = page.locator('[role="dialog"]');
    await expect(modal).toBeVisible();
    await expect(modal.locator('#claim-modal-title')).toHaveText('Pick a display name');
  });

  // Test 4 — an already-claimed visitor does NOT see the auto-modal after a
  // finished quiz. This is the regression #165 fixed: the prior gate
  // (`isAnonymous()` — i.e. "no password_hash") stayed true after a claim,
  // re-opening the modal every time. The corrected gate is `hasCustomName()`.
  test('claim modal does not auto-open for a visitor who already claimed a name', async ({ page, browserName }) => {
    test.setTimeout(45_000);

    const quizTitle = `E2E Claim Skip Quiz ${browserName}`;
    const chosenName = `Already-Claimed-${browserName}-${Date.now()}`;

    await seedQuiz(page, quizTitle);
    await page.context().clearCookies();

    // Visit /client/, claim a name via the start-screen modal, then play through.
    await page.goto('/client/');
    // Scope to :visible: both start-screen and leaderboard cards live in DOM
    // at the same time (x-show toggles CSS, not mount state).
    await expect(page.locator('.claim-cta:visible')).toBeVisible();
    await page.getByRole('button', { name: 'Set your name' }).click();

    const startModal = page.locator('[role="dialog"]');
    await expect(startModal).toBeVisible();
    await startModal.locator('input#claim-name-modal').fill(chosenName);
    await startModal.getByRole('button', { name: 'Save' }).click();
    await expect(startModal).toBeHidden();
    await expect(page.getByRole('button', { name: 'Change your name' })).toBeVisible();

    // Now play through. playThroughQuiz issues its own goto('/client/'), which
    // is fine — the session cookie keeps the same anonymous-but-claimed row.
    await playThroughQuiz(page, quizTitle);

    // The claim modal must NOT auto-open: the player already has a custom name,
    // so the `!hasCustomName()` gate in nextQuestion() short-circuits.
    await expect(page.locator('[role="dialog"]')).toHaveCount(0);

    // The leaderboard row for the current player should already show the
    // chosen name (no second claim step required). Match within the
    // aria-current row to ignore any other rows.
    const playerRow = page.locator('table tbody tr[aria-current="true"]');
    await expect(playerRow).toBeVisible();
    await expect(playerRow).toContainText(chosenName);
  });
});

// Test 4 — "Change your name" pre-fills the input with the current
// custom name so a small edit doesn't require retyping (#409).
test('Change your name modal pre-fills the input with the current display name', async ({ page, browserName }) => {
  await page.goto('/client/');

  // First claim a custom name so the "Change" branch is reachable.
  const chosenName = `Edit-${browserName}-${Date.now()}`;
  await page.getByRole('button', { name: 'Set your name' }).click();
  const firstModal = page.locator('[role="dialog"]');
  await firstModal.locator('input#claim-name-modal').fill(chosenName);
  await firstModal.getByRole('button', { name: 'Save' }).click();
  await expect(firstModal).toBeHidden();
  await expect(page.getByRole('button', { name: 'Change your name' })).toBeVisible();

  // Re-open via the "Change your name" affordance. The input must
  // start populated with the just-saved name instead of empty, so a
  // small edit (e.g. fixing a typo) does not require retyping the
  // whole thing.
  await page.getByRole('button', { name: 'Change your name' }).click();
  const editModal = page.locator('[role="dialog"]');
  await expect(editModal).toBeVisible();
  await expect(editModal.locator('input#claim-name-modal')).toHaveValue(chosenName);
});

// Test 5 — authenticated players never see the claim CTA (#409). Their
// displayName is stable and changes go through the future profile page
// (#410), so the in-game prompt would be noise. Registers as a plain
// player rather than relying on registerAdmin so the test is robust
// to ordering — other tests in the same e2e worker may have already
// claimed the "first registrant becomes admin" slot.
test('signed-in player does not see the claim-name CTA on the player client', async ({ page, browserName }) => {
  const displayName = `e2e-claim-authn-${browserName}-${Date.now()}`;
  // The hard gate (#574) means register no longer signs the player in.
  // Verify the row directly, then log in so the client sees an
  // authenticated player.
  await registerForPending(page, displayName);
  markEmailVerified(displayName);
  await login(page, displayName);

  await page.goto('/client/');

  // The "Playing as" card and both name buttons are gated on
  // !isAuthenticated(). A signed-in visitor sees neither, just the
  // bare start screen.
  await expect(page.locator('.claim-cta')).not.toBeVisible();
  await expect(page.getByRole('button', { name: 'Set your name' })).not.toBeVisible();
  await expect(page.getByRole('button', { name: 'Change your name' })).not.toBeVisible();
});

// Test 6 — sign-in CTA on the claim-name callout (#431). With registration
// enabled (set in playwright.config.ts) the callout offers both a Register
// link (/register) and a log-in link (/login).
test('claim-name callout includes Register and log-in links', async ({ page }) => {
  await page.goto('/client/');

  const card = page.locator('.claim-cta:visible');
  await expect(card).toBeVisible();

  const register = card.getByTestId('claim-cta-register');
  await expect(register).toBeVisible();
  await expect(register).toHaveAttribute('href', '/register');

  const signIn = card.getByTestId('claim-cta-signin');
  await expect(signIn).toBeVisible();
  await expect(signIn).toHaveAttribute('href', '/login');

  // Wait on the URL change because the link is plain navigation, not an Alpine event.
  await Promise.all([
    page.waitForURL(/\/login$/),
    signIn.click(),
  ]);
});

// Test 7 — modal does not duplicate the callout's sign-in CTA (#431).
test('claim modal does not include a sign-in link', async ({ page }) => {
  await page.goto('/client/');

  await page.getByRole('button', { name: 'Set your name' }).click();
  const modal = page.locator('[role="dialog"]');
  await expect(modal).toBeVisible();

  await expect(modal.locator('a[href="/login"]')).toHaveCount(0);
});
