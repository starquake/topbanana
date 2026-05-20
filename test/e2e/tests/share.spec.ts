import { test, expect } from '@playwright/test';
import { registerAdmin, createQuizWithQuestions, startQuizAsAnonymous, answerRemainingQuestions } from './helpers';

// #176 — share buttons on the player client (start screen + finish
// screen) and on the public home page. Each surface composes a
// different message string and points at the same /play/{slug-id}
// deep link. The dialog opens for desktop browsers (no
// navigator.share) and the test interacts with it directly; on
// mobile browsers Playwright's default chromium/firefox projects
// behave like desktop, so navigator.share is undefined and the
// fallback dialog renders.

test('player client start screen has a share button that opens the dialog with invite text', async ({ page, browserName }) => {
  const username = `e2e-admin-share-start-${browserName}`;
  const quizTitle = `E2E Share Start Quiz ${browserName}`;

  await registerAdmin(page, username);
  await createQuizWithQuestions(page, quizTitle);

  // Drop the admin session and visit /client/ as an anonymous
  // player so the start screen renders.
  await page.context().clearCookies();
  await page.goto('/client/');

  // Pick the quiz from the dropdown. Without a selection the share
  // button is hidden (x-show="!!selectedQuizId"), so the click
  // would land on the wrong target.
  const select = page.locator('select');
  await expect(select.locator('option', { hasText: quizTitle })).toHaveCount(1);
  await select.selectOption({ label: quizTitle });

  const shareBtn = page.getByRole('button', { name: 'Share', exact: true });
  await expect(shareBtn).toBeVisible();
  await shareBtn.click();

  // The dialog renders an <h>-style label "Share" and the play URL
  // in the body. Match the URL substring so the test doesn't pin
  // the exact quiz ID.
  const dialog = page.locator('dialog.share-dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText('Share');
  await expect(dialog).toContainText('/play/');

  // The WhatsApp link must carry the invitation phrasing — encoded
  // because wa.me takes a single ?text= field. encodeURIComponent
  // emits %20 for spaces (not +), which is what we assert against.
  const whatsapp = dialog.locator('a[data-share-network="whatsapp"]');
  await expect(whatsapp).toHaveAttribute(
    'href',
    new RegExp(`wa\\.me/\\?text=.*Play%20this%20quiz%3A%20E2E%20Share%20Start%20Quiz%20${browserName}`),
  );
});

test('player client finish screen has a share button that includes the score', async ({ page, browserName }) => {
  const username = `e2e-admin-share-finish-${browserName}`;
  const quizTitle = `E2E Share Finish Quiz ${browserName}`;

  await registerAdmin(page, username);
  await createQuizWithQuestions(page, quizTitle);

  // Play the quiz through as anonymous so the finish screen
  // renders with a real `score` and `quizSlugId`.
  await page.context().clearCookies();
  await startQuizAsAnonymous(page, quizTitle);
  await answerRemainingQuestions(page);

  // An anonymous finisher gets the claim-name modal auto-opened on
  // top of the leaderboard (#165 gate). Dismiss with ESC so the
  // Share result button beneath becomes clickable.
  await page.keyboard.press('Escape');
  await expect(page.locator('[role="dialog"]')).toBeHidden();

  const shareBtn = page.getByRole('button', { name: 'Share result' });
  await expect(shareBtn).toBeVisible();
  await shareBtn.click();

  const dialog = page.locator('dialog.share-dialog');
  await expect(dialog).toBeVisible();

  // The Reddit submit link must carry the title-shaped brag text.
  // We don't pin the exact score number (it depends on timing in
  // the answer loop) — just that "I scored" and the quiz title
  // appear in the encoded title field. %20 is the space encoding
  // emitted by encodeURIComponent.
  const reddit = dialog.locator('a[data-share-network="reddit"]');
  await expect(reddit).toHaveAttribute('href', new RegExp(`reddit\\.com/submit.*title=I%20scored%20\\d+%20on%20E2E%20Share%20Finish%20Quiz%20${browserName}`));
});

test('home page popular-card share button opens the dialog with invitation text', async ({ page, browserName }) => {
  const username = `e2e-admin-share-home-${browserName}`;
  const quizTitle = `E2E Share Home Quiz ${browserName}`;

  // The home page only surfaces quizzes that have at least one
  // finished play in the last 30 days, so we need to author the
  // quiz AND play it through anonymously before the card appears.
  await registerAdmin(page, username);
  await createQuizWithQuestions(page, quizTitle);
  await page.context().clearCookies();
  await startQuizAsAnonymous(page, quizTitle);
  await answerRemainingQuestions(page);

  await page.goto('/');
  const card = page.locator('li', { has: page.getByRole('link', { name: quizTitle }) });
  await expect(card).toBeVisible();

  const shareBtn = card.getByRole('button', { name: new RegExp(`Share ${quizTitle}`) });
  await expect(shareBtn).toBeVisible();
  await shareBtn.click();

  const dialog = page.locator('dialog.share-dialog');
  await expect(dialog).toBeVisible();

  const whatsapp = dialog.locator('a[data-share-network="whatsapp"]');
  await expect(whatsapp).toHaveAttribute('href', new RegExp(`wa\\.me/\\?text=.*Play%20this%20quiz%3A%20E2E%20Share%20Home%20Quiz%20${browserName}`));
});
