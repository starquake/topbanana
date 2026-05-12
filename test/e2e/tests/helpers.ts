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
  await page.locator('input[name=description]').fill('E2E generated quiz');
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  for (const [index, q] of questions.entries()) {
    await page.getByRole('link', { name: /add question/i }).click();
    await expect(page).toHaveURL(/\/admin\/quizzes\/\d+\/questions\/new$/);

    await page.locator('input[name=text]').fill(q.text);
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
        // scrollIntoViewIfNeeded gives Firefox a frame to settle before the
        // click — without it CI occasionally surfaces "Clicking the checkbox
        // did not change its state" when the form's still styling under
        // CDN-loaded Bulma CSS.
        const checkbox = page.locator(`input[name="option[${i}].correct"]`);
        await checkbox.scrollIntoViewIfNeeded();
        await checkbox.check();
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

// startQuizAsAnonymous navigates to /client/, picks the named quiz from the
// dropdown, and clicks Start Game. Stops before the first question's options
// are clicked so a caller can interleave timer/timeout behaviour between the
// start and the answer loop.
export async function startQuizAsAnonymous(page: Page, quizTitle: string): Promise<void> {
  await page.goto('/client/');

  // Alpine fetches the quiz list asynchronously, so wait for our title.
  const select = page.locator('select');
  await expect(select.locator('option', { hasText: quizTitle })).toHaveCount(1);
  await select.selectOption({ label: quizTitle });
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
    await expect(optionButton).toBeVisible();
    await optionButton.click();

    if (wasCorrect) {
      await expect(page.locator('.notification.is-success')).toBeVisible();
    } else {
      await expect(page.locator('.notification.is-danger')).toBeVisible();
    }
  }

  // The leaderboard renders after the last answer's auto-advance hits 404
  // on getNextQuestion. Generous timeout because the per-question feedback
  // delay adds up.
  await expect(page.getByRole('heading', { name: 'Game Finished!' })).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
}
