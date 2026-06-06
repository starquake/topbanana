import { test, expect } from './fixtures';
import {
  registerForPending,
  markEmailVerified,
  markAdmin,
  login,
  seedQuiz,
  setQuizMode,
} from './helpers';

// MP-10 (#687): when a player leaves, their row drops out of the live roster
// on the host/TV surface at once. Two players join from separate anonymous
// contexts; one leaves and the TV roster, which updates off the SSE-tick ->
// GET /state refresh, drops by one. The leave is driven through the REST
// endpoint directly: navigator.sendBeacon fires only on tab unload, which is
// awkward to trigger deterministically in Playwright, and the endpoint is the
// exact request the beacon issues.
test('a player leaving drops out of the host roster live', async ({
  page,
  context,
  baseURL,
  browserName,
}) => {
  const displayName = `e2e-leave-host-${browserName}`;
  const quizTitle = `E2E Session Leave ${browserName}`;

  await registerForPending(page, displayName);
  markEmailVerified(displayName);
  markAdmin(displayName);
  await login(page, displayName);
  await expect(page).toHaveURL(/\/admin\/quizzes$/);

  await seedQuiz(page, quizTitle);
  setQuizMode(quizTitle, 'live');

  await page.goto('/admin/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  await page.getByRole('button', { name: 'Play live' }).click();

  await expect(page).toHaveURL(/\/host\/[A-Z0-9]+$/);
  const code = page.url().split('/host/')[1];

  const aliceContext = await context.browser()!.newContext({ storageState: undefined, baseURL });
  const bobContext = await context.browser()!.newContext({ storageState: undefined, baseURL });
  try {
    const aliceJoin = await aliceContext.request.post(`/api/sessions/${code}/join`, {
      data: { displayName: 'Alice' },
    });
    expect(aliceJoin.status()).toBe(200);
    const bobJoin = await bobContext.request.post(`/api/sessions/${code}/join`, {
      data: { displayName: 'Bob' },
    });
    expect(bobJoin.status()).toBe(200);

    // Both rows show on the TV once the join ticks land.
    const roster = page.locator('[data-player-row]');
    await expect(roster).toHaveCount(2);

    // Alice leaves; the endpoint accepts an empty body (the sendBeacon shape).
    const leaveResp = await aliceContext.request.post(`/api/sessions/${code}/leave`);
    expect(leaveResp.status()).toBe(204);

    // The TV roster drops to just Bob without a reload.
    await expect(roster).toHaveCount(1);
    await expect(roster.first()).toContainText('Bob');
  } finally {
    await aliceContext.close();
    await bobContext.close();
  }
});
