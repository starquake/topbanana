import { join } from 'node:path';

import { test, expect } from './fixtures';
import type { Page } from './fixtures';
import { seedQuiz, claimAndJoin, execSqlite, waitForAlpineComponent } from './helpers';

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

type BeatState = { progress: number; revealing: boolean };

async function beatState(page: Page): Promise<BeatState> {
  return page.evaluate(() => {
    const root = document.querySelector('[x-data="joinApp"]');
    const cmp = (window as unknown as {
      Alpine: { $data: (el: Element) => { questionProgress: number; revealing: boolean } };
    }).Alpine.$data(root!);
    return { progress: cmp.questionProgress, revealing: cmp.revealing };
  });
}

// #1344: every state re-read during the read beat restarted the beat, so the
// bar jumped back to 0 on each tick.
test('a state re-read during the read beat does not reset the bar', async ({ page, hostSessions }) => {
  test.setTimeout(60_000);

  const stamp = Date.now();
  const quizTitle = `Read Beat ${stamp}`;
  const robin = `Robin-${stamp}`;
  const quincy = `Quincy-${stamp}`;

  const host = await hostSessions.adminHost();
  await seedQuiz(host, quizTitle);
  const { joinCode } = await hostSessions.openViaApi(makeQuizLive(quizTitle));

  const otherContext = await hostSessions.newPlayerContext();
  await claimAndJoin(otherContext.request, joinCode, robin);

  await page.goto(`/join/${joinCode}`);
  await page.getByTestId('join-name-input').fill(quincy);
  await page.getByTestId('join-name-submit').click();
  await expect(page.getByTestId('lobby-roster').getByText(quincy)).toBeVisible();
  await waitForAlpineComponent(page, '[x-data="joinApp"]', 'eventSource');

  const startResp = await host.request.post(`/api/sessions/${joinCode}/start`);
  expect(startResp.status()).toBe(204);

  await expect(page.getByTestId('question-read-beat')).toBeVisible({ timeout: 20_000 });
  await expect.poll(async () => (await beatState(page)).progress, { intervals: [50] }).toBeGreaterThan(15);
  const before = await beatState(page);
  expect(before.revealing).toBe(true);

  await page.evaluate(async () => {
    const root = document.querySelector('[x-data="joinApp"]');
    const cmp = (window as unknown as {
      Alpine: { $data: (el: Element) => { refreshState: () => Promise<void> } };
    }).Alpine.$data(root!);
    await cmp.refreshState();
  });

  const after = await beatState(page);
  if (after.revealing) {
    expect(after.progress).toBeGreaterThanOrEqual(before.progress);
  } else {
    expect(after.progress).toBeGreaterThan(90);
  }
});
