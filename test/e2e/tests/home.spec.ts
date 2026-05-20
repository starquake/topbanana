import { test, expect } from '@playwright/test';

// #166 — the public start page at GET /. The test relies on nothing
// beyond what every project starts with: the page renders even with an
// empty database (empty-state messaging is part of the contract). Both
// the popular-quizzes and active-players sections must be present, and
// the discreet admin link in the footer must deep-link a logged-out
// visitor into the /login flow.
test('start page renders the popular + active sections and a discreet admin link', async ({ page }) => {
  await page.goto('/');

  // Title + brand wordmark.
  await expect(page).toHaveTitle('Top Banana!');
  await expect(page.getByRole('heading', { level: 1 })).toContainText(/Top\s*Banana!?/i);

  // Both section labels are <h2> for screen readers.
  await expect(page.getByRole('heading', { name: 'Popular quizzes', level: 2 })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Most active players', level: 2 })).toBeVisible();

  // Discreet admin link sits in the footer. Logged-out visitors get
  // redirected to /login by the admin middleware; we don't need to
  // assert anything beyond the link existing and being clickable here.
  const adminLink = page.getByRole('link', { name: 'Manage quizzes' });
  await expect(adminLink).toBeVisible();
  await adminLink.click();
  await expect(page).toHaveURL(/\/login$/);
});
