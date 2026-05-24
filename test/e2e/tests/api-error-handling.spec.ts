import { test, expect, Route, Request } from './fixtures';
import {
  registerAdmin,
  createQuizWithQuestions,
  startQuizAsAnonymous,
  QUIZ_QUESTIONS,
} from './helpers';

// Covers issue #287: the player-client JS services used to call
// response.json() on every response, which threw SyntaxError on
// plain-text 4xx/5xx bodies. The retry banner — wired to that catch —
// fired for ANY non-2xx, including 400/404, which a re-click can't
// fix. The fix differentiates "retryable" (5xx, network) from
// "non-retryable" (4xx) and lets the timeout/advance path carry the
// game forward without showing a misleading banner.
test('400 on answer POST does not show the retry banner', async ({ page, browserName }) => {
  test.setTimeout(120_000);

  const adminUser = `e2e-admin-287-${browserName}`;
  const quizTitle = `E2E 287 ${browserName}`;

  await registerAdmin(page, adminUser);
  await createQuizWithQuestions(page, quizTitle);
  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/\/login$/);

  // Fail the first answers POST with a 400 (mimicking the real server
  // path for ErrOptionNotInQuestion). The point is "no banner for
  // non-retryable"; subsequent POSTs go through so the game advances.
  let answersPostCount = 0;
  await page.route(/\/api\/games\/[^/]+\/questions\/\d+\/answers$/, async (route: Route, request: Request) => {
    if (request.method() === 'POST' && answersPostCount === 0) {
      answersPostCount++;
      await route.fulfill({ status: 400, body: 'option not in question' });
      return;
    }
    await route.continue();
  });

  await startQuizAsAnonymous(page, quizTitle);

  const firstChoice = QUIZ_QUESTIONS[0].options[0];
  const firstButton = page.getByRole('button', { name: firstChoice });
  await expect(firstButton).toBeVisible({ timeout: 10_000 });
  await firstButton.click();

  // The retry banner uses role="alert". It must NOT appear for a
  // 400 — that's the headline regression #287 pins.
  const banner = page.getByRole('alert');
  await expect(banner).toBeHidden({ timeout: 3_000 });

  // The catch path synthesizes a timeout-style splash so the player
  // doesn't get stuck on a blank screen.
  await expect(page.locator('.splash-timeout')).toBeVisible({ timeout: 3_000 });

  // The game must advance to the next question (the splash auto-
  // advances after resolveAndAdvance's pause). Long timeout because
  // the timeout splash is intentionally held briefly before the
  // advance fires.
  const secondChoice = QUIZ_QUESTIONS[1].options[0];
  await expect(page.getByRole('button', { name: secondChoice })).toBeVisible({ timeout: 15_000 });
});

// 409 on POST /api/games used to crash startGame with SyntaxError
// (response.json() called on a plain-text body). The fix catches the
// ApiError, re-fetches getMyGameForQuiz, and uses the existing game
// id. The test fakes both endpoints because Playwright route
// interception is the cheapest way to drive the recovery branch.
test('409 on startGame recovers via getMyGameForQuiz', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const adminUser = `e2e-admin-287-conflict-${browserName}`;
  const quizTitle = `E2E 287 conflict ${browserName}`;

  await registerAdmin(page, adminUser);
  await createQuizWithQuestions(page, quizTitle);
  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/\/login$/);

  // Navigate via the public list (#284) so /api/players/me mints an
  // anonymous player and the resulting /play/{slug-id} pre-selects the
  // quiz. Wait for Alpine init's checkAlreadyPlayed to finish (its
  // /my-game + /leaderboard calls would otherwise hit the route stubs
  // set up below and corrupt the counts the test asserts on); the
  // Leaderboard heading is visible only AFTER the leaderboard fetch
  // resolves, so it's the right gating marker.
  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();

  // Intercept the start-game POST with a 409 (one shot) and let
  // /my-game return a real game synthesised on the fly. The test
  // doesn't need the recovered game to be ACTUALLY playable beyond
  // the next-question fetch; we just need the startGame catch path
  // to read existing.gameId and call gameService.getNextQuestion,
  // which we stub to 404 so the "no more questions" branch lands on
  // the leaderboard immediately. That's enough to prove the
  // SyntaxError crash is gone and the catch branch is reached.
  let createGameCount = 0;
  await page.route('**/api/games', async (route: Route, request: Request) => {
    if (request.method() !== 'POST') {
      await route.continue();
      return;
    }
    createGameCount++;
    await route.fulfill({
      status: 409,
      body: 'game already exists for this player and quiz',
    });
  });

  // First /my-game call (from checkAlreadyPlayed before Start) must
  // return 404 so the client goes down the startGame path. The
  // second call (the recovery after the 409) returns a synthesised
  // game so startGame can populate this.gameId.
  let myGameCount = 0;
  await page.route(/\/api\/quizzes\/[^/]+\/my-game$/, async (route: Route, request: Request) => {
    if (request.method() !== 'GET') {
      await route.continue();
      return;
    }
    myGameCount++;
    if (myGameCount === 1) {
      await route.fulfill({ status: 404, body: 'no game' });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ gameId: 'fake-recovered-game-id', completed: false }),
    });
  });

  // Stub the very next /questions/next so the recovered game advances
  // straight to "no more questions" → leaderboard. The 404 contract
  // is the standard server response when the last question has been
  // issued.
  await page.route(/\/api\/games\/fake-recovered-game-id\/questions\/next$/, async (route: Route) => {
    await route.fulfill({ status: 404, body: 'no more questions' });
  });

  // Stub the leaderboard call the no-more-questions branch fires.
  await page.route(/\/api\/quizzes\/[^/]+\/leaderboard$/, async (route: Route, request: Request) => {
    if (request.method() !== 'GET') {
      await route.continue();
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ quizId: 0, entries: [], currentPlayer: null }),
    });
  });

  // No console errors thrown — the pre-fix code logged a SyntaxError
  // here when response.json() hit "game already exists" text.
  const consoleErrors: string[] = [];
  page.on('pageerror', err => consoleErrors.push(err.message));

  await page.getByRole('button', { name: 'Start Game' }).click();

  // The startError state must NOT trigger — the catch path recovers.
  await expect(page.locator('text=/Couldn.t start the quiz/')).toBeHidden({ timeout: 3_000 });

  // The leaderboard view renders because the stubbed next-question
  // 404 sends the SPA there. Use the "Final leaderboard" heading the
  // template emits.
  await expect(page.locator('text=/leaderboard/i').first()).toBeVisible({ timeout: 5_000 });

  // Poll the counters instead of asserting once. The leaderboard
  // heading above matches the start-screen Leaderboard that's
  // already on screen pre-Start, so it isn't a gate on the 409
  // recovery completing — and firefox sometimes hasn't fired the
  // second /my-game probe by the time a single-shot assertion runs
  // (#332).
  await expect.poll(() => createGameCount, { timeout: 5_000 }).toBeGreaterThan(0);
  await expect.poll(() => myGameCount, { timeout: 5_000 }).toBeGreaterThanOrEqual(2);
  expect(consoleErrors, `page errors during recovery: ${consoleErrors.join(', ')}`).toEqual([]);
});
