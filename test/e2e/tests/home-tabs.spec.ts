import { test, expect } from './fixtures';
import { seedQuiz } from './helpers';
import { adminStatePath } from '../e2e-auth';

// #601 — the start page's primary section is a Popular / Newest tab
// toggle over two server-rendered lists, switched client-side by Alpine.
// Seed a public quiz as the shared admin via the JSON importer, then
// drive the toggle anonymously. The seeded quiz has no plays, so it is
// absent from Popular but present in Newest (which orders by creation
// time and ignores play count), giving a card that is hidden under the
// default Popular tab and revealed by clicking Newest.
test.use({ storageState: adminStatePath() });

test('start page toggles between the Popular and Newest quiz lists', async ({ page, browserName }) => {
  const quizTitle = `E2E Newest Tab Quiz ${browserName}`;
  await seedQuiz(page, quizTitle);

  // Drop the admin session so the page renders as an anonymous visitor.
  await page.context().clearCookies();
  await page.goto('/');

  const popularTab = page.getByRole('tab', { name: 'Popular' });
  const newestTab = page.getByRole('tab', { name: 'Newest' });
  const popularPanel = page.locator('#panel-popular');
  const newestPanel = page.locator('#panel-newest');

  // Popular is the default tab: its panel shows, the newest panel is
  // hidden, and the seeded never-played quiz is not in the popular list.
  await expect(popularTab).toHaveAttribute('aria-selected', 'true');
  await expect(popularPanel).toBeVisible();
  await expect(newestPanel).toBeHidden();

  // Switch to Newest: the seeded quiz card appears and the popular
  // panel hides.
  await newestTab.click();
  await expect(newestTab).toHaveAttribute('aria-selected', 'true');
  await expect(newestPanel).toBeVisible();
  await expect(popularPanel).toBeHidden();
  await expect(newestPanel.getByRole('link', { name: quizTitle })).toBeVisible();
});
