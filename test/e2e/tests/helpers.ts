import type { Page } from '@playwright/test';
import { expect } from '@playwright/test';

export const PASSWORD = 'correctbatterystaple';

export type QuestionSpec = {
  text: string;
  options: [string, string, string, string];
  correctIndices: readonly number[];
  // Optional question image. When set, the player client renders <figure
  // class="image"><img>; the admin form persists the URL.
  imageUrl?: string;
  // Optional E2E expectation: did this question's image render (true) or did
  // it fail to load and get hidden by @error (false)? Undefined means the
  // question has no image and no <figure> is rendered at all.
  expectImageVisible?: boolean;
};

// 1x1 transparent PNG inlined as a data URL so the working-image case works
// without network access.
const TRANSPARENT_PNG_DATA_URL =
  'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkAAIAAAoAAv/lxKUAAAAASUVORK5CYII=';

// Four question variants exercised by both the admin and player E2E flows.
// Variant 1 has a deliberately-broken image; variant 2 has a working image
// rendered immediately afterwards. Together they prove the imageError state
// resets between consecutive image-bearing questions (the <img> element is
// reused across them, so a stale display:none would otherwise hide the
// second image too).
export const QUIZ_QUESTIONS: readonly QuestionSpec[] = [
  {
    text: 'What is 2+2?',
    options: ['3', '4', '5', '6'],
    correctIndices: [1],
    imageUrl: '/this-image-does-not-exist.png',
    expectImageVisible: false,
  },
  {
    text: 'Which animals are mammals?',
    options: ['cat', 'salmon', 'sparrow', 'lizard'],
    correctIndices: [],
    imageUrl: TRANSPARENT_PNG_DATA_URL,
    expectImageVisible: true,
  },
  { text: 'Pick a colour.',   options: ['red', 'blue', 'green', 'yellow'], correctIndices: [0, 1, 2, 3] },
  { text: 'Which are prime?', options: ['2', '3', '5', '9'],               correctIndices: [0, 1, 2] },
];

export async function registerAdmin(page: Page, username: string): Promise<void> {
  await page.goto('/register');
  await page.locator('input[name=username]').fill(username);
  await page.locator('input[name=password]').fill(PASSWORD);
  await page.locator('button[type=submit]').click();
  await expect(page).toHaveURL(/\/admin\/quizzes$/);
}

export async function createQuizWithQuestions(
  page: Page,
  title: string,
  questions: readonly QuestionSpec[] = QUIZ_QUESTIONS,
): Promise<void> {
  // Create the quiz; the save handler redirects to the quiz view at
  // /admin/quizzes/{id}, where each question is added in turn.
  await page.goto('/admin/quizzes/new');
  await page.locator('input[name=title]').fill(title);
  // The description is rendered as a <textarea> on the redesigned form,
  // not an <input>. The :is() selector keeps the helper resilient to either.
  await page.locator(':is(input, textarea)[name=description]').fill('E2E generated quiz');
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  for (const [index, q] of questions.entries()) {
    await page.getByRole('link', { name: /add question/i }).click();
    await expect(page).toHaveURL(/\/admin\/quizzes\/\d+\/questions\/new$/);

    // Question text became a <textarea> in the redesign.
    await page.locator(':is(input, textarea)[name=text]').fill(q.text);
    // Position is auto-assigned by the server now (#16) — no input field
    // on the question form. The index variable is kept on the for-of
    // signature so future helpers can use it without re-binding.
    void index;
    if (q.imageUrl !== undefined) {
      await page.locator('input[name=image_url]').fill(q.imageUrl);
    }
    for (let i = 0; i < q.options.length; i++) {
      await page.locator(`input[name="option[${i}].text"]`).fill(q.options[i]);
      if (q.correctIndices.includes(i)) {
        // The redesigned form hides the real checkbox (opacity: 0,
        // pointer-events: none) and exposes a styled <label class="option-check">
        // pill instead. Click the label to mirror what a real user does —
        // the browser propagates the click to the wrapped input. Drives
        // the actual user-facing affordance instead of force-clicking the
        // hidden control, so a regression in the label/input wiring would
        // surface here.
        const label = page.locator('label.option-check').nth(i);
        await label.scrollIntoViewIfNeeded();
        await label.click();
      }
    }
    await page.getByRole('button', { name: 'Save' }).click();
    await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  }
}

