import { test, expect } from './fixtures';
import { seedQuiz, setQuizMode } from './helpers';

// #1115 multi-language: auto-detect from Accept-Language on the home page and
// SPA shell, and the footer switcher flipping the UI language and persisting the
// choice across a reload via the lang cookie.

test.describe('locale auto-detect from Accept-Language', () => {
  test.use({ locale: 'nl-NL' });

  test('renders Dutch on the home page and the SPA shell', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByRole('tab', { name: 'Populair' })).toBeVisible();

    await page.goto('/join');
    await expect(page.getByRole('heading', { name: 'Doe mee met een spel' })).toBeVisible();
    // The switcher marks the active locale so a player can tell which is live.
    await expect(page.getByTestId('lang-nl')).toHaveAttribute('aria-current', 'true');
  });
});

test.describe('locale defaults to English', () => {
  test.use({ locale: 'en-US' });

  test('renders English on the home page and the SPA shell', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByRole('tab', { name: 'Popular' })).toBeVisible();

    await page.goto('/join');
    await expect(page.getByRole('heading', { name: 'Join a game' })).toBeVisible();
    await expect(page.getByTestId('lang-en')).toHaveAttribute('aria-current', 'true');
  });
});

test.describe('footer language switcher', () => {
  test.use({ locale: 'en-US' });

  test('switching to Dutch persists across a reload', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByRole('tab', { name: 'Popular' })).toBeVisible();

    await page.getByTestId('lang-nl').click();
    await expect(page.getByRole('tab', { name: 'Populair' })).toBeVisible();

    // The cookie carries the choice, so a fresh load stays Dutch.
    await page.reload();
    await expect(page.getByRole('tab', { name: 'Populair' })).toBeVisible();
    await expect(page.getByTestId('lang-nl')).toHaveAttribute('aria-current', 'true');
  });
});

// #1427: the switcher returns to the page it was clicked on, so a player is
// not dropped out of a live room or a solo game.
test.describe('footer language switcher keeps the player in place', () => {
  test.use({ locale: 'en-US' });

  test('a player in a live room lobby stays in the room', async ({ page, hostSessions, browserName }) => {
    test.setTimeout(45_000);
    const quizTitle = `Lang Live ${browserName} ${Date.now()}`;
    await seedQuiz(await hostSessions.adminHost(), quizTitle);
    setQuizMode(quizTitle, 'live');
    const { joinCode } = await hostSessions.hostLive(quizTitle);

    const playerName = `lang-${browserName}-${Date.now()}`;
    await page.goto(`/join/${joinCode}`);
    await page.getByTestId('join-name-input').fill(playerName);
    await page.getByTestId('join-name-submit').click();
    const roster = page.getByTestId('lobby-roster');
    await expect(roster.getByText(playerName)).toBeVisible();
    await expect(page.getByTestId('exit-session-open')).toHaveText('Exit session');

    await page.getByTestId('lang-nl').click();

    await expect(page).toHaveURL(new RegExp(`/join/${joinCode}$`));
    await expect(page.getByTestId('exit-session-open')).toHaveText('Sessie verlaten');
    await expect(roster.getByText(playerName)).toBeVisible();
    await expect(page.getByTestId('lang-nl')).toHaveAttribute('aria-current', 'true');
  });

  test('a player who typed a code on bare /join keeps it', async ({ page, hostSessions, browserName }) => {
    const quizTitle = `Lang Code ${browserName} ${Date.now()}`;
    await seedQuiz(await hostSessions.adminHost(), quizTitle);
    setQuizMode(quizTitle, 'live');
    const { joinCode } = await hostSessions.hostLive(quizTitle);

    await page.goto('/join');
    await page.getByTestId('join-code-input').fill(joinCode);
    await page.getByTestId('join-code-submit').click();
    await expect(page.getByTestId('join-name-input')).toBeVisible();
    await expect(page).toHaveURL(new RegExp(`/join/${joinCode}$`));

    await page.getByTestId('lang-nl').click();

    await expect(page).toHaveURL(new RegExp(`/join/${joinCode}$`));
    await expect(page.getByTestId('lang-nl')).toHaveAttribute('aria-current', 'true');
    await expect(page.getByTestId('join-name-input')).toBeVisible();
  });

  test('a mistyped code on bare /join is dropped from the URL', async ({ page }) => {
    await page.goto('/join');
    await page.getByTestId('join-code-input').fill('NOPE');
    await page.getByTestId('join-code-submit').click();
    await page.getByTestId('join-name-input').fill(`lang-miss-${Date.now()}`);
    await page.getByTestId('join-name-submit').click();

    await expect(page.getByTestId('join-code-input')).toHaveValue('NOPE');
    await expect(page).toHaveURL(/\/join\/$/);

    await page.getByTestId('lang-nl').click();

    await expect(page).toHaveURL(/\/join\/$/);
    await expect(page.getByTestId('lang-nl')).toHaveAttribute('aria-current', 'true');
    await expect(page.getByTestId('join-code-input')).toBeVisible();
  });

  test('a player on a solo quiz start screen stays on the quiz', async ({ page, hostSessions, browserName }) => {
    const quizTitle = `Lang Solo ${browserName} ${Date.now()}`;
    await seedQuiz(await hostSessions.adminHost(), quizTitle);

    await page.goto('/quizzes');
    await page.getByRole('link', { name: quizTitle }).click();
    await expect(page.getByRole('button', { name: 'Start Game' })).toBeVisible();
    const playURL = page.url();
    expect(new URL(playURL).pathname).toMatch(/^\/play\//);

    await page.getByTestId('lang-nl').click();

    await expect(page).toHaveURL(playURL);
    await expect(page.getByRole('button', { name: 'Start het spel' })).toBeVisible();
    await expect(page.getByText(quizTitle).first()).toBeVisible();
  });
});
