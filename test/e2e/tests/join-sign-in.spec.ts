import { join } from 'node:path';

import { test, expect } from './fixtures';
import {
  PASSWORD,
  seedQuiz,
  setQuizMode,
  registerForPending,
  markEmailVerified,
  execSqlite,
} from './helpers';

// quizIdFor resolves a seeded quiz's id by title via the same sqlite shortcut
// the role/verify helpers use, so openViaApi can target it. setQuizMode flips
// the mode but returns void (the importer only makes solo quizzes, #677), so
// the id is read separately here.
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

// #865: an anonymous joiner asked for a display name can sign in instead. The
// Sign-in link carries a login deep-link return (/login?next=/join/{code}), so
// after a successful login the player lands back on the same room and JoinApp's
// init() resolves their account name and auto-joins under it - no ad-hoc
// anonymous name. The host setup (seed, mark live, open a room) runs as the
// shared admin via the hostSessions fixture, which ends the room on teardown;
// the player flow runs in the default anonymous page context.
test.describe('player join sign-in', () => {
  test('anonymous join name prompt offers sign-in; signing in returns to the room and joins under the account name', async ({ page, hostSessions }) => {
    // Generous budget: the flow registers + verifies an account, opens a live
    // room, navigates the login deep-link return, then waits for the auto-join
    // roster to settle off the SSE -> GET state loop.
    test.setTimeout(60_000);

    const stamp = Date.now();
    const quizTitle = `Live SignIn ${stamp}`;
    // Player names are global on players.display_name (#716), so a unique
    // stamped name avoids a collision with a parallel spec sharing the worker
    // DB. A chosen display name marks the row hasCustomName=true, which is what
    // makes the post-login auto-join land under the account name.
    const regName = `Reg-${stamp}`;

    // Register + verify the player on the anonymous page. Post-#574 register
    // hands out no session, so the page stays anonymous afterward - exactly the
    // state a fresh joiner is in when the name form is shown.
    await registerForPending(page, regName);
    markEmailVerified(regName);

    // Seed + host setup as the admin (storageState) in a separate context so
    // the player page stays anonymous. The importer only makes solo quizzes
    // (#677), so seed then flip the mode to live.
    const host = await hostSessions.adminHost();
    await seedQuiz(host, quizTitle);
    setQuizMode(quizTitle, 'live');

    const quizID = quizIdFor(quizTitle);
    const { joinCode } = await hostSessions.openViaApi(quizID);
    expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

    // The anonymous joiner reaches the name form via the deep link.
    await page.goto(`/join/${joinCode}`);
    await expect(page.getByTestId('join-name-input')).toBeVisible();

    // The Sign-in affordance is offered for the anonymous player.
    const signIn = page.getByTestId('join-name-signin');
    await expect(signIn).toBeVisible();

    // Following it navigates to login with the deep-link return back to this
    // exact room (URL-encoded).
    await signIn.click();
    await expect(page).toHaveURL(new RegExp(`/login\\?next=%2Fjoin%2F${joinCode}$`));

    // Sign in here, on the deep-link login page itself, so the next field
    // carried in the query survives into the form post (login() in helpers.ts
    // navigates to a bare /login, which would drop it).
    await page.locator('input[name=email]').fill(`${regName}@example.test`);
    await page.locator('input[name=password]').fill(PASSWORD);
    await page.locator('button[type=submit]').click();

    // The login 303s back to the room. JoinApp resolves /api/players/me, sees
    // the custom name, and auto-joins: the roster shows the account name and
    // the anonymous name form is gone. This proves the deep-link return plus a
    // join under the account name.
    await expect(page).toHaveURL(new RegExp(`/join/${joinCode}$`));
    const roster = page.getByTestId('lobby-roster');
    await expect(roster).toBeVisible();
    await expect(roster.getByText(regName)).toBeVisible();
    await expect(page.getByTestId('join-name-input')).toBeHidden();
  });
});
