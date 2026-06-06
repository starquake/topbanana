import { execFileSync } from 'node:child_process';
import { join } from 'node:path';

import { adminStatePath } from '../e2e-auth';
import { test, expect } from './fixtures';
import { seedQuiz, QUIZ_QUESTIONS, claimAndJoin } from './helpers';

// makeQuizLive flips a seeded quiz to mode='live' (the importer lands quizzes
// on 'solo', and only live quizzes are hostable, MP-0 / #677) and returns its
// id so the test can open a session for it. Mirrors the sqlite3 shortcut the
// join spec uses rather than driving an admin live-mode toggle that does not
// exist in the seeded-import path.
function makeQuizLive(title: string): number {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; cannot mark a quiz live');
  }
  const dbFile = join(dataDir, `e2e-${test.info().parallelIndex}.db`);
  const escapedTitle = title.replace(/'/g, "''");
  const output = execFileSync('sqlite3', [
    dbFile,
    `UPDATE quizzes SET mode = 'live' WHERE title = '${escapedTitle}'; SELECT id FROM quizzes WHERE title = '${escapedTitle}';`,
  ], { encoding: 'utf8' });
  const lines = output.trim().split('\n');
  const id = Number.parseInt(lines[lines.length - 1], 10);
  if (!Number.isInteger(id)) {
    throw new Error(`makeQuizLive(${title}): could not resolve quiz id from sqlite output ${JSON.stringify(output)}`);
  }
  return id;
}

