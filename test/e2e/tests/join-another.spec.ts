import { join } from 'node:path';

import { adminStatePath } from '../e2e-auth';
import { test, expect } from './fixtures';
import { importQuiz, execSqlite } from './helpers';

// quizIdLive flips the named quiz to mode='live' and returns its id, mirroring
// the sqlite shortcut play-live.spec uses: only live quizzes are hostable
// (MP-0 / #677) and the session opener is addressed by id.
function quizIdLive(title: string): number {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; cannot mark a quiz live');
  }
  const dbFile = join(dataDir, `e2e-${test.info().parallelIndex}.db`);
  const escaped = title.replace(/'/g, "''");
  const output = execSqlite(
    dbFile,
    `UPDATE quizzes SET mode = 'live' WHERE title = '${escaped}'; SELECT id FROM quizzes WHERE title = '${escaped}';`,
  );
  const lines = output.split('\n');
  const id = Number.parseInt(lines[lines.length - 1], 10);
  if (!Number.isInteger(id)) {
    throw new Error(`quizIdLive(${title}): could not resolve quiz id from sqlite output ${JSON.stringify(output)}`);
  }
  return id;
}

// #828: after a finished live game the player can start a fresh join straight
// away via the "Join another quiz" button on the final standings, instead of
// being stranded there until the host tears the session down. The button
// forgets the remembered session and returns to the code-entry form, so the
// reloaded /join no longer resumes back into the finished standings.
test.describe('join another quiz after a finished game', () => {
  test('the finished standings offer a button back to a fresh join', async ({ page, baseURL }) => {
    test.setTimeout(60_000);

    const quizTitle = `Join Another ${Date.now()}`;
    const quincy = `Quincy-${Date.now()}`;

    const hostContext = await page.context().browser()!.newContext({ storageState: adminStatePath(), baseURL });
    const host = await hostContext.newPage();

    // A single-question live quiz reaches the finished phase after one answer:
    // with one player in the roster, the runner early-closes the question as
    // soon as that player answers, so the game falls straight through reveal to
    // the final standings.
    await importQuiz(host, {
      title: quizTitle,
      description: 'join-another spec',
      rounds: [{
        title: 'Round 1',
        questions: [{
          text: 'What is 2+2?',
          options: [
            { text: '3', correct: false },
            { text: '4', correct: true },
            { text: '5', correct: false },
            { text: '6', correct: false },
          ],
        }],
      }],
    }, 'live');
    const quizID = quizIdLive(quizTitle);

    const createResp = await host.request.post('/api/sessions', { data: { quizId: quizID } });
    expect(createResp.status(), `create session: ${createResp.status()} ${await createResp.text()}`).toBe(201);
    const { joinCode } = await createResp.json() as { joinCode: string };

    await page.goto(`/join/${joinCode}`);
    await page.getByTestId('join-name-input').fill(quincy);
    await page.getByTestId('join-name-submit').click();
    await expect(page.getByTestId('lobby-roster').getByText(quincy)).toBeVisible();

    const startResp = await host.request.post(`/api/sessions/${joinCode}/start`);
    expect(startResp.status(), `start session: ${startResp.status()} ${await startResp.text()}`).toBe(204);

    // Answer the only question once its options open (after the read beat), then
    // the runner advances through reveal to the finished phase.
    await expect(page.getByTestId('question-view')).toBeVisible({ timeout: 20_000 });
    const correct = page.getByTestId('question-options').getByRole('button', { name: '4', exact: true });
    await expect(correct).toBeVisible({ timeout: 10_000 });
    await correct.click();

    // Finished: the final standings render with the new "Join another quiz"
    // button.
    await expect(page.getByTestId('finished-view')).toBeVisible({ timeout: 20_000 });
    const joinAnother = page.getByTestId('join-another');
    await expect(joinAnother).toBeVisible();

    // Clicking it returns to the code-entry form for a fresh join and does NOT
    // resume back into the finished standings.
    await joinAnother.click();
    await expect(page).toHaveURL(/\/join\/?$/);
    await expect(page.getByTestId('join-code-input')).toBeVisible();
    await expect(page.getByTestId('finished-view')).toHaveCount(0);

    await hostContext.close();
  });
});
