import { adminStatePath } from '../e2e-auth';
import { test, expect } from './fixtures';
import { seedQuiz, setQuizMode, endHostedSession } from './helpers';
import type { Page } from '@playwright/test';

// host-armed last-call countdown (#735): a hosted session never auto-starts.
// The host either starts now, or arms a visible countdown shown to everyone
// (host TV + every player lobby). While armed the host can start now (skip) or
// cancel; at zero the runner starts the game. SESSION_START_COUNTDOWN is shrunk
// to 2s in playwright.config.ts so the fire path is observable but quick.

// openHostLobby seeds a live quiz as the shared admin in its own context, opens
// a session, and returns the host TV page plus the join code. The admin
// storageState passes the host gate; the player page stays anonymous.
async function openHostLobby(
  page: Page,
  baseURL: string | undefined,
  quizTitle: string,
): Promise<{ host: Page; joinCode: string; close: () => Promise<void> }> {
  const hostContext = await page.context().browser()!.newContext({ storageState: adminStatePath(), baseURL });
  const host = await hostContext.newPage();

  await seedQuiz(host, quizTitle);
  setQuizMode(quizTitle, 'live');

  const createResp = await host.request.post('/api/sessions', { data: { quizId: await quizIdFor(host, quizTitle) } });
  expect(createResp.status(), `create session: ${createResp.status()} ${await createResp.text()}`).toBe(201);
  const { joinCode } = await createResp.json() as { joinCode: string };
  expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

  await host.goto(`/host/${joinCode}`);
  await expect(host.getByText(joinCode, { exact: true })).toBeVisible();

  return {
    host,
    joinCode,
    close: async () => {
      await endHostedSession(host, joinCode);
      await hostContext.close();
    },
  };
}

// quizIdFor resolves a seeded quiz's id via the admin quiz page URL so the
// session create can target it without a sqlite shortcut.
async function quizIdFor(host: Page, quizTitle: string): Promise<number> {
  await host.goto('/admin/quizzes');
  await host.getByRole('link', { name: quizTitle }).click();
  await expect(host).toHaveURL(/\/admin\/quizzes\/\d+$/);
  const id = Number.parseInt(host.url().split('/admin/quizzes/')[1], 10);
  expect(Number.isInteger(id)).toBeTruthy();

  return id;
}

// joinAsPlayer lands the anonymous page in the lobby via the deep link.
async function joinAsPlayer(page: Page, joinCode: string, name: string): Promise<void> {
  await page.goto(`/join/${joinCode}`);
  await page.getByTestId('join-name-input').fill(name);
  await page.getByTestId('join-name-submit').click();
  await expect(page.getByTestId('lobby-roster').getByText(name)).toBeVisible();
}

test.describe('host-armed last-call countdown', () => {
  test('host arms the countdown, host + player see it, and the game auto-starts at zero', async ({ page, baseURL }) => {
    test.setTimeout(60_000);
    const quizTitle = `Armed Fire ${Date.now()}`;
    const alice = `Alice-${Date.now()}`;

    const { host, joinCode, close } = await openHostLobby(page, baseURL, quizTitle);
    try {
      await joinAsPlayer(page, joinCode, alice);

      // Before arming, the player lobby shows the static waiting hint.
      await expect(page.getByTestId('waiting-hint')).toBeVisible();

      // Host arms the last-call countdown.
      await host.locator('[data-arm-start]').click();

      // Both surfaces show the live "Starting in M:SS" countdown.
      await expect(host.locator('[data-start-countdown-label]')).toContainText('Starting in');
      await expect(page.getByTestId('start-countdown')).toContainText('Starting in');
      // While armed the static waiting hint is gone on the player surface.
      await expect(page.getByTestId('waiting-hint')).toHaveCount(0);

      // The countdown fires (SESSION_START_COUNTDOWN=2s): the runner starts the
      // game, so the player leaves the lobby into the round intro / first
      // question and the host TV countdown controls disappear.
      await expect(page.getByTestId('lobby-view')).toHaveCount(0, { timeout: 15_000 });
      await expect(host.locator('[data-start-countdown]')).toBeHidden();
    } finally {
      await close();
    }
  });

  test('Start now during the countdown begins the game immediately', async ({ page, baseURL }) => {
    test.setTimeout(60_000);
    const quizTitle = `Armed Skip ${Date.now()}`;
    const bob = `Bob-${Date.now()}`;

    const { host, joinCode, close } = await openHostLobby(page, baseURL, quizTitle);
    try {
      await joinAsPlayer(page, joinCode, bob);

      await host.locator('[data-arm-start]').click();
      await expect(page.getByTestId('start-countdown')).toContainText('Starting in');

      // Start now skips the rest of the countdown; the game begins at once.
      await host.locator('[data-skip-start]').click();
      await expect(page.getByTestId('lobby-view')).toHaveCount(0, { timeout: 15_000 });
    } finally {
      await close();
    }
  });

  test('Cancel stops the countdown and the game stays in the lobby', async ({ page, baseURL }) => {
    test.setTimeout(60_000);
    const quizTitle = `Armed Cancel ${Date.now()}`;
    const cara = `Cara-${Date.now()}`;

    const { host, joinCode, close } = await openHostLobby(page, baseURL, quizTitle);
    try {
      await joinAsPlayer(page, joinCode, cara);

      await host.locator('[data-arm-start]').click();
      await expect(page.getByTestId('start-countdown')).toContainText('Starting in');

      // Cancel clears the countdown: the player lobby returns to the waiting
      // hint and the game does not start.
      await host.locator('[data-cancel-start]').click();
      await expect(page.getByTestId('start-countdown')).toHaveCount(0);
      await expect(page.getByTestId('waiting-hint')).toBeVisible();

      // Well past the original 2s deadline the game is still in the lobby.
      await page.waitForTimeout(4_000);
      await expect(page.getByTestId('lobby-view')).toBeVisible();
    } finally {
      await close();
    }
  });
});
