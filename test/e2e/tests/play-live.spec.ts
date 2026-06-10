import { join } from 'node:path';

import { test, expect } from './fixtures';
import { seedQuiz, importQuiz, QUIZ_QUESTIONS, claimAndJoin, execSqlite } from './helpers';

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
  test('answers a question, waits with no correctness shown, then sees the revealed answer', async ({ page, hostSessions }) => {
    test.setTimeout(60_000);

    const quizTitle = `Live Play ${Date.now()}`;
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

    // A second, API-only player joins from its own anonymous context (its own
    // session cookie -> a distinct anonymous player) and holds its answer so
    // the question does not early-close the instant the page player answers.
    // The join carries no name (#716): it claims players.display_name first.
    const otherContext = await hostSessions.newPlayerContext();
    await claimAndJoin(otherContext.request, joinCode, robin);

    // Player joins via the deep link and lands in the lobby.
    await page.goto(`/join/${joinCode}`);
    await page.getByTestId('join-name-input').fill(quincy);
    await page.getByTestId('join-name-submit').click();
    await expect(page.getByTestId('lobby-roster').getByText(quincy)).toBeVisible();

    // Host starts the game immediately ("Start now"). The runner drives
    // round_intro -> question on its own beat.
    const startResp = await host.request.post(`/api/sessions/${joinCode}/start`);
    expect(startResp.status(), `start session: ${startResp.status()} ${await startResp.text()}`).toBe(204);

    // Round intro (#748): the between-rounds screen names the round about to
    // start. The seeded quiz lands every question in the default round titled
    // "Round 1", so the title shows it and the eyebrow reads "Round 1 of 1" -
    // never the old generic "next round" wording.
    await expect(page.getByTestId('round-intro')).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId('round-title')).toHaveText('Round 1');
    await expect(page.getByTestId('round-intro-eyebrow')).toHaveText('Round 1 of 1');

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
  });

  // #755 cross-surface contract (player half): the live round-intro card names
  // the round and words its heading correctly. A multi-round quiz with a round
  // summary exercises all three round-intro fields the player surface shares
  // with the TV (lobby.gohtml) and the solo client (index.html): the title, the
  // optional summary, and an accurate "Round N of M" eyebrow that is NOT the old
  // generic "Next round" wording on the first round. Asserting "Round 1 of 2"
  // (not the single-round "Round 1 of 1" the sibling spec checks) pins that N/M
  // reflects the real round position, so the first round can never read as a
  // between-rounds screen. The TV half is pinned in host-game.spec.ts; the
  // standings half is in standings-bargraph.spec.ts.
  test('round intro shows the round title, summary, and an accurate Round N of M heading', async ({ page, hostSessions }) => {
    test.setTimeout(60_000);

    const quizTitle = `Live Round Intro ${Date.now()}`;
    const roundSummary = 'Warm up with the easy ones first.';
    const robin = `Robin-${Date.now()}`;
    const quincy = `Quincy-${Date.now()}`;

    const host = await hostSessions.adminHost();

    // A two-round quiz, imported live: the first round carries a summary so the
    // optional copy is exercised, and the round count is 2 so the eyebrow reads
    // "Round 1 of 2".
    await importQuiz(host, {
      title: quizTitle,
      description: 'Live round-intro contract spec',
      rounds: [
        {
          title: 'Opening round',
          summary: roundSummary,
          questions: [
            { text: 'What is 2+2?', options: [
              { text: '3', correct: false },
              { text: '4', correct: true },
              { text: '5', correct: false },
              { text: '6', correct: false },
            ] },
          ],
        },
        {
          title: 'Closing round',
          questions: [
            { text: 'What is 3+3?', options: [
              { text: '5', correct: false },
              { text: '6', correct: true },
              { text: '7', correct: false },
              { text: '8', correct: false },
            ] },
          ],
        },
      ],
    }, 'live');
    // makeQuizLive re-asserts mode='live' (a no-op here) and returns the id the
    // session opener is addressed by.
    const quizID = makeQuizLive(quizTitle);

    const { joinCode } = await hostSessions.openViaApi(quizID);

    // A second, API-only player keeps the roster non-empty so the start is not
    // blocked and the runner advances into the round intro.
    const otherContext = await hostSessions.newPlayerContext();
    await claimAndJoin(otherContext.request, joinCode, robin);

    await page.goto(`/join/${joinCode}`);
    await page.getByTestId('join-name-input').fill(quincy);
    await page.getByTestId('join-name-submit').click();
    await expect(page.getByTestId('lobby-roster').getByText(quincy)).toBeVisible();

    const startResp = await host.request.post(`/api/sessions/${joinCode}/start`);
    expect(startResp.status(), `start session: ${startResp.status()} ${await startResp.text()}`).toBe(204);

    // Round intro: the card names the first round, shows its summary, and the
    // eyebrow reads "Round 1 of 2" - never "Next round" on the first round.
    await expect(page.getByTestId('round-intro')).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId('round-title')).toHaveText('Opening round');
    await expect(page.getByTestId('round-summary')).toHaveText(roundSummary);
    await expect(page.getByTestId('round-intro-eyebrow')).toHaveText('Round 1 of 2');
    await expect(page.getByTestId('round-intro')).not.toContainText('Next round');
  });
});
