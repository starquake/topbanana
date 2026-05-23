import { test, expect } from '@playwright/test';
import { registerAdmin, createQuizWithQuestions, playThroughQuiz } from './helpers';

// Petname format: Title-cased Adjective-Adjective-Noun, e.g. "Steamy-Farty-Bear".
// EnsurePlayer middleware generates one of these for every fresh anonymous
// visitor on the first /api/players/me round-trip.
const PETNAME_PATTERN = /^[A-Z][a-z]+-[A-Z][a-z]+-[A-Z][a-z]+$/;

// Test 1 — petname card visible for fresh anonymous visitor.
test('start screen shows a Playing as card with an auto-generated petname for a fresh anonymous visitor', async ({ page }) => {
  // Playwright's default per-test context has fresh cookies, so navigating
  // to /client/ triggers EnsurePlayer to mint a new anonymous row whose
  // username is the generated petname.
  await page.goto('/client/');

  // Two `.claim-cta` blocks live in the DOM at once — start-screen and
  // leaderboard — because `x-show` on their parent toggles CSS, not
  // mount state. Scope to the visible one.
  const card = page.locator('.claim-cta:visible');
  await expect(card).toBeVisible();

  // Petname format: Adjective-Adjective-Noun, each segment Title-cased.
  const name = await card.getByTestId('claim-cta-name').textContent();
  expect(name).toMatch(PETNAME_PATTERN);

  // Body copy and button label both default to the "no name picked yet" branch.
  await expect(card.getByText('Pick a display name', { exact: false })).toBeVisible();
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
  // chosen name plus the "already claimed" branch of body copy and button label.
  await expect(modal).toBeHidden();
  await expect(card.getByTestId('claim-cta-name')).toHaveText(chosenName);
  await expect(card.getByTestId('claim-cta-name')).not.toHaveText(petname ?? '');
  await expect(card.getByText('Not happy with it?', { exact: false })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Change your name' })).toBeVisible();
  // The "Set your name" span is also still in the DOM (gated by x-show), so
  // assert on its visibility rather than DOM count.
  await expect(page.getByRole('button', { name: 'Set your name' })).not.toBeVisible();

  // No navigation — still on /client/. Use a regex tolerant of an optional
  // trailing slash so the test doesn't depend on a redirect quirk.
  await expect(page).toHaveURL(/\/client\/?$/);
});

// Test 3 — fresh anonymous visitor sees the claim modal auto-open after the
// leaderboard renders.
test('claim modal auto-opens on top of the leaderboard for a fresh anonymous visitor', async ({ page, browserName }) => {
  // Four questions × ~2s feedback + admin setup overhead pushes this test
  // past Playwright's 30s default; match player.spec.ts's bump.
  test.setTimeout(60_000);

  const adminUser = `e2e-admin-claim-${browserName}`;
  const quizTitle = `E2E Claim Quiz ${browserName}`;

  // Admin setup: register, create the quiz, log out so the next steps
  // run anonymously.
  await registerAdmin(page, adminUser);
  await createQuizWithQuestions(page, quizTitle);
  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/\/login$/);

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
  test.setTimeout(60_000);

  const adminUser = `e2e-admin-claim-skip-${browserName}`;
  const quizTitle = `E2E Claim Skip Quiz ${browserName}`;
  const chosenName = `Already-Claimed-${browserName}-${Date.now()}`;

  await registerAdmin(page, adminUser);
  await createQuizWithQuestions(page, quizTitle);
  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/\/login$/);

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