// playThroughQuiz walks the full quiz by clicking the first option on each
// question and waiting for the per-question feedback notification. Used by
// claim.spec.ts (and indirectly composes startQuizAsAnonymous +
// answerRemainingQuestions for tests that need to interleave behaviour).
export async function playThroughQuiz(page: Page, quizTitle: string): Promise<void> {
  await startQuizAsAnonymous(page, quizTitle);
  await answerRemainingQuestions(page);
}

// startQuizAsAnonymous navigates to the public /quizzes list, clicks the
// card matching quizTitle (which lands on /play/{slug-id}), and clicks
// Start Game. Replaces the pre-#284 dropdown-on-/client/ flow — the
// SPA's quiz picker was retired in favour of the dedicated list page.
// Stops before the first question's options are clicked so a caller
// can interleave timer/timeout behaviour between the start and the
// answer loop.
export async function startQuizAsAnonymous(page: Page, quizTitle: string): Promise<void> {
  await page.goto('/quizzes');
  // The card title is rendered as a stretched <a> so getByRole('link')
  // finds the play-deep-link anchor. Clicking it lands on the SPA
  // shell at /play/{slug-id} with the quiz pre-selected.
  await page.getByRole('link', { name: quizTitle }).click();
  // #312 — wait for Alpine's init() to have wired the start screen
  // before clicking. The "Leaderboard" heading is only emitted once
  // checkAlreadyPlayed has set this.quizSlugId, which happens at the
  // tail of init(). Without this gate Playwright on chromium races
  // Alpine's first reactivity tick: the button is in the DOM (visible,
  // not yet `:disabled` because the binding hasn't evaluated) so the
  // click "succeeds" but the @click handler isn't attached yet —
  // startGame() never fires, no POST /api/games leaves the wire, and
  // the page sits on the start screen until the toBeVisible budget
  // expires.
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
  await page.getByRole('button', { name: 'Start Game' }).click();
}

// answerRemainingQuestions clicks the first option for each question starting
// at fromIndex (default 0) and asserts the matching success/danger feedback.
// Waits for the leaderboard at the end so the caller can pick up immediately
// after the auto-advance from the final question. fromIndex lets timeout
// specs skip the questions that have already been resolved (e.g. via the
// timer-expired path).
export async function answerRemainingQuestions(page: Page, fromIndex = 0): Promise<void> {
  for (let i = fromIndex; i < QUIZ_QUESTIONS.length; i++) {
    const q = QUIZ_QUESTIONS[i];
    const choice = q.options[0];
    const wasCorrect = q.correctIndices.includes(0);

    const optionButton = page.getByRole('button', { name: choice });
    // Per-question wait must cover the prior feedback pause (up to 3s
    // on a wrong pick, #233) plus this question's reveal-countdown
    // beat (3s, #247). The default 5s toBeVisible timeout isn't
    // enough; 10s gives headroom for slow CI runners.
    await expect(optionButton).toBeVisible({ timeout: 10_000 });
    await optionButton.click();

    if (wasCorrect) {
      await expect(page.locator('.splash-correct')).toBeVisible();
    } else {
      await expect(page.locator('.splash-wrong')).toBeVisible();
    }
  }

  // The leaderboard renders after the last answer's auto-advance hits 404
  // on getNextQuestion. Generous timeout because the per-question feedback
  // delay adds up.
  await expect(page.getByRole('heading', { name: 'Game Finished!' })).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
}
