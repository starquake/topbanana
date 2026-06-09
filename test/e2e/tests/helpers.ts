import { join } from 'node:path';

import type { APIRequestContext } from '@playwright/test';

import type { Page } from './fixtures';
import { test, expect } from './fixtures';
import { execSqlite } from './sqlite';

// Re-exported so specs keep a single import hub (./helpers) for test utilities.
export { execSqlite };

export const PASSWORD = 'correctbatterystaple';

export type QuestionSpec = {
  text: string;
  options: [string, string, string, string];
  correctIndices: readonly number[];
};

// Four question variants exercised by both the admin and player E2E flows.
// Image-bearing variants were removed alongside the hidden UI in #426;
// re-add them when the image feature work resumes.
export const QUIZ_QUESTIONS: readonly QuestionSpec[] = [
  { text: 'What is 2+2?',          options: ['3', '4', '5', '6'],                correctIndices: [1] },
  { text: 'Which animals are mammals?', options: ['cat', 'salmon', 'sparrow', 'lizard'], correctIndices: [] },
  { text: 'Pick a colour.',        options: ['red', 'blue', 'green', 'yellow'], correctIndices: [0, 1, 2, 3] },
  { text: 'Which are prime?',      options: ['2', '3', '5', '9'],               correctIndices: [0, 1, 2] },
];

export async function registerAdmin(page: Page, displayName: string): Promise<void> {
  await registerForPending(page, displayName);
  // The hard email-verification gate (#574) means register no longer
  // hands out a session. Stamp email_verified_at directly (same trick
  // home.spec.ts uses to wipe games) rather than clicking the emailed
  // link: this is the setup shortcut for the many specs that just need a
  // signed-in admin. The real link round-trip is covered on its own in
  // email-roundtrip.spec.ts.
  markEmailVerified(displayName);
  // Stamp the admin role directly too. Admin promotion for an ADMIN_EMAILS
  // address now happens when the verify token is consumed via the endpoint
  // (#785); this direct email_verified_at stamp bypasses that endpoint, so the
  // role would otherwise never be granted. Set it explicitly rather than
  // relying on the register-time promotion the shortcut no longer triggers.
  markAdmin(displayName);
  await login(page, displayName);
  await expect(page).toHaveURL(/\/admin\/quizzes$/);
}

// registerForPending fills and submits the register form, then asserts
// the post-#574 hard-gate outcome: register renders the "verify your
// email" confirmation at /register with no session (no redirect). The
// row now exists but the visitor is not signed in. Email is the
// credential after #446; displayName is the optional display name.
export async function registerForPending(page: Page, displayName: string): Promise<void> {
  await page.goto('/register');
  await page.locator('input[name=email]').fill(`${displayName}@example.test`);
  await page.locator('input[name=display_name]').fill(displayName);
  await page.locator('input[name=password]').fill(PASSWORD);
  await page.locator('input[name=password_confirm]').fill(PASSWORD);
  await page.locator('button[type=submit]').click();
  await expect(page).toHaveURL(/\/register$/);
  await expect(page.getByRole('heading', { name: 'Verify your email' })).toBeVisible();
}

// login submits the login form for the named account. The caller asserts
// the post-login landing URL (it varies by role). LOGIN_COOLDOWN is set
// to 0s in playwright.config.ts, so back-to-back logins in a worker do
// not trip the per-IP rate limiter.
export async function login(page: Page, displayName: string): Promise<void> {
  await page.goto('/login');
  await page.locator('input[name=email]').fill(`${displayName}@example.test`);
  await page.locator('input[name=password]').fill(PASSWORD);
  await page.locator('button[type=submit]').click();
}

export function markEmailVerified(displayName: string): void {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; helpers cannot stamp email_verified_at');
  }
  const dbFile = join(dataDir, `e2e-${test.info().parallelIndex}.db`);
  // The sqlite3 CLI's `.parameter set` evaluates its value as SQL, so it
  // doesn't actually neutralise an injection payload on its own. Escape
  // the single quote here instead -- standard SQL literal escaping, one
  // line, no extra round-trip through the CLI's parameter parser.
  const escapedDisplayName = displayName.replace(/'/g, "''");
  const output = execSqlite(
    dbFile,
    `UPDATE players SET email_verified_at = CURRENT_TIMESTAMP WHERE display_name = '${escapedDisplayName}'; SELECT changes();`,
  );
  const changed = Number.parseInt(output, 10);
  if (changed !== 1) {
    throw new Error(`markEmailVerified(${displayName}): expected 1 row updated, got ${changed}`);
  }
}

