import { join } from 'node:path';

import { test, expect } from './fixtures';
import type { HostSessions } from './fixtures';
import { seedQuiz, registerForPending, login, markEmailVerified, execSqlite } from './helpers';

// A logged-in player who has chosen a custom name skips the name-entry form on
// the live-session join surface: they auto-join under their account name and
// land straight in the lobby. An anonymous visitor still sees the name form
// (unchanged).
//
// Each test seeds + hosts a session as the shared admin in a separate browser
// context, then drives the player join in this file's own context, mirroring
// join.spec.ts.

// makeQuizLive flips a seeded quiz to mode='live' (the importer lands quizzes
// on 'solo', and only live quizzes are hostable) and returns its id. Mirrors
// the sqlite3 shortcut join.spec.ts / play-live.spec.ts use.
function makeQuizLive(title: string): number {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; cannot mark a quiz live');
  }
  const dbFile = join(dataDir, `e2e-${test.info().parallelIndex}.db`);
  const escapedTitle = title.replace(/'/g, "''");
  const output = execSqlite(
    dbFile,
    `UPDATE quizzes SET mode = 'live' WHERE title = '${escapedTitle}'; SELECT id FROM quizzes WHERE title = '${escapedTitle}';`,
  );
  const lines = output.split('\n');
  const id = Number.parseInt(lines[lines.length - 1], 10);
  if (!Number.isInteger(id)) {
    throw new Error(`makeQuizLive(${title}): could not resolve quiz id from sqlite output ${JSON.stringify(output)}`);
  }
  return id;
}

// hostLiveSession seeds a quiz as the admin (host), flips it live, opens a
// session through the host-session factory, and returns the join code.
async function hostLiveSession(hostSessions: HostSessions, quizTitle: string): Promise<string> {
  const host = await hostSessions.adminHost();
  await seedQuiz(host, quizTitle);
  const quizID = makeQuizLive(quizTitle);
  const { joinCode } = await hostSessions.openViaApi(quizID);
  expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);
  return joinCode;
}

test.describe('player join name skip for named players', () => {
  test('a logged-in player with a custom name auto-joins via deep link, skipping the name form', async ({ page, hostSessions, browserName }) => {
    test.setTimeout(45_000);
    const quizTitle = `Named Join ${browserName} ${Date.now()}`;

    const joinCode = await hostLiveSession(hostSessions, quizTitle);

    // Sign in as a freshly registered player: registration claims a display
    // name, so this account has hasCustomName=true and isAuthenticated=true.
    const displayName = `e2e-named-${browserName}-${Date.now()}`;
    await registerForPending(page, displayName);
    markEmailVerified(displayName);
    await login(page, displayName);

    // Deep link straight to the room. The name form must never paint; the
    // player lands in the lobby joined under their account display name.
    await page.goto(`/join/${joinCode}`);

    const roster = page.getByTestId('lobby-roster');
    await expect(roster).toBeVisible();
    await expect(roster.getByText(displayName)).toBeVisible();
    // The name form lives under x-show (kept in DOM, toggled via CSS), so
    // assert it never became visible rather than that it is absent.
    await expect(page.getByTestId('join-name-input')).toBeHidden();

    // The authoritative state agrees the player joined under their account name.
    const stateResp = await page.request.get(`/api/sessions/${joinCode}/state`);
    expect(stateResp.ok()).toBeTruthy();
    const state = await stateResp.json() as { players: { displayName: string }[] };
    expect(state.players.some((p) => p.displayName === displayName)).toBe(true);
  });

  test('a logged-in named player entering the code on /join auto-joins without the name form', async ({ page, hostSessions, browserName }) => {
    test.setTimeout(45_000);
    const quizTitle = `Named Code ${browserName} ${Date.now()}`;

    const joinCode = await hostLiveSession(hostSessions, quizTitle);

    const displayName = `e2e-named-code-${browserName}-${Date.now()}`;
    await registerForPending(page, displayName);
    markEmailVerified(displayName);
    await login(page, displayName);

    // Bare /join: the player types the code, then auto-joins straight to the
    // lobby - the name form is skipped.
    await page.goto('/join');
    await page.getByTestId('join-code-input').fill(joinCode.toLowerCase());
    await page.getByTestId('join-code-submit').click();

    const roster = page.getByTestId('lobby-roster');
    await expect(roster).toBeVisible();
    await expect(roster.getByText(displayName)).toBeVisible();
    // The name form lives under x-show (kept in DOM, toggled via CSS), so
    // assert it never became visible rather than that it is absent.
    await expect(page.getByTestId('join-name-input')).toBeHidden();
  });

  test('an anonymous visitor still sees the name form on a deep link', async ({ page, hostSessions, browserName }) => {
    test.setTimeout(45_000);
    const quizTitle = `Anon Join ${browserName} ${Date.now()}`;

    const joinCode = await hostLiveSession(hostSessions, quizTitle);

    // Anonymous: no login. The name form must show on the deep link, and the
    // lobby must not appear until a name is submitted.
    await page.goto(`/join/${joinCode}`);
    await expect(page.getByTestId('join-name-input')).toBeVisible();
    await expect(page.getByTestId('lobby-roster')).toHaveCount(0);
  });
});