// MP-7 (#684): a player plays a synchronized question in a hosted live session.
// The host setup (seed + make live + open session + start) runs as the shared
// admin in a separate browser context so the player page itself stays
// anonymous. The phase transitions are driven server-side by the session
// runner; the player never advances them and only submits one answer.
//
// Two players join so the question stays open after the page-driven player
// answers: the runner early-closes only once every active player has answered.
// A second player who holds their answer keeps the question phase open long
// enough to assert the "answered, waiting" state with no correctness shown,
// then answers via the API to trigger the close -> reveal transition.
test.describe('player synchronized play', () => {
  test('answers a question, waits with no correctness shown, then sees the revealed answer', async ({ page, baseURL }) => {
    test.setTimeout(60_000);

    const quizTitle = `Live Play ${Date.now()}`;
    // Player names are global on players.display_name now (#716), so use
    // unique names to avoid colliding with a parallel spec on the worker DB.
    const robin = `Robin-${Date.now()}`;
    const quincy = `Quincy-${Date.now()}`;

    // Host side: seed the quiz, make it live, and open a session as the admin
    // (storageState) in its own context so the player page stays anonymous.
    const hostContext = await page.context().browser()!.newContext({ storageState: adminStatePath(), baseURL });
    const host = await hostContext.newPage();

    await seedQuiz(host, quizTitle);
    const quizID = makeQuizLive(quizTitle);

    const createResp = await host.request.post('/api/sessions', { data: { quizId: quizID } });
    expect(createResp.status(), `create session: ${createResp.status()} ${await createResp.text()}`).toBe(201);
    const { joinCode } = await createResp.json() as { joinCode: string };
    expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

    // A second, API-only player joins from its own anonymous context (its own
    // session cookie -> a distinct anonymous player) and holds its answer so
    // the question does not early-close the instant the page player answers.
    // The join carries no name (#716): it claims players.display_name first.
    const otherContext = await page.context().browser()!.newContext({ storageState: undefined, baseURL });
    await claimAndJoin(otherContext.request, joinCode, robin);

    // Player joins via the deep link and lands in the lobby.
    await page.goto(`/join/${joinCode}`);
    await page.getByTestId('join-name-input').fill(quincy);
    await page.getByTestId('join-name-submit').click();
    await expect(page.getByTestId('lobby-roster').getByText(quincy)).toBeVisible();

    // Host starts the game; a lobby stays put until this fires. The runner
    // then drives round_intro -> question on its own beat.
    const startResp = await host.request.post(`/api/sessions/${joinCode}/start`);
    expect(startResp.status(), `start session: ${startResp.status()} ${await startResp.text()}`).toBe(204);

    // The question view appears once the runner issues the first question. The
    // question text is the only spoiler-free signal the player gets.
    const firstQuestion = QUIZ_QUESTIONS[0];
    await expect(page.getByTestId('question-view')).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId('question-text')).toHaveText(firstQuestion.text);

    // Read beat (#247 parity): the question shows first with the options HIDDEN
    // and a "Get ready" indicator, so the player reads before answers open.
    await expect(page.getByTestId('question-read-beat')).toBeVisible();
    await expect(page.getByTestId('question-options')).toBeHidden();

    // After the read beat the answer window opens: the options appear and the
    // countdown bar drains from its full value off the server deadline (value
    // attribute drops below 100 within the answer window).
    await expect(page.getByTestId('question-options')).toBeVisible({ timeout: 10_000 });
    const progress = page.locator('[data-testid="question-view"] progress.progress');
    await expect(async () => {
      const value = Number(await progress.getAttribute('value'));
      expect(value).toBeLessThan(100);
    }).toPass({ timeout: 10_000 });

    // Pre-answer: no option carries a correctness marker (the server omits
    // correctOptionIds until reveal, so the buttons cannot leak the answer).
    const optionButtons = page.locator('[data-testid="question-options"] button');
    await expect(optionButtons).toHaveCount(firstQuestion.options.length);
    for (let i = 0; i < firstQuestion.options.length; i++) {
      await expect(optionButtons.nth(i)).not.toHaveAttribute('data-correct', 'true');
    }

    // Submit the one answer: pick option index 1 ('4'), the correct one.
    const correctText = firstQuestion.options[firstQuestion.correctIndices[0]];
    await page.getByTestId('question-options').getByRole('button', { name: correctText }).click();

    // Answered/waiting state: the lock-in message shows, the picked button is
    // flagged, and crucially NO correctness is revealed yet - the reveal view
    // is absent and no option is marked correct or wrong. The second player has
    // not answered, so the question phase holds here.
    await expect(page.getByTestId('answered-waiting')).toBeVisible();
    const pickedButton = page.getByTestId('question-options').getByRole('button', { name: correctText });
    await expect(pickedButton).toHaveAttribute('data-picked', 'true');
    await expect(page.getByTestId('reveal-view')).toHaveCount(0);
    for (let i = 0; i < firstQuestion.options.length; i++) {
      await expect(optionButtons.nth(i)).not.toHaveAttribute('data-correct', 'true');
    }

    // The second player now answers via the API. With every active player in,
    // the runner closes the question and enters reveal after its beat. The
    // reveal view shows the correct answer; the page player's correct pick is
    // verdict 'Correct!'. The second player's option id comes from the state
    // read (the wire shape exposes the option set without correctness).
    const stateResp = await otherContext.request.get(`/api/sessions/${joinCode}/state`);
    expect(stateResp.ok()).toBeTruthy();
    const otherState = await stateResp.json() as { question: { options: { id: number; text: string }[] } };
    const otherPick = otherState.question.options.find((o) => o.text === correctText);
    expect(otherPick, 'second player should see the option set without correctness').toBeTruthy();
    const otherAnswer = await otherContext.request.post(`/api/sessions/${joinCode}/answer`, {
      data: { optionId: otherPick!.id },
    });
    expect(otherAnswer.status()).toBe(204);

    await expect(page.getByTestId('reveal-view')).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId('reveal-verdict')).toHaveText('Correct!');

    const revealButtons = page.locator('[data-testid="reveal-options"] button');
    const correctReveal = page.getByTestId('reveal-options').getByRole('button', { name: correctText });
    await expect(correctReveal).toHaveAttribute('data-correct', 'true');
    // Exactly one option is marked correct for this single-correct question.
    await expect(revealButtons.and(page.locator('[data-correct="true"]'))).toHaveCount(1);

    await otherContext.close();
    await hostContext.close();
  });
});
