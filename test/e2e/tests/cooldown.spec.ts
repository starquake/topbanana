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

  // Drive the button into the disabled "Wait Ns" state WITHOUT assuming the
  // per-IP limiter starts clear. The forgot-password limiter is in-memory and
  // its 60s window is keyed on the source IP (127.0.0.1 for every worker), so
  // this spec's bucket is shared with the other browser project's run of the
  // same spec and with retries on the same worker server. The disabled state
  // renders only when a submit is *rejected* (a one-shot error flash); a fresh
  // browser context always GETs an enabled button, so the bucket's server-side
  // state - not the page - decides whether the FIRST submit is allowed or
  // already rejected.
  //
  // The old fixed "submit once to arm, submit again to trip" dance assumed a
  // clean bucket: when it was already armed (a prior run within 60s), the
  // first submit was itself rejected, the button went disabled, and the second
  // submit could not be clicked (disabled button + frozen virtual clock) - the
  // #643 flake. Instead: submit once, then submit again only if the button is
  // still enabled. Either path lands on the disabled state.
  await page.locator('input[name=identifier]').fill('nobody@example.test');
  await submit.click();
  await page.waitForURL(/\/forgot-password$/);
  await expect(submit).toBeVisible();

  if (await submit.isEnabled()) {
    // Bucket was clear: the first submit was allowed (success notice, button
    // still enabled). Submit again inside the window to trip the limiter.
    await page.locator('input[name=identifier]').fill('nobody@example.test');
    await submit.click();
    await page.waitForURL(/\/forgot-password$/);
  }

  // The stable locator always resolves, so these state assertions auto-wait
  // through the redirect and cooldown.js's relabel. Tolerate any countdown
  // value rather than the exact "Wait 60s" frame: a contaminating prior run
  // leaves a smaller remaining count, and the exact frame is racy under load.
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
