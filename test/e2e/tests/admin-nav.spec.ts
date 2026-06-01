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
