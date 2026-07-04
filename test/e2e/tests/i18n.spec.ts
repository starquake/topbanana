import { test, expect } from './fixtures';

// #1115 multi-language: auto-detect from Accept-Language on the home page and
// SPA shell, and the footer switcher flipping the UI language and persisting the
// choice across a reload via the lang cookie.

test.describe('locale auto-detect from Accept-Language', () => {
  test.use({ locale: 'nl-NL' });

  test('renders Dutch on the home page and the SPA shell', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByRole('tab', { name: 'Populair' })).toBeVisible();

    await page.goto('/join');
    await expect(page.getByRole('heading', { name: 'Doe mee met een spel' })).toBeVisible();
    // The switcher marks the active locale so a player can tell which is live.
    await expect(page.getByTestId('lang-nl')).toHaveAttribute('aria-current', 'true');
  });
});

test.describe('locale defaults to English', () => {
  test.use({ locale: 'en-US' });

  test('renders English on the home page and the SPA shell', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByRole('tab', { name: 'Popular' })).toBeVisible();

    await page.goto('/join');
    await expect(page.getByRole('heading', { name: 'Join a game' })).toBeVisible();
    await expect(page.getByTestId('lang-en')).toHaveAttribute('aria-current', 'true');
  });
});

test.describe('footer language switcher', () => {
  test.use({ locale: 'en-US' });

  test('switching to Dutch persists across a reload', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByRole('tab', { name: 'Popular' })).toBeVisible();

    await page.getByTestId('lang-nl').click();
    await expect(page.getByRole('tab', { name: 'Populair' })).toBeVisible();

    // The cookie carries the choice, so a fresh load stays Dutch.
    await page.reload();
    await expect(page.getByRole('tab', { name: 'Populair' })).toBeVisible();
    await expect(page.getByTestId('lang-nl')).toHaveAttribute('aria-current', 'true');
  });
});
