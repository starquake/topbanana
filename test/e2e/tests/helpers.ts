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
    await page.locator('input[name=position]').fill(String(index + 1));
    if (q.imageUrl !== undefined) {
      await page.locator('input[name=image_url]').fill(q.imageUrl);
    }
    for (let i = 0; i < q.options.length; i++) {
      await page.locator(`input[name="option[${i}].text"]`).fill(q.options[i]);
      if (q.correctIndices.includes(i)) {
        await page.locator(`input[name="option[${i}].correct"]`).check();
      }
    }
    await page.getByRole('button', { name: 'Save' }).click();
    await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);
  }
}