// markAdmin sets role='admin' (the top tier) for the named player by
// shelling out to the sqlite3 CLI, mirroring how the production promote
// path mutates the row. The e2e suite has no Admin out of the box, so
// this is the bootstrap the settings/role specs use.
export function markAdmin(displayName: string): void {
  setRole(displayName, 'admin');
}

// markHost sets role='host' (the middle tier) for the named player.
export function markHost(displayName: string): void {
  setRole(displayName, 'host');
}

function setRole(displayName: string, role: 'player' | 'host' | 'admin'): void {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; helpers cannot stamp role');
  }
  const dbFile = join(dataDir, `e2e-${test.info().parallelIndex}.db`);
  const escapedDisplayName = displayName.replace(/'/g, "''");
  const output = execSqlite(
    dbFile,
    `UPDATE players SET role = '${role}' WHERE display_name = '${escapedDisplayName}'; SELECT changes();`,
  );
  const changed = Number.parseInt(output, 10);
  if (changed !== 1) {
    throw new Error(`setRole(${displayName}, ${role}): expected 1 row updated, got ${changed}`);
  }
}

// setQuizMode flips a quiz's play mode by title, shelling out to the
// sqlite3 CLI the same way the role/verification helpers do. The admin
// importer only creates solo quizzes (#677), so a host-lobby spec that
// needs a live quiz seeds one solo and flips it here.
export function setQuizMode(title: string, mode: 'solo' | 'live'): void {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; helpers cannot stamp quiz mode');
  }
  const dbFile = join(dataDir, `e2e-${test.info().parallelIndex}.db`);
  const escapedTitle = title.replace(/'/g, "''");
  const output = execSqlite(
    dbFile,
    `UPDATE quizzes SET mode = '${mode}' WHERE title = '${escapedTitle}'; SELECT changes();`,
  );
  const changed = Number.parseInt(output, 10);
  if (changed !== 1) {
    throw new Error(`setQuizMode(${title}, ${mode}): expected 1 row updated, got ${changed}`);
  }
}

