import { test, expect } from './fixtures';
import { importQuiz, setQuizMode } from './helpers';

// #1050 — hosting a session needs the big-screen lobby, which does not fit on a
// phone, so on the host picker (/host/quizzes) the "Host this" button is hidden
// below the md breakpoint and a plain "use a larger screen" note takes its
// place. These specs pin that the right one shows at each width by visibility,
// not pixel geometry, so they stay deterministic.

const MOBILE = { width: 390, height: 844 } as const;
const WIDE = { width: 1280, height: 800 } as const;

// seedLiveQuiz seeds a quiz as the shared admin and flips it to mode='live' so it
// is hostable and therefore appears on the host picker. Returns the card's quiz
// id, read off the card's data-testid, so the host-action / note testids can be
// addressed exactly for this quiz (the picker may carry other live quizzes from
// parallel specs on the worker DB).
async function seedLiveQuiz(host: import('./fixtures').Page, title: string): Promise<string> {
  await importQuiz(host, {
    title,
    description: 'E2E seeded quiz',
    questions: [
      {
        text: 'What is 1+1?',
        options: [
          { text: '2', correct: true },
          { text: '1', correct: false },
          { text: '3', correct: false },
          { text: '4', correct: false },
        ],
      },
    ],
  });
  setQuizMode(title, 'live');

  await host.goto('/host/quizzes');
  const card = host.locator('article[data-testid^="quiz-card-"]').filter({ hasText: title });
  await expect(card).toBeVisible();
  const testid = await card.getAttribute('data-testid');
  return testid!.replace('quiz-card-', '');
}

test.describe('host picker host action by viewport (#1050)', () => {
  test('shows the larger-screen note instead of the host button on a phone', async ({ hostSessions }) => {
    const host = await hostSessions.adminHost();
    await host.setViewportSize(MOBILE);

    const id = await seedLiveQuiz(host, `Host Picker Mobile ${Date.now()}`);

    await expect(host.getByTestId(`host-needs-larger-screen-${id}`)).toBeVisible();
    await expect(host.getByTestId(`host-action-${id}`)).toBeHidden();
  });

  test('shows the host button and hides the note on a wide screen', async ({ hostSessions }) => {
    const host = await hostSessions.adminHost();
    await host.setViewportSize(WIDE);

    const id = await seedLiveQuiz(host, `Host Picker Wide ${Date.now()}`);

    await expect(host.getByTestId(`host-action-${id}`)).toBeVisible();
    await expect(host.getByTestId(`host-needs-larger-screen-${id}`)).toBeHidden();
  });
});
