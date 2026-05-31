import { test, expect } from './fixtures';

// The forgot-password POST handler enforces a 60s per-IP cooldown. Two
// submits inside that window make the second one trip the limiter: the
// PRG redirect re-renders the page with the submit button disabled and
// labelled "Wait 60s". cooldown.js should then tick that label down and
// re-enable the button at zero, with no page reload.
test('forgot-password cooldown button counts down and re-enables', async ({ page }) => {
  // Install the clock before any navigation so the page's setInterval
  // is driven by Playwright's virtual clock from first paint -- the
  // countdown then advances only when we fast-forward, never on the
  // wall clock.
  await page.clock.install();

  await page.goto('/forgot-password');

  const submit = page.getByRole('button', { name: 'Send reset link' });
  await expect(submit).toBeEnabled();

  // First submit succeeds and arms the limiter; the PRG redirect lands
  // back on /forgot-password with the opaque success notice.
  await page.locator('input[name=identifier]').fill('nobody@example.test');
  await submit.click();
  await expect(page).toHaveURL(/\/forgot-password$/);

  // Second submit inside the 60s window trips the limiter. The redirect
  // re-renders the page with the button disabled and showing "Wait 60s".
  await page.locator('input[name=identifier]').fill('nobody@example.test');
  await page.getByRole('button', { name: /^Wait \d+s$|^Send reset link$/ }).click();

  const cooldownButton = page.getByRole('button', { name: /^Wait \d+s$/ });
  await expect(cooldownButton).toBeDisabled();
  await expect(cooldownButton).toHaveText('Wait 60s');

  // Advance past the full 60s cooldown without real waiting. runFor
  // (not fastForward) fires every intermediate 1s tick of cooldown.js's
  // setInterval -- fastForward jumps the clock and would fire a repeating
  // timer only once.
  await page.clock.runFor(61_000);

  // The button re-enables and the active label returns, with no reload.
  const reEnabled = page.getByRole('button', { name: 'Send reset link' });
  await expect(reEnabled).toBeEnabled();
  await expect(reEnabled).not.toHaveAttribute('aria-disabled', /.*/);
});