// csrfTokenPattern scrapes the hidden csrf_token input a server-rendered
// form carries (the import form, the login form, ...). Tolerant of attribute
// order: the value may sit before or after the name attribute on the input.
export const csrfTokenPattern =
  /<input[^>]*\bname="csrf_token"[^>]*\bvalue="([^"]*)"|<input[^>]*\bvalue="([^"]*)"[^>]*\bname="csrf_token"/;

type ImportOption = { text: string; correct: boolean };
type ImportQuestion = { text: string; options: ImportOption[] };

export type ImportRound = { title: string; summary?: string; questions: ImportQuestion[] };

export type ImportDoc = {
  title: string;
  description: string;
  timeLimitSeconds?: number;
  questions?: ImportQuestion[];
  rounds?: ImportRound[];
};

// importQuiz creates a full quiz tree in one request via the admin JSON
// importer, using page.request so the context's storageState admin cookie
// authenticates the call. It GETs the import form to seed the CSRF cookie
// and read the hidden token, then POSTs the document. The play mode is a
// required form field with no default (#752); callers pass it separately
// from the JSON document, defaulting to solo. Throws with the response
// body on a non-redirect outcome so a malformed doc surfaces loudly.
export async function importQuiz(
  page: Page,
  doc: ImportDoc,
  mode: 'solo' | 'live' = 'solo',
): Promise<void> {
  const formResp = await page.request.get('/admin/quizzes/import');
  if (!formResp.ok()) {
    throw new Error(`GET /admin/quizzes/import failed: ${formResp.status()} ${await formResp.text()}`);
  }
  const html = await formResp.text();
  const match = csrfTokenPattern.exec(html);
  const csrfToken = match?.[1] ?? match?.[2];
  if (!csrfToken) {
    throw new Error(`no csrf_token found in /admin/quizzes/import response; body=${html}`);
  }

  const postResp = await page.request.post('/admin/quizzes/import', {
    form: { json: JSON.stringify(doc), mode, csrf_token: csrfToken },
    maxRedirects: 0,
  });
  // The importer 303-redirects to the new quiz on success. maxRedirects: 0
  // keeps the 303 visible instead of following it to the quiz view.
  if (postResp.status() !== 303) {
    throw new Error(`POST /admin/quizzes/import failed: ${postResp.status()} ${await postResp.text()}`);
  }
}

// seedQuiz creates a quiz with the given questions via the admin JSON
// importer instead of driving the authoring UI. Specs that previously
// called createQuizWithQuestions only to have a quiz exist use this; the
// playthrough is then found by title via startQuizAsAnonymous.
export async function seedQuiz(
  page: Page,
  title: string,
  questions: readonly QuestionSpec[] = QUIZ_QUESTIONS,
): Promise<void> {
  await importQuiz(page, {
    title,
    description: 'E2E seeded quiz',
    questions: questions.map((q) => ({
      text: q.text,
      options: q.options.map((text, i) => ({ text, correct: q.correctIndices.includes(i) })),
    })),
  });
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
    // Anchor on the question we just saved appearing in the quiz
    // view's list, not on the URL bar (#396). waitForURL still
    // races slow navigations on contended runners; waiting for the
    // destination-page content also doubles as a check that the
    // save round-tripped.
    await expect(page.locator('.q-text', { hasText: q.text })).toBeVisible({ timeout: 15_000 });
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
    // toBeVisible passes during both the answer window AND the
    // feedback pause (the buttons stay in DOM so the per-option
    // reveal can paint correct/wrong/dim — #233). Under parallel
    // load the click can land while a prior question's feedback is
    // still active: `:disabled="!!feedback"` is truthy, the button
    // carries btn-answer-dim, and the locator then detaches as the
    // question advances (#432). Gate on toBeEnabled so the click
    // happens within this question's answer window or fails fast.
    await expect(optionButton).toBeEnabled({ timeout: 10_000 });
    await optionButton.click();

    // The quieter post-answer reveal (#767): a small verdict eyebrow
    // carries Correct! / Not quite while the per-option highlight shows
    // the right answer.
    const verdict = page.getByTestId('reveal-verdict');
    if (wasCorrect) {
      await expect(verdict).toHaveText('Correct!');
    } else {
      await expect(verdict).toHaveText('Not quite');
    }
  }

  // The leaderboard renders after the last answer's auto-advance hits 404
  // on getNextQuestion. Generous timeout because the per-question feedback
  // delay adds up.
  await expect(page.getByRole('heading', { name: 'Game Finished!' })).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
}

// endHostedSession ends a live session through its lobby End-session control so
// the host's dashboard returns to a hostable state for the next test (a host
// runs one session at a time, #850). Idempotent: a no-op if the room is already
// finished. The End control lives under x-show and is hidden once the session
// is finished, so this waits for the lobby's first state read to settle the
// x-show before deciding, then only drives the control when it is visible.
export async function endHostedSession(host: Page, joinCode: string): Promise<void> {
  await host.goto(`/host/${joinCode}`);
  const endBtn = host.locator('[data-end-session]');
  if (await endBtn.count() === 0) return;
  // Let the lobby's first GET /state land so the Alpine x-show settles to the
  // real phase: the End control is briefly visible on the initial lobby-phase
  // paint even for a finished room, so reading visibility before this settles
  // would misfire. waitForResponse is bounded so a never-firing read still
  // falls through to the visibility gate below.
  await host
    .waitForResponse(
      (r) => new URL(r.url()).pathname === `/api/sessions/${joinCode}/state`,
      { timeout: 10_000 },
    )
    .catch(() => undefined);
  if (!(await endBtn.isVisible())) return;
  host.once('dialog', (dialog) => dialog.accept());
  await Promise.all([
    host.waitForResponse(
      (r) => r.request().method() === 'POST'
        && new URL(r.url()).pathname === `/host/${joinCode}/end`,
      { timeout: 10_000 },
    ),
    endBtn.click(),
  ]);
}

// claimAndJoin drives the #716 anonymous-player join contract over the REST
// API: it claims displayName on the request context's players row (the shared
// claim endpoint the live join routes an unnamed player through), then posts
// the nameless join. The roster, badges, and standings then read that current
// players.display_name. Used by the API-driven players in the live-session
// specs (the player join UI lives behind its own form, exercised elsewhere).
export async function claimAndJoin(
  request: APIRequestContext,
  code: string,
  displayName: string,
): Promise<void> {
  const claimResp = await request.patch('/api/players/me', { data: { displayName } });
  expect(claimResp.status(), `claim ${displayName}: ${await claimResp.text()}`).toBe(200);
  const joinResp = await request.post(`/api/sessions/${code}/join`);
  expect(joinResp.status(), `join ${displayName}: ${await joinResp.text()}`).toBe(200);
}
