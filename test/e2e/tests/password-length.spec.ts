import { test, expect } from './fixtures';

// password-length.js shows a live "too short" hint under the register
// page's new-password input. The threshold is auth.MinPasswordLength (13),
// rendered into the input's minlength and read back by the JS, so the hint
// stays in sync with the server-side rule.
test('register password field shows then clears the too-short hint', async ({ page }) => {
  await page.goto('/register');

  const password = page.locator('input[name=password]');
  const hint = page.locator('#password-length-hint');

  // Empty value: no hint.
  await expect(hint).toHaveText('');

  // Non-empty but under the minimum: the hint names the threshold.
  await password.fill('short');
  await expect(hint).toHaveText('Must be at least 13 characters.');

  // Long enough: the hint clears.
  await password.fill('correctbatterystaple');
  await expect(hint).toHaveText('');
});
