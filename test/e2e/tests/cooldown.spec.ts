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

  // Stable handle on the single submit button. Its accessible name changes
  // as it counts down ("Send reset link" <-> "Wait Ns"), so locate it by type
  // and assert on state/text, not the moving name: a name-based locator returns
  // "element not found" in the window before cooldown.js relabels the button,
  // which flaked on loaded firefox runners (#643).
  const submit = page.locator('button[type="submit"]');
  await expect(submit).toBeEnabled();
  await expect(submit).toHaveText('Send reset link');

  // First submit succeeds and arms the limiter; the PRG redirect lands
  // back on /forgot-password with the opaque success notice.
  await page.locator('input[name=identifier]').fill('nobody@example.test');
  await submit.click();
  await expect(page).toHaveURL(/\/forgot-password$/);

  // Second submit inside the 60s window trips the limiter. The redirect
  // re-renders the page with the button disabled and showing a "Wait Ns"
  // countdown.
  await page.locator('input[name=identifier]').fill('nobody@example.test');
  await submit.click();
  // Wait for the PRG redirect to land before asserting, and give the
  // disabled-state checks a generous timeout: under firefox on a loaded CI
  // runner the redirect + render can exceed the default 5s, which flaked the
  // button-disabled assertion -- and once the first attempt failed, the armed
  // 60s per-IP limiter then doomed the retry. See #643.
  await page.waitForURL(/\/forgot-password$/);

  // The stable locator always resolves, so these state assertions auto-wait
  // through the redirect and cooldown.js's relabel. Tolerate any countdown
  // value rather than the exact "Wait 60s" frame, which is racy under load.
  await expect(submit).toBeDisabled({ timeout: 15_000 });
  await expect(submit).toHaveText(/^Wait \d+s$/, { timeout: 15_000 });

  // Advance past the full 60s cooldown without real waiting. runFor
  // (not fastForward) fires every intermediate 1s tick of cooldown.js's
  // setInterval -- fastForward jumps the clock and would fire a repeating
  // timer only once.
  await page.clock.runFor(61_000);

  // The button re-enables with the active label restored, no reload.
  await expect(submit).toBeEnabled();
  await expect(submit).toHaveText('Send reset link');
  await expect(submit).not.toHaveAttribute('aria-disabled', /.*/);
});
