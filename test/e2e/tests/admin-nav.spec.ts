import { test, expect } from './fixtures';
import { adminStatePath } from '../e2e-auth';

// Reuse the shared seed-admin session instead of registering a fresh admin
// per test; these specs only need to be signed in as an admin.
test.use({ storageState: adminStatePath() });

// #517 / #582 — persistent admin top nav. The real sections (Quizzes,
// Players, Invites, Email) must be reachable from any admin page via the
// navbar. Email was orphaned before #517 and Invites before #582 (each
// linked only by typing the URL or the dashboard card), so the nav links
// are the load-bearing additions this spec guards.
test('admin top nav reaches all sections', async ({ page }) => {
  await page.goto('/admin/quizzes');

  // Scope to the primary nav so the links resolve to the navbar entries,
  // not in-page cards/buttons that happen to share a name.
  const nav = page.getByRole('navigation', { name: 'Primary' });

  await nav.getByRole('link', { name: 'Quizzes' }).first().click();
  await expect(page).toHaveURL(/\/admin\/quizzes$/);

  await nav.getByRole('link', { name: 'Players' }).first().click();
  await expect(page).toHaveURL(/\/admin\/players$/);

  // Invites was orphaned before #582 — assert the nav links it and it loads.
  await nav.getByRole('link', { name: 'Invites' }).first().click();
  await expect(page).toHaveURL(/\/admin\/invites$/);
  await expect(page.getByRole('heading', { name: 'Invites', level: 1 })).toBeVisible();

  // Email was orphaned before #517 — assert the nav links it and it loads.
  await nav.getByRole('link', { name: 'Email' }).first().click();
  await expect(page).toHaveURL(/\/admin\/email$/);
  await expect(page.getByRole('heading', { name: /email diagnostics/i })).toBeVisible();
});

// #732 — the "Signed in as <name>" text links to the profile page, and
// reaching it from the admin chrome lands the back link on the dashboard
// so the round-trip returns where it started.
test('admin "Signed in as" links to profile and returns to the dashboard', async ({ page }) => {
  await page.goto('/admin/quizzes');

  const nav = page.getByRole('navigation', { name: 'Primary' });
  // The display name is the only link inside the "Signed in as" label.
  await nav.locator('a[href="/profile?next=/admin"]').click();
  await expect(page).toHaveURL(/\/profile\?next=\/admin$/);

  // The profile back link points at the admin dashboard, not the home page.
  const back = page.getByRole('link', { name: 'Back to admin' });
  await expect(back).toBeVisible();
  await expect(back).toHaveAttribute('href', '/admin');

  await back.click();
  await expect(page).toHaveURL(/\/admin$/);
});

// #844 — Log out moved from the admin footer into the shared top bar's
// account cluster; it must stay a working POST form reachable on every
// admin page. The footer is metadata only now.
test('admin top bar logs the user out', async ({ page }) => {
  await page.goto('/admin/quizzes');

  const nav = page.getByRole('navigation', { name: 'Primary' });
  await nav.getByRole('button', { name: 'Log out' }).click();

  // Logout clears the session; admin pages now bounce to /login.
  await page.goto('/admin/quizzes');
  await expect(page).toHaveURL(/\/login/);
});

test('admin nav marks the active section', async ({ page }) => {
  const nav = page.getByRole('navigation', { name: 'Primary' });

  await page.goto('/admin/players');
  await expect(nav.getByRole('link', { name: 'Players' }).first()).toHaveAttribute('aria-current', 'page');
  await expect(nav.getByRole('link', { name: 'Quizzes' }).first()).not.toHaveAttribute('aria-current', 'page');

  // Invites highlights its own section too (#582).
  await page.goto('/admin/invites');
  await expect(nav.getByRole('link', { name: 'Invites' }).first()).toHaveAttribute('aria-current', 'page');
  await expect(nav.getByRole('link', { name: 'Players' }).first()).not.toHaveAttribute('aria-current', 'page');
});
