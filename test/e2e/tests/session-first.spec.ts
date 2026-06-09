import { adminStatePath } from '../e2e-auth';
import { test, expect } from './fixtures';
import { seedQuiz, setQuizMode } from './helpers';
import type { Page } from '@playwright/test';

// session-first live hosting (#836): a host opens a live room BEFORE choosing a
// quiz. From the admin dashboard the host clicks "Host a session", lands on the
// empty-room lobby (no quiz armed), a player joins and sees the staging waiting
// hint, then the host picks the first quiz from the lobby's staging picker and
// the game runs. A second test covers the host ending the room from the lobby.
//
// The whole host flow is driven through the real browser (admin storageState
// passes the host gate) so the dashboard entry, the empty-room picker, and the
// End session control are all exercised end-to-end; the player page stays
// anonymous. Phase transitions are server-driven by the runner.

// seedLiveQuiz seeds a quiz as the shared admin and flips it to mode='live' so
// it is hostable (the importer only creates solo quizzes). The single-question
// shape lets one player drive the game to completion by answering once.
async function seedLiveQuiz(host: Page, title: string, questionText: string, correct: string): Promise<void> {
  await seedQuiz(host, title, [
    { text: questionText, options: [correct, 'wrong-a', 'wrong-b', 'wrong-c'], correctIndices: [0] },
  ]);
  setQuizMode(title, 'live');
}

// hostASession drives the dashboard "Host a session" entry through the browser:
// it opens the admin dashboard and clicks the empty-room control, landing on the
// host lobby. It returns the host page and the room's join code read off the
// lobby URL. The room is empty (no quiz armed) at this point.
async function hostASession(page: Page, baseURL: string | undefined): Promise<{
  host: Page;
  joinCode: string;
  close: () => Promise<void>;
}> {
  const hostContext = await page.context().browser()!.newContext({ storageState: adminStatePath(), baseURL });
  const host = await hostContext.newPage();

  await host.goto('/admin');
  await host.locator('[data-host-session-submit]').click();
  await expect(host).toHaveURL(/\/host\/[A-Z0-9]{6}$/);
  const joinCode = host.url().split('/host/')[1];
  expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

  return { host, joinCode, close: () => hostContext.close() };
}

// joinAsPlayer lands the anonymous page in the lobby via the deep link.
async function joinAsPlayer(page: Page, joinCode: string, name: string): Promise<void> {
  await page.goto(`/join/${joinCode}`);
  await page.getByTestId('join-name-input').fill(name);
  await page.getByTestId('join-name-submit').click();
  await expect(page.getByTestId('lobby-roster').getByText(name)).toBeVisible();
}

test.describe('session-first live hosting', () => {
  test('host opens an empty room, a player joins, then the host picks the first quiz and it runs', async ({ page, baseURL }) => {
    test.setTimeout(90_000);

    const stamp = Date.now();
    const quizTitle = `Session-first ${stamp}`;
    const player = `Pat-${stamp}`;

    const { host, joinCode, close } = await hostASession(page, baseURL);
    try {
      // The quiz the host will pick from the staging picker. Seeded after the
      // room is open to prove the room can stage before a quiz exists.
      await seedLiveQuiz(host, quizTitle, 'What is 1+1?', 'two');
      // Re-render the lobby so the freshly seeded quiz is in the picker (the
      // options are server-rendered at GET).
      await host.reload();

      // The empty room shows the staging picker, not the Start controls.
      await expect(host.locator('[data-start-quiz-picker]')).toBeVisible();
      await expect(host.locator('[data-start-now]')).toBeHidden();

      // A player joins the empty room and sees the staging waiting hint (no quiz
      // picked yet) rather than a broken screen.
      await joinAsPlayer(page, joinCode, player);
      await expect(page.getByTestId('waiting-hint')).toContainText('start a quiz');

      // The host picks the first quiz from the staging picker and starts it. The
      // native form submit 303-redirects back to the lobby, which the runner
      // drives into the first game.
      await host.locator('[data-next-quiz-select]').selectOption({ label: quizTitle });
      await host.locator('[data-next-quiz-submit]').click();

      // The still-joined player is carried into the game without re-entering a
      // code: they reach the question and the room is no longer empty.
      await expect(page.getByTestId('question-view')).toBeVisible({ timeout: 20_000 });
      await expect(page.getByTestId('question-text')).toHaveText('What is 1+1?');
      await expect(page.getByTestId('question-options')).toBeVisible({ timeout: 10_000 });
      await expect(
        page.getByTestId('question-options').getByRole('button', { name: 'two' }),
      ).toBeEnabled();
    } finally {
      await close();
    }
  });

  test('the host ends the session and the room closes for the joined player', async ({ page, baseURL }) => {
    test.setTimeout(90_000);

    const stamp = Date.now();
    const player = `Quinn-${stamp}`;

    const { host, joinCode, close } = await hostASession(page, baseURL);
    try {
      // A player joins the empty room.
      await joinAsPlayer(page, joinCode, player);
      await expect(page.getByTestId('waiting-hint')).toBeVisible();

      // The End session control carries a confirm guard; accept it so the submit
      // proceeds.
      host.once('dialog', (dialog) => dialog.accept());
      await host.locator('[data-end-session]').click();

      // The room is terminally closed: the still-joined player drops into the
      // finished view (the room ended out from under them).
      await expect(page.getByTestId('finished-view')).toBeVisible({ timeout: 20_000 });
    } finally {
      await close();
    }
  });
});
