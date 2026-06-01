import { test, expect } from './fixtures';
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
  // registerAdmin (register + verify + login) plus the four-question
  // quiz authoring runs well past the default 30s budget on a loaded
  // firefox CI worker (#585); match the playthrough specs' budget.
  test.setTimeout(90_000);
  const displayName = `e2e-admin-share-start-${browserName}`;
  const quizTitle = `E2E Share Start Quiz ${browserName}`;

  await registerAdmin(page, displayName);
  await createQuizWithQuestions(page, quizTitle);

  // Drop the admin session and navigate via the public list (#284)
  // so the deep-link /play/{slug-id} carries the quiz selection
  // forward without the retired dropdown.
  await page.context().clearCookies();
  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);

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
  // Heavy register + quiz authoring + full playthrough; see #585.
  test.setTimeout(90_000);
  const displayName = `e2e-admin-share-finish-${browserName}`;
  const quizTitle = `E2E Share Finish Quiz ${browserName}`;

  await registerAdmin(page, displayName);
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

// Regression for the "shared score is always 0" bug: revisiting an
// already-played quiz (or refreshing the page after finishing)
// loads the finished view via checkAlreadyPlayed but never restores
// the in-memory score counter. Sharing from that state used to
// brag about scoring zero; the fix reads the score from the loaded
// leaderboard payload instead. This spec exercises the revisit
// path: play through, navigate away, come back, share.
test('share-result reads score from the leaderboard so a revisit still brags the real number', async ({ page, browserName }) => {
  // Heavy register + quiz authoring + full playthrough; see #585.
  test.setTimeout(90_000);
  const displayName = `e2e-admin-share-revisit-${browserName}`;
  const quizTitle = `E2E Share Revisit Quiz ${browserName}`;

  await registerAdmin(page, displayName);
  await createQuizWithQuestions(page, quizTitle);

  // First play-through as anonymous so the score lands on the
  // leaderboard against the auto-petname player row.
  await page.context().clearCookies();
  await startQuizAsAnonymous(page, quizTitle);
  await answerRemainingQuestions(page);

  // Capture the score the player just earned so we can assert the
  // share text matches. The leaderboard row carrying the current
  // player is the canonical source — same row the share helper
  // now reads from.
  const myRow = page.locator('table.player-table tbody tr[aria-current="true"]');
  await expect(myRow).toBeVisible();
  const scoreText = await myRow.locator('td').nth(2).textContent();
  const score = (scoreText ?? '').trim();
  expect(score).not.toBe('');
  expect(score).not.toBe('0');

  // Navigate to a fresh session via the public list (#284) — this
  // drops the in-memory score counter but keeps the player cookie,
  // so the server still recognises the player as a finisher of this
  // quiz on the resulting /play/{slug-id} deep link.
  await page.goto('/quizzes');
  await page.getByRole('link', { name: quizTitle }).click();
  await expect(page).toHaveURL(/\/play\//);

  // The "Game Finished!" heading + leaderboard table confirm we
  // are on the already-played revisit path. The lockout banner
  // is intentionally hidden on this path (template gates it on
  // `startError && !finished`), so the heading is the canonical
  // visible marker.
  await expect(page.getByRole('heading', { name: 'Game Finished!' })).toBeVisible();
  await expect(page.locator('table.player-table')).toBeVisible();

  // Dismiss the claim modal if it auto-opened, then share.
  await page.keyboard.press('Escape');

  const shareBtn = page.getByRole('button', { name: 'Share result' });
  await expect(shareBtn).toBeVisible();
  await shareBtn.click();

  const dialog = page.locator('dialog.share-dialog');
  await expect(dialog).toBeVisible();

  // The WhatsApp link must carry the actual score, not zero. We
  // assert via the encoded "I%20scored%20<n>" prefix so the regex
  // pins the score value to the leaderboard reading captured above.
  const whatsapp = dialog.locator('a[data-share-network="whatsapp"]');
  await expect(whatsapp).toHaveAttribute('href', new RegExp(`wa\\.me/\\?text=I%20scored%20${score}%20on%20`));
});

test('home page popular-card share button opens the dialog with invitation text', async ({ page, browserName }) => {
  // Heavy register + quiz authoring + full playthrough; see #585.
  test.setTimeout(90_000);
  const displayName = `e2e-admin-share-home-${browserName}`;
  const quizTitle = `E2E Share Home Quiz ${browserName}`;

  // The home page only surfaces quizzes that have at least one
  // finished play in the last 30 days, so we need to author the
  // quiz AND play it through anonymously before the card appears.
  await registerAdmin(page, displayName);
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
