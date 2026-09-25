import type { Page } from '@playwright/test';

import { test, expect, Route, Request } from './fixtures';
import {
  seedQuiz,
  startQuizAsAnonymous,
  installPlaythroughClock,
  QUIZ_QUESTIONS,
} from './helpers';
import { adminStatePath } from '../e2e-auth';

test.use({ storageState: adminStatePath() });

const NEXT_PATH = /\/api\/games\/[^/]+\/questions\/next$/;

// A prefetched question's clock offset is taken when the response lands, not
// after the feedback pause, so the read beat does not run late (#1340).
test('a prefetched question keeps the clock offset from when it arrived', async ({ page, browserName }) => {
  test.setTimeout(45_000);

  const quizTitle = `E2E 1340 ${browserName} ${Date.now()}`;
  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();
  await installPlaythroughClock(page);

  // The second /next (the prefetch fired when Q1's feedback lands) returns Q2
  // with a 3s read beat measured from the moment the response is sent.
  let nextCount = 0;
  let prefetchServed: () => void = () => {};
  const prefetched = new Promise<void>((resolve) => { prefetchServed = resolve; });
  await page.route(NEXT_PATH, async (route: Route, request: Request) => {
    if (request.method() !== 'GET') {
      await route.continue();
      return;
    }
    nextCount++;
    if (nextCount !== 2) {
      await route.continue();
      return;
    }
    const now = Date.now();
    const q2 = QUIZ_QUESTIONS[1];
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        type: 'question',
        id: 9_001,
        text: q2.text,
        options: q2.options.map((text, i) => ({ id: 9_100 + i, text })),
        startedAt: new Date(now + 3_000).toISOString(),
        expiredAt: new Date(now + 60_000).toISOString(),
        serverNow: new Date(now).toISOString(),
        position: 2,
        total: QUIZ_QUESTIONS.length,
      }),
    });
    prefetchServed();
  });

  await startQuizAsAnonymous(page, quizTitle);
  const firstButton = page.getByRole('button', { name: QUIZ_QUESTIONS[0].options[0] });
  await expect(async () => {
    await page.clock.runFor(500);
    await expect(firstButton).toBeEnabled({ timeout: 100 });
  }).toPass({ timeout: 10_000 });
  // A wrong pick holds feedback for 3s, the full length of Q2's read beat.
  await firstButton.click();
  await expect(page.getByTestId('reveal-verdict')).toHaveText('Not quite');
  await prefetched;

  // Past the feedback pause Q2's read beat has already elapsed, so its options
  // show straight away.
  await page.clock.runFor(3_500);
  await page.clock.runFor(300);
  await expect(page.getByRole('button', { name: QUIZ_QUESTIONS[1].options[0] })).toBeVisible({ timeout: 1_000 });
});

// A double tap on Start bootstraps one game, so no second reveal interval runs (#1341).
test('double-clicking Start creates one game and fetches one question', async ({ page, browserName }) => {
  test.setTimeout(30_000);

  const quizTitle = `E2E 1341 ${browserName} ${Date.now()}`;
  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();

  let createCount = 0;
  let nextCount = 0;
  page.on('request', (request) => {
    if (request.method() === 'POST' && new URL(request.url()).pathname === '/api/games') createCount++;
    if (request.method() === 'GET' && NEXT_PATH.test(new URL(request.url()).pathname)) nextCount++;
  });

  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  await page.getByRole('button', { name: 'Start Game' }).dblclick();

  await expect(page.getByText(QUIZ_QUESTIONS[0].text)).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole('button', { name: QUIZ_QUESTIONS[0].options[0] })).toBeVisible({ timeout: 10_000 });
  expect(createCount).toBe(1);
  expect(nextCount).toBe(1);
});

// stingListenerCount reports the end + stop listeners on the shared
// question-show Howl, or -1 before the Howl exists.
function stingListenerCount(page: Page): Promise<number> {
  return page.evaluate(() => {
    type HowlLike = { _src: string | string[]; _onend: unknown[]; _onstop: unknown[] };
    const howler = (window as unknown as { Howler?: { _howls: HowlLike[] } }).Howler;
    const sting = howler?._howls.find((h) => String(h._src).includes('question-show'));
    return sting ? sting._onend.length + sting._onstop.length : -1;
  });
}

// emitStingEvent fires end or stop for the sting's played sound the way Howler
// does; stop() itself is only queued on a Howl that is unloaded or play-locked.
async function emitStingEvent(page: Page, event: 'end' | 'stop'): Promise<void> {
  await page.evaluate((name) => {
    type HowlLike = {
      _src: string | string[];
      _onstop: { id?: number }[];
      _sounds: { _id: number }[];
      _emit: (event: string, id?: number) => unknown;
    };
    const howler = (window as unknown as { Howler: { _howls: HowlLike[] } }).Howler;
    const sting = howler._howls.find((h) => String(h._src).includes('question-show'));
    if (!sting) throw new Error('question-show Howl not found');
    const id = sting._onstop.find((l) => l.id)?.id ?? sting._sounds.at(-1)?._id;
    sting._emit(name, id);
  }, event);
}

// Once the question-show sting ends or is stopped, no end/stop listener stays
// on its shared Howl (#1344). The sting file is blocked so it never plays
// through on its own and the spec drives each event itself.
test.describe('question-show sting listeners', () => {
  test.use({ serviceWorkers: 'block' });

  for (const event of ['stop', 'end'] as const) {
    test(`a sting ${event} leaves no stale end/stop listeners`, async ({ page, browserName }) => {
      test.setTimeout(45_000);

      const quizTitle = `E2E 1344 sting ${event} ${browserName} ${Date.now()}`;
      await seedQuiz(page, quizTitle);
      await page.context().clearCookies();
      await page.route('**/static/audio/sfx/question-show.mp3', (route: Route) => route.abort());
      await startQuizAsAnonymous(page, quizTitle);
      await expect(page.getByText(QUIZ_QUESTIONS[0].text)).toBeVisible({ timeout: 10_000 });

      await expect.poll(() => stingListenerCount(page), { timeout: 10_000 }).toBe(2);
      await emitStingEvent(page, event);
      await expect.poll(() => stingListenerCount(page), { timeout: 5_000 }).toBe(0);
    });
  }
});
