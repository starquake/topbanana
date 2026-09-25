import { join } from 'node:path';

import { test, expect } from './fixtures';
import type { Page } from './fixtures';
import { seedQuiz, execSqlite, waitForAlpineComponent } from './helpers';

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

// holdStateReads counts GET /state requests and holds each one until release()
// so the test can pile ticks up behind a pending read.
async function holdStateReads(page: Page, joinCode: string): Promise<{ count: () => number; release: () => void }> {
  let count = 0;
  let release: () => void = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  await page.route(`**/api/sessions/${joinCode}/state`, async (route) => {
    count++;
    await held;
    await route.continue();
  });
  return { count: () => count, release };
}

async function fireRefreshes(page: Page, selector: string, method: string, times: number): Promise<void> {
  await page.evaluate(({ selector, method, times }) => {
    const root = document.querySelector(selector);
    const cmp = (window as unknown as {
      Alpine: { $data: (el: Element) => Record<string, () => Promise<void>> };
    }).Alpine.$data(root!);
    for (let i = 0; i < times; i++) void cmp[method]();
  }, { selector, method, times });
}

// #1344: every tick fired its own GET /state with no coalescing, so a burst of
// ticks piled up overlapping reads.
test.describe('state read coalescing', () => {
  test('player ticks during a pending read cost one follow-up read', async ({ page, hostSessions }) => {
    test.setTimeout(60_000);
    const stamp = Date.now();
    const quizTitle = `Coalesce Player ${stamp}`;
    const eve = `Eve-${stamp}`;

    const host = await hostSessions.adminHost();
    await seedQuiz(host, quizTitle);
    const { joinCode } = await hostSessions.openViaApi(makeQuizLive(quizTitle));

    await page.goto(`/join/${joinCode}`);
    await page.getByTestId('join-name-input').fill(eve);
    await page.getByTestId('join-name-submit').click();
    await expect(page.getByTestId('lobby-roster').getByText(eve)).toBeVisible();
    await waitForAlpineComponent(page, '[x-data="joinApp"]', 'eventSource');

    const reads = await holdStateReads(page, joinCode);
    await fireRefreshes(page, '[x-data="joinApp"]', 'refreshState', 4);
    await expect.poll(() => reads.count()).toBe(1);
    reads.release();
    await expect.poll(() => reads.count()).toBe(2);
    await page.waitForTimeout(500);
    expect(reads.count()).toBe(2);
  });

  test('host ticks during a pending read cost one follow-up read', async ({ hostSessions }) => {
    test.setTimeout(60_000);
    const quizTitle = `Coalesce Host ${Date.now()}`;

    const host = await hostSessions.adminHost();
    await seedQuiz(host, quizTitle);
    const { joinCode } = await hostSessions.openViaApi(makeQuizLive(quizTitle));
    await host.goto(`/host/${joinCode}`);
    await waitForAlpineComponent(host, '[x-data^="hostBigScreen"]', 'source');

    const reads = await holdStateReads(host, joinCode);
    await fireRefreshes(host, '[x-data^="hostBigScreen"]', 'refresh', 4);
    await expect.poll(() => reads.count()).toBe(1);
    reads.release();
    await expect.poll(() => reads.count()).toBe(2);
    await host.waitForTimeout(500);
    expect(reads.count()).toBe(2);
    await host.unroute(`**/api/sessions/${joinCode}/state`);
  });
});
