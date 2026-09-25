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

// #1340: the prefetched question's clock offset used to be computed only when
// the item was shown, after the feedback pause, so the read beat ran late by
// the length of the pause.
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
  // show straight away. With the offset taken after the pause they stayed
  // hidden for another 3s.
  await page.clock.runFor(3_500);
  await page.clock.runFor(300);
  await expect(page.getByRole('button', { name: QUIZ_QUESTIONS[1].options[0] })).toBeVisible({ timeout: 1_000 });
});
