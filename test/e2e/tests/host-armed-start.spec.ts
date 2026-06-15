import { join } from 'node:path';

import { test, expect } from './fixtures';
import type { HostSessions } from './fixtures';
import { execSqlite, seedQuiz, setQuizMode } from './helpers';
import type { Page } from '@playwright/test';

// host-armed last-call countdown (#735): a hosted session never auto-starts.
// The host either starts now, or arms a visible countdown shown to everyone
// (host TV + every player lobby). While armed the host can start now (skip) or
// cancel; at zero the runner starts the game. SESSION_START_COUNTDOWN is shrunk
// to 2s in playwright.config.ts so the fire path is observable but quick.

// openHostLobby seeds a live quiz as the shared admin, opens a session through
// the host-session factory (auto-ended on teardown), navigates the host to the
// big screen, and returns the host TV page plus the join code. The admin
// storageState passes the host gate; the player page stays anonymous.
async function openHostLobby(
  hostSessions: HostSessions,
  quizTitle: string,
): Promise<{ host: Page; joinCode: string }> {
  const host = await hostSessions.adminHost();

  await seedQuiz(host, quizTitle);
  setQuizMode(quizTitle, 'live');

  const { joinCode } = await hostSessions.openViaApi(quizIdFor(quizTitle));
  expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

  await host.goto(`/host/${joinCode}`);
  await expect(host.getByText(joinCode, { exact: true })).toBeVisible();

  return { host, joinCode };
}

// quizIdFor resolves a seeded quiz's id by title via the same sqlite shortcut
// the role/verify helpers use, so openViaApi can target it. Mirrors the helper
// in join-sign-in.spec.ts and trims the admin-page navigation the prior browser
// lookup did during setup - one fewer Playwright call during setup keeps the
// admin host context out of the "Target page closed" race the suite saw under
// full-suite load (#909).
function quizIdFor(title: string): number {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; cannot resolve a quiz id');
  }
  const dbFile = join(dataDir, `e2e-${test.info().parallelIndex}.db`);
  const escapedTitle = title.replace(/'/g, "''");
  const output = execSqlite(dbFile, `SELECT id FROM quizzes WHERE title = '${escapedTitle}';`);
  const id = Number.parseInt(output, 10);
  if (!Number.isInteger(id)) {
    throw new Error(`quizIdFor(${title}): could not resolve quiz id from sqlite output ${JSON.stringify(output)}`);
  }
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
  test('host arms the countdown, host + player see it, and the game auto-starts at zero', async ({ page, hostSessions }) => {
    test.setTimeout(60_000);
    const quizTitle = `Armed Fire ${Date.now()}`;
    const alice = `Alice-${Date.now()}`;

    const { host, joinCode } = await openHostLobby(hostSessions, quizTitle);
    await joinAsPlayer(page, joinCode, alice);

    // Before arming, the player lobby shows the static waiting hint.
    await expect(page.getByTestId('waiting-hint')).toBeVisible();

    // Host arms the last-call countdown.
    await host.getByTestId('arm-start').click();

    // Both surfaces show the live "Starting in M:SS" countdown.
    await expect(host.getByTestId('start-countdown-label')).toContainText('Starting in');
    await expect(page.getByTestId('start-countdown')).toContainText('Starting in');
    // While armed the static waiting hint is gone on the player surface.
    await expect(page.getByTestId('waiting-hint')).toHaveCount(0);

    // The countdown fires (SESSION_START_COUNTDOWN=2s): the runner starts the
    // game, so the player leaves the lobby into the round intro / first
    // question and the host TV countdown controls disappear. Wait on the
    // positive arrival of the player's question view rather than the lobby's
    // disappearance; SSE state propagation can exceed a tight budget under
    // full-suite load.
    await expect(page.getByTestId('question-view')).toBeVisible({ timeout: 25_000 });
    await expect(host.getByTestId('start-countdown')).toBeHidden();
  });

  test('Start now during the countdown begins the game immediately', async ({ page, hostSessions }) => {
    test.setTimeout(60_000);
    const quizTitle = `Armed Skip ${Date.now()}`;
    const bob = `Bob-${Date.now()}`;

    const { host, joinCode } = await openHostLobby(hostSessions, quizTitle);
    await joinAsPlayer(page, joinCode, bob);

    await host.getByTestId('arm-start').click();
    await expect(page.getByTestId('start-countdown')).toContainText('Starting in');

    // Start now skips the rest of the countdown; the game begins at once. Wait
    // on the player's question view appearing rather than the lobby's
    // disappearance, so SSE state propagation under full-suite load does not
    // race a tight budget.
    await host.getByTestId('skip-start').click();
    await expect(page.getByTestId('question-view')).toBeVisible({ timeout: 25_000 });
  });

  test('Cancel stops the countdown and the game stays in the lobby', async ({ page, hostSessions }) => {
    test.setTimeout(60_000);
    const quizTitle = `Armed Cancel ${Date.now()}`;
    const cara = `Cara-${Date.now()}`;

    const { host, joinCode } = await openHostLobby(hostSessions, quizTitle);
    await joinAsPlayer(page, joinCode, cara);

    await host.getByTestId('arm-start').click();
    await expect(page.getByTestId('start-countdown')).toContainText('Starting in');

    // Cancel clears the countdown: the player lobby returns to the waiting
    // hint and the game does not start.
    await host.getByTestId('cancel-start').click();
    await expect(page.getByTestId('start-countdown')).toHaveCount(0);
    await expect(page.getByTestId('waiting-hint')).toBeVisible();

    // Well past the original 2s deadline the game is still in the lobby.
    await page.waitForTimeout(4_000);
    await expect(page.getByTestId('lobby-view')).toBeVisible();
  });
});
