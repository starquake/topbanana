import { test, expect, Route, Request } from './fixtures';
import {
  seedQuiz,
  startQuizAsAnonymous,
  installPlaythroughClock,
  QUIZ_QUESTIONS,
} from './helpers';
import { adminStatePath } from '../e2e-auth';

// Seed each quiz as the shared admin via the JSON importer, then clear the
// admin cookie so the error-handling flows run anonymous.
test.use({ storageState: adminStatePath() });

// Covers issue #287: the player-client JS services used to call
// response.json() on every response, which threw SyntaxError on
// plain-text 4xx/5xx bodies. The retry banner — wired to that catch —
// fired for ANY non-2xx, including 400/404, which a re-click can't
// fix. The fix differentiates "retryable" (5xx, network) from
// "non-retryable" (4xx) and lets the timeout/advance path carry the
// game forward without showing a misleading banner.
test('400 on answer POST does not show the retry banner', async ({ page, browserName }) => {
  test.setTimeout(45_000);

  // Date.now() keeps the title unique per attempt: a Playwright retry reuses
  // the same per-worker DB, so a fixed title would 409 on re-import (the failed
  // attempt already seeded it) and doom the retry (#908).
  const quizTitle = `E2E 287 ${browserName} ${Date.now()}`;

  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();
  // Install the virtual clock so the post-400 synthesized feedback
  // pause and the next question's reveal beat advance via
  // page.clock.runFor instead of wall time. The spec's regression
  // intent is that the catch path's setTimeout fires (so the game
  // advances) - the timer is preserved, just driven by virtual time.
  await installPlaythroughClock(page);

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

  // Stub Q2 on the post-400 /next call so the catch path's advance
  // lands on a fresh question instead of the server's resume-candidate
  // path returning Q1 again (the 400 short-circuited the POST before
  // the server saw it, so Q1's answer window is still open server-side
  // and GetNextQuestion hands it back until expiredAt elapses in real
  // wall time - a 14s detour that blew the 15s toBeVisible budget on
  // chromium under load, #908). Pinning startedAt 500ms in the past
  // also skips Q2's reveal beat: the SPA's startRevealCountdown
  // short-circuits to startCountdown when revealStart >= startAt.
  let nextCount = 0;
  await page.route(/\/api\/games\/[^/]+\/questions\/next$/, async (route: Route, request: Request) => {
    if (request.method() !== 'GET') {
      await route.continue();
      return;
    }
    nextCount++;
    if (nextCount === 1) {
      await route.continue();
      return;
    }
    const now = new Date();
    const startedAt = new Date(now.getTime() - 500).toISOString();
    const expiredAt = new Date(now.getTime() + 60_000).toISOString();
    const q2 = QUIZ_QUESTIONS[1];
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        type: 'question',
        id: 9_001,
        text: q2.text,
        options: q2.options.map((text, i) => ({ id: 9_100 + i, text })),
        startedAt,
        expiredAt,
        serverNow: now.toISOString(),
        position: 2,
        total: QUIZ_QUESTIONS.length,
      }),
    });
  });

  await startQuizAsAnonymous(page, quizTitle);

  // Fast-forward the per-question reveal beat (#247) so the option
  // button mounts under virtual time rather than wall time.
  await page.clock.runFor(2_500);
  const firstChoice = QUIZ_QUESTIONS[0].options[0];
  const firstButton = page.getByRole('button', { name: firstChoice });
  await expect(firstButton).toBeVisible({ timeout: 10_000 });
  await expect(firstButton).toBeEnabled({ timeout: 10_000 });
  await firstButton.click();

  // The retry banner uses role="alert". It must NOT appear for a
  // 400 — that's the headline regression #287 pins.
  const banner = page.getByRole('alert');
  await expect(banner).toBeHidden({ timeout: 3_000 });

  // The catch path synthesizes a timed-out feedback so the player
  // doesn't get stuck on a blank screen; the verdict eyebrow reads
  // "Time up" (#767).
  await expect(page.getByTestId('reveal-verdict')).toHaveText('Time up');

  // Fast-forward past the synthesized feedback pause so
  // resolveAndAdvance's setTimeout fires and the next question fetches.
  await page.clock.runFor(3_500);

  // The game must advance to the next question - the catch path's
  // timer fires under virtual time and the /next stub above hands
  // Q2 back with an already-elapsed reveal beat, so the buttons
  // mount on the next render frame.
  const secondChoice = QUIZ_QUESTIONS[1].options[0];
  await expect(page.getByRole('button', { name: secondChoice })).toBeVisible({ timeout: 10_000 });
});

// 409 on POST /api/games used to crash startGame with SyntaxError
// (response.json() called on a plain-text body). The fix catches the
// ApiError, re-fetches getMyGameForQuiz, and uses the existing game
// id. The test fakes both endpoints because Playwright route
// interception is the cheapest way to drive the recovery branch.
test('409 on startGame recovers via getMyGameForQuiz', async ({ page, browserName }) => {
  test.setTimeout(30_000);

  // Unique per attempt so a retry does not 409 on the seed (#908).
  const quizTitle = `E2E 287 conflict ${browserName} ${Date.now()}`;

  await seedQuiz(page, quizTitle);
  await page.context().clearCookies();

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
