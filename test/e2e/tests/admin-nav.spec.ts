import { test, expect } from './fixtures';
import { registerAdmin } from './helpers';

// #517 — persistent admin top nav. The three real sections (Quizzes,
// Players, Email) must be reachable from any admin page via the navbar.
// Email was previously orphaned (linked only by typing the URL), so the
// nav link is the load-bearing addition this spec guards.
test('admin top nav reaches all three sections', async ({ page, browserName }) => {
  const username = `e2e-admin-nav-${browserName}`;

  await registerAdmin(page, username);

  // Scope to the primary nav so the links resolve to the navbar entries,
  // not in-page cards/buttons that happen to share a name.
  const nav = page.getByRole('navigation', { name: 'Primary' });

  await nav.getByRole('link', { name: 'Quizzes' }).first().click();
  await expect(page).toHaveURL(/\/admin\/quizzes$/);

  await nav.getByRole('link', { name: 'Players' }).first().click();
  await expect(page).toHaveURL(/\/admin\/players$/);

  // Email was orphaned before #517 — assert the nav links it and it loads.
  await nav.getByRole('link', { name: 'Email' }).first().click();
  await expect(page).toHaveURL(/\/admin\/email$/);
  await expect(page.getByRole('heading', { name: /email diagnostics/i })).toBeVisible();
});

test('admin nav marks the active section', async ({ page, browserName }) => {
  const username = `e2e-admin-nav-active-${browserName}`;

  await registerAdmin(page, username);

  const nav = page.getByRole('navigation', { name: 'Primary' });

  // registerAdmin lands on /admin/quizzes, so the Quizzes link is current.
  await page.goto('/admin/players');
  await expect(nav.getByRole('link', { name: 'Players' }).first()).toHaveAttribute('aria-current', 'page');
  await expect(nav.getByRole('link', { name: 'Quizzes' }).first()).not.toHaveAttribute('aria-current', 'page');
});
