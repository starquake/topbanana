import { join } from 'node:path';

import { test, expect } from './fixtures';
import { seedQuiz, QUIZ_QUESTIONS, claimAndJoin, execSqlite } from './helpers';

// makeQuizLive flips a seeded quiz to mode='live' (the importer lands quizzes
// on 'solo', and only live quizzes are hostable, MP-0 / #677) and returns its
// id so the test can open a session for it. Mirrors the sqlite3 shortcut the
// other live specs use rather than driving an admin live-mode toggle that does
// not exist in the seeded-import path.
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

// MP-10 (#687): reconnect/resume hardening. A player who reloads mid-session
// lands back in the current phase without re-entering the code or name. The
// reload fires the beforeunload leave beacon (marking the row left_at), but
// resume re-Joins, and the server's resume gate revives a prior participant's
// row regardless of left_at - so the reload is harmless.
test.describe('reconnect and resume', () => {
  test('reloading mid-question lands back on the question and can still answer', async ({ page, hostSessions }) => {
    test.setTimeout(60_000);

    const quizTitle = `Resume Live ${Date.now()}`;
    // Player names are global on players.display_name now (#716), so use
    // unique names to avoid colliding with a parallel spec on the worker DB.
    const robin = `Robin-${Date.now()}`;
    const quincy = `Quincy-${Date.now()}`;

    // Host side: seed the quiz, make it live, and open a session as the admin
    // (storageState) in its own context so the player page stays anonymous.
    const host = await hostSessions.adminHost();
    await seedQuiz(host, quizTitle);
    const quizID = makeQuizLive(quizTitle);

    const { joinCode } = await hostSessions.openViaApi(quizID);
    expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

    // A second, API-only player joins from its own anonymous context and holds
    // its answer so the question never early-closes while the page player
    // reloads and resumes. The runner only closes once every active player has
    // answered.
    const otherContext = await hostSessions.newPlayerContext();
    await claimAndJoin(otherContext.request, joinCode, robin);

    // Player joins via the deep link and lands in the lobby (claims their name
    // through the shared flow, then joins nameless, #716). The landed code is
    // now remembered in localStorage for the resume on reload.
    await page.goto(`/join/${joinCode}`);
    await page.getByTestId('join-name-input').fill(quincy);
    await page.getByTestId('join-name-submit').click();
    await expect(page.getByTestId('lobby-roster').getByText(quincy)).toBeVisible();

    // Host starts the game; the runner drives round_intro -> question.
    const startResp = await host.request.post(`/api/sessions/${joinCode}/start`);
    expect(startResp.status(), `start session: ${startResp.status()} ${await startResp.text()}`).toBe(204);

    const firstQuestion = QUIZ_QUESTIONS[0];
    await expect(page.getByTestId('question-view')).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId('question-text')).toHaveText(firstQuestion.text);

    // Reload mid-question (the drop). beforeunload fires the leave beacon; the
    // reloaded page must resume by re-Joining with the remembered name (no
    // code/name form), landing straight back on the question.
    await page.reload();

    // Resume lands back on the question phase - neither the enter-code nor the
    // name form is shown (both are x-show toggled, so present-but-hidden) - with
    // the same question text. The countdown bar re-derives from the server
    // deadline, so it sits below 100 within the (still-open) window.
    await expect(page.getByTestId('join-code-input')).toBeHidden();
    await expect(page.getByTestId('join-name-input')).toBeHidden();
    await expect(page.getByTestId('question-view')).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId('question-text')).toHaveText(firstQuestion.text);

    const progress = page.locator('[data-testid="question-view"] progress.progress');
    await expect(async () => {
      const value = Number(await progress.getAttribute('value'));
      expect(value).toBeLessThan(100);
    }).toPass({ timeout: 10_000 });

    // The resumed player can still answer. Pick the correct option; the
    // answered/waiting state shows with no correctness leaked.
    const correctText = firstQuestion.options[firstQuestion.correctIndices[0]];
    await page.getByTestId('question-options').getByRole('button', { name: correctText }).click();
    await expect(page.getByTestId('answered-waiting')).toBeVisible();

    // The second player answers via the API; with every active player in, the
    // runner closes the question and the resumed page sees the reveal.
    const stateResp = await otherContext.request.get(`/api/sessions/${joinCode}/state`);
    expect(stateResp.ok()).toBeTruthy();
    const otherState = await stateResp.json() as { question: { options: { id: number; text: string }[] } };
    const otherPick = otherState.question.options.find((o) => o.text === correctText);
    expect(otherPick).toBeTruthy();
    const otherAnswer = await otherContext.request.post(`/api/sessions/${joinCode}/answer`, {
      data: { optionId: otherPick!.id },
    });
    expect(otherAnswer.status()).toBe(204);

    await expect(page.getByTestId('reveal-view')).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId('reveal-verdict')).toHaveText('Correct!');
  });

  test('a fresh visitor with no remembered session sees the code-entry form', async ({ page }) => {
    // No URL code and nothing in localStorage: the bare /join entry must show
    // the enter-code form, never a false resume into a room the visitor never
    // joined. The name form is present-but-hidden (x-show), so assert it is not
    // visible; the lobby view (x-if) is absent entirely.
    await page.goto('/join');
    await expect(page.getByTestId('join-code-input')).toBeVisible();
    await expect(page.getByTestId('join-name-input')).toBeHidden();
    await expect(page.getByTestId('lobby-view')).toHaveCount(0);
  });
});
