import { join } from 'node:path';

import type { APIRequestContext, Response } from '@playwright/test';

import type { Locator, Page } from './fixtures';
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
// importer only creates solo quizzes (#677), so a host big-screen spec that
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

// publishQuiz flips a quiz to published by title, shelling out to the sqlite3
// CLI the same way setQuizMode does (#1192). New quizzes created through the
// admin importer / UI default to draft, and a draft is filtered out of the
// public solo list and the shared live picker; a spec that plays or lists a
// seeded quiz publishes it first so it behaves like a finished quiz.
export function publishQuiz(title: string): void {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; helpers cannot publish quiz');
  }
  const dbFile = join(dataDir, `e2e-${test.info().parallelIndex}.db`);
  const escapedTitle = title.replace(/'/g, "''");
  const output = execSqlite(
    dbFile,
    `UPDATE quizzes SET published = 1 WHERE title = '${escapedTitle}'; SELECT changes();`,
  );
  const changed = Number.parseInt(output, 10);
  if (changed !== 1) {
    throw new Error(`publishQuiz(${title}): expected 1 row updated, got ${changed}`);
  }
}

// setQuizVisibility flips a seeded quiz's visibility column by title, the same
// direct-SQL shortcut publishQuiz uses (the E2E authoring flow has no
// visibility control), so a spec can seed a private or unlisted quiz that never
// surfaces on the public list.
export function setQuizVisibility(
  title: string,
  visibility: 'public' | 'unlisted' | 'private',
): void {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; helpers cannot set quiz visibility');
  }
  const dbFile = join(dataDir, `e2e-${test.info().parallelIndex}.db`);
  const escapedTitle = title.replace(/'/g, "''");
  const output = execSqlite(
    dbFile,
    `UPDATE quizzes SET visibility = '${visibility}' WHERE title = '${escapedTitle}'; SELECT changes();`,
  );
  const changed = Number.parseInt(output, 10);
  if (changed !== 1) {
    throw new Error(`setQuizVisibility(${title}): expected 1 row updated, got ${changed}`);
  }
}

// attachQuizAudio stamps an audio clip onto every question of a quiz by title,
// shelling out to the sqlite3 CLI (#1088). The admin authoring UI has no audio
// upload in the E2E flow, so this seeds one audio media row for the quiz and
// points every question's audio_media_id at it (a shared clip is fine: the
// manifest keys clips by questionId, so each question gets its own preloaded
// Howl pointing at the same /media/{id} URL). The audio_repeat flag is stamped
// on every question too. The /media/{id} bytes are served by the spec's route
// interception, so the media row's path is a placeholder. Returns nothing; the
// audio-manifest endpoint and the /next (or /state) payloads then both carry the
// same /media/{id} URL, which the spec routes to a real WAV.
export function attachQuizAudio(title: string, opts: { audioRepeat?: boolean } = {}): void {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; helpers cannot stamp quiz audio');
  }
  const dbFile = join(dataDir, `e2e-${test.info().parallelIndex}.db`);
  const escapedTitle = title.replace(/'/g, "''");
  const repeat = opts.audioRepeat ? 1 : 0;
  // One audio media row owned by the quiz's creator, then link every question of
  // the quiz to it. last_insert_rowid() is the new media id within the same
  // sqlite3 invocation. SELECT changes() at the end reports the questions linked
  // so the caller's assertion catches a title typo / empty quiz.
  const sql = `
INSERT INTO media (quiz_id, type, mime, path, size_bytes, sha256, created_by_player_id, ready)
SELECT q.id, 'audio', 'audio/wav', 'e2e-audio.wav', 44, 'e2e', q.created_by_player_id, 1
FROM quizzes q WHERE q.title = '${escapedTitle}';
UPDATE questions
SET audio_media_id = last_insert_rowid(), audio_repeat = ${repeat}
WHERE quiz_id = (SELECT id FROM quizzes WHERE title = '${escapedTitle}');
SELECT changes();`;
  const output = execSqlite(dbFile, sql);
  const changed = Number.parseInt(output, 10);
  if (!(changed >= 1)) {
    throw new Error(`attachQuizAudio(${title}): expected >=1 question linked, got ${changed}`);
  }
}

// attachQuizImage stamps an image media row onto every question of a quiz by
// title, shelling out to the sqlite3 CLI the same way attachQuizAudio does. The
// /media/{id} bytes are never written to disk, so a spec that wants the broken
// image state routes /media to 404 (or lets the server miss the file). The
// question payloads then carry the /media/{id} imageUrl that the <img> tries to
// load.
export function attachQuizImage(title: string): void {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; helpers cannot stamp quiz image');
  }
  const dbFile = join(dataDir, `e2e-${test.info().parallelIndex}.db`);
  const escapedTitle = title.replace(/'/g, "''");
  const sql = `
INSERT INTO media (quiz_id, type, mime, path, size_bytes, sha256, created_by_player_id, ready)
SELECT q.id, 'image', 'image/png', 'e2e-missing.png', 64, 'e2e', q.created_by_player_id, 1
FROM quizzes q WHERE q.title = '${escapedTitle}';
UPDATE questions
SET image_media_id = last_insert_rowid()
WHERE quiz_id = (SELECT id FROM quizzes WHERE title = '${escapedTitle}');
SELECT changes();`;
  const output = execSqlite(dbFile, sql);
  const changed = Number.parseInt(output, 10);
  if (!(changed >= 1)) {
    throw new Error(`attachQuizImage(${title}): expected >=1 question linked, got ${changed}`);
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
  { publish = true }: { publish?: boolean } = {},
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
  // The importer creates a draft (#1192), hidden from the public solo list and
  // the shared live picker; publish it by default so a seeded quiz behaves like
  // a finished one. A spec that keeps the quiz editable (reorder, delete a
  // question) passes { publish: false }.
  if (publish) publishQuiz(doc.title);
}

// seedQuiz creates a quiz with the given questions via the admin JSON
// importer instead of driving the authoring UI. Specs that previously
// called createQuizWithQuestions only to have a quiz exist use this; the
// playthrough is then found by title via startQuizAsAnonymous.
//
// The importer creates a draft (#1192), which is hidden from the public solo
// list, so seedQuiz publishes it by default (via importQuiz) so the quiz plays
// like a finished one. A spec that needs the quiz to stay editable (e.g. a
// delete-from-list test, since a published quiz cannot be deleted) passes
// { publish: false }.
export async function seedQuiz(
  page: Page,
  title: string,
  questions: readonly QuestionSpec[] = QUIZ_QUESTIONS,
  { publish = true }: { publish?: boolean } = {},
): Promise<void> {
  await importQuiz(page, {
    title,
    description: 'E2E seeded quiz',
    questions: questions.map((q) => ({
      text: q.text,
      options: q.options.map((text, i) => ({ text, correct: q.correctIndices.includes(i) })),
    })),
  }, 'solo', { publish });
}

// submitNewQuizForm fills and submits the create-quiz form, binding the wait to
// the Save POST so it can never race the navigation, and returns that response.
// The create handler 303-redirects to /admin/quizzes/{id} on success; a non-303
// re-renders the form in place at /admin/quizzes (a 400 field error, a 409 slug
// collision, ...), which clicking-then-asserting-the-URL would otherwise surface
// only as an opaque toHaveURL timeout on the bare list URL (#1090).
async function submitNewQuizForm(page: Page, title: string): Promise<Response> {
  await page.goto('/admin/quizzes/new');
  await page.locator('input[name=title]').fill(title);
  // The description is rendered as a <textarea> on the redesigned form,
  // not an <input>. The :is() selector keeps the helper resilient to either.
  await page.locator(':is(input, textarea)[name=description]').fill('E2E generated quiz');
  const [saveResp] = await Promise.all([
    page.waitForResponse(
      (r) => r.request().method() === 'POST' && new URL(r.url()).pathname === '/admin/quizzes',
    ),
    page.getByRole('button', { name: 'Save' }).click(),
  ]);
  return saveResp;
}

// deleteQuizByTitle removes a quiz the admin owns by exact title, no-op if none
// exists. The admin delete route cascades through the quiz's questions, media,
// games, and sessions, so it leaves no leftovers for the next create.
async function deleteQuizByTitle(page: Page, title: string): Promise<void> {
  await page.goto('/admin/quizzes');
  const link = page.getByRole('link', { name: title, exact: true });
  if ((await link.count()) === 0) return;
  const href = await link.first().getAttribute('href');
  const quizID = /\/admin\/quizzes\/(\d+)/.exec(href ?? '')?.[1];
  if (!quizID) throw new Error(`deleteQuizByTitle(${title}): no quiz id in href ${href}`);
  const csrfToken = await page.locator('input[name="csrf_token"]').first().inputValue();
  const resp = await page.request.post(`/admin/quizzes/${quizID}/delete`, { form: { csrf_token: csrfToken } });
  if (!resp.ok()) {
    throw new Error(`deleteQuizByTitle(${title}): delete ${quizID} -> ${resp.status()} ${await resp.text()}`);
  }
}

export async function createQuizWithQuestions(
  page: Page,
  title: string,
  questions: readonly QuestionSpec[] = QUIZ_QUESTIONS,
): Promise<void> {
  // Create the quiz, then add each question in turn on the quiz view. A worker
  // keeps one SQLite DB for its whole life, and the quiz slug is derived from
  // the title (the sole UNIQUE column), so re-creating the same title in a
  // worker - under --repeat-each, or on a Playwright retry that re-runs this
  // helper - hits a 409 slug collision. On that collision, delete the leftover
  // quiz and re-create so every attempt starts from a pristine quiz rather than
  // dead-ending on the create redirect or inheriting a prior run's state (#1090).
  let saveResp = await submitNewQuizForm(page, title);
  if (saveResp.status() === 409) {
    await deleteQuizByTitle(page, title);
    saveResp = await submitNewQuizForm(page, title);
  }
  if (saveResp.status() !== 303) {
    throw new Error(
      `quiz Save (POST /admin/quizzes) returned ${saveResp.status()}, expected a 303 redirect; body=${await saveResp.text()}`,
    );
  }
  await page.waitForURL(/\/admin\/quizzes\/\d+$/);

  for (const q of questions) {
    // Each round carries its own "Add question" button now (#929); the
    // imported quiz has a single default round, so the first match is it.
    // The link targets the round-scoped create form, so the URL carries a
    // round_id query.
    await page.getByRole('link', { name: /add question/i }).first().click();
    await expect(page).toHaveURL(/\/admin\/quizzes\/\d+\/questions\/new\?round_id=\d+$/);

    // Question text became a <textarea> in the redesign. Position is
    // auto-assigned by the server now (#16) - no input field on the form.
    await page.locator(':is(input, textarea)[name=text]').fill(q.text);
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

// FEEDBACK_PAUSE_WRONG_MS mirrors the larger arm of the per-question feedback
// hold the solo client applies after a pick (#233): 2s after a correct pick,
// 3s after a wrong one. Fast-forwarding by the wrong-arm value covers both.
const FEEDBACK_PAUSE_WRONG_MS = 3_000;

// CLOCK_BUFFER_MS gives a small margin over the exact beat so a tick that
// lands fractionally short of its threshold still fires - runFor advances by
// whole-ms increments and the client's serverTime() is derived from
// Date.now() plus a clockOffset that's recomputed from each question's
// serverNow, so a buffer absorbs the difference.
const CLOCK_BUFFER_MS = 500;

// playthroughClockInstall is the contract every spec calling
// playThroughQuiz / answerRemainingQuestions must satisfy before page.goto.
// It installs Playwright's virtual clock at the current real time so the
// helper can fast-forward through the per-question reveal beat and feedback
// pause without paying wall-clock time. Kept as a thin wrapper rather than
// a hidden side-effect so the spec's clock contract is visible at its call
// site.
export async function installPlaythroughClock(page: Page): Promise<void> {
  await page.clock.install();
}

// waitForOptionEnabled retries runFor + the toBeVisible / toBeEnabled
// assertions in small chunks of virtual time until the option button is
// visible AND enabled. The chunked retry absorbs three timing skews the
// helper has no other way to align:
//   1. The per-question /next fetch runs in real time, so the
//      startRevealCountdown setInterval may not be registered yet when
//      we first runFor.
//   2. setInterval only ticks under virtual time, so once registered it
//      needs a runFor to actually fire its 100ms ticks.
//   3. resolveAndAdvance's setTimeout (the feedback pause) chains into
//      nextQuestion -> fetch -> startRevealCountdown, all of which
//      interleave real-time and virtual-time work.
// Each retry pumps 500ms of virtual time and re-checks; the toPass
// timeout caps total wall-clock at ~10s so a genuinely-stuck render
// still fails loudly.
async function waitForOptionEnabled(page: Page, choice: string): Promise<void> {
  const optionButton = page.getByRole('button', { name: choice });
  await expect(async () => {
    await page.clock.runFor(500);
    await expect(optionButton).toBeVisible({ timeout: 100 });
    await expect(optionButton).toBeEnabled({ timeout: 100 });
  }).toPass({ timeout: 10_000 });
}

// answerRemainingQuestions clicks the first option for each question starting
// at fromIndex (default 0) and asserts the matching success/danger feedback.
// Waits for the leaderboard at the end so the caller can pick up immediately
// after the auto-advance from the final question. fromIndex lets timeout
// specs skip the questions that have already been resolved (e.g. via the
// timer-expired path).
//
// CONTRACT: the calling spec must call installPlaythroughClock(page) before
// page.goto so the virtual clock is in place from the SPA's first paint. The
// helper drives the per-question reveal-beat (#247) and feedback-pause (#233)
// setInterval/setTimeout via page.clock.runFor instead of waiting in real
// time, so the whole suite skips paying ~5s per question.
export async function answerRemainingQuestions(page: Page, fromIndex = 0): Promise<void> {
  for (let i = fromIndex; i < QUIZ_QUESTIONS.length; i++) {
    const q = QUIZ_QUESTIONS[i];
    const choice = q.options[0];
    const wasCorrect = q.correctIndices.includes(0);

    // Pump virtual time forward in 500ms chunks until the option
    // button shows up and is no longer :disabled. The reveal beat
    // (#247) only ticks under virtual time, and the per-question
    // /next fetch is real-time, so a single runFor would race the
    // fetch and pointlessly advance the clock before the setInterval
    // is registered.
    await waitForOptionEnabled(page, choice);
    const optionButton = page.getByRole('button', { name: choice });
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

    // Feedback pause (#233): resolveAndAdvance schedules
    // setTimeout(2s correct / 3s wrong) before nextQuestion. runFor
    // fires the setTimeout under virtual time; the subsequent
    // nextQuestion fetch is real-time, and the next iteration's
    // waitForOptionEnabled picks up once the new question's
    // setInterval is wired up.
    await page.clock.runFor(FEEDBACK_PAUSE_WRONG_MS + CLOCK_BUFFER_MS);
  }

  // The leaderboard renders after the last answer's auto-advance hits 404
  // on getNextQuestion.
  await expect(page.getByRole('heading', { name: 'Game Finished!' })).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible();
}

// waitForHostRoom waits for the big-screen room URL after a host-start action
// (clicking "Host live", "Host a session", "Host this", or "End and start") and
// returns the join code. It replaces a bare `expect(page).toHaveURL(/\/host\//)`,
// whose 5s expect timeout was too tight for the `/host/<code>` navigation to land
// under CI load (#1143): page.waitForURL is bounded by the test timeout, not the
// 5s expect budget, so it absorbs a slow-runner navigation. It stops at 'commit'
// rather than the default 'load': the big screen opens an SSE EventSource as it
// boots, and firefox holds the page 'load' event open while that stream stays
// connected, so a default wait can hang past the test budget (#1035).
export async function waitForHostRoom(host: Page): Promise<string> {
  await host.waitForURL(/\/host\/[A-Z0-9]{6}$/, { waitUntil: 'commit' });
  return host.url().split('/host/')[1];
}

// endHostedSession ends a live session through its lobby End-session control so
// the host's dashboard returns to a hostable state for the next test (a host
// runs one session at a time, #850). Idempotent: a no-op if the room is already
// finished. The End control lives under x-show and is hidden once the session
// is finished, so this waits for the lobby's first state read to settle the
// x-show before deciding, then only drives the control when it is visible.
export async function endHostedSession(host: Page, joinCode: string): Promise<void> {
  await host.goto(`/host/${joinCode}`);
  const endBtn = host.getByTestId('end-session');
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

// playerRow scopes to a single host-roster row by display name. The host's
// one-room-per-host reuse can leave stale rows from a prior test's room in the
// roster (#957), so specs must assert a player is present by name, never with a
// strict total [data-player-row] count.
export function playerRow(page: Page, displayName: string): Locator {
  return page.locator('[data-player-row]').filter({ hasText: displayName });
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

// installAppearanceFlashProbe records on window[key] whether any element matching
// `selector` ever mounts while active - a MutationObserver catches a mount/unmount
// faster than Playwright can poll, so a "flash" can't slip through. Read it back
// with flashProbeSeen. beforeBoot (default) installs via addInitScript to watch
// from the first frame; pass false to install now, ignoring an earlier in-scope
// appearance and counting only later mounts.
export async function installAppearanceFlashProbe(
  page: Page,
  { selector, key, beforeBoot = true }: { selector: string; key: string; beforeBoot?: boolean },
): Promise<void> {
  const probe = (arg: { selector: string; key: string }) => {
    const flags = window as unknown as Record<string, boolean>;
    flags[arg.key] = false;
    const check = () => {
      if (document.querySelector(arg.selector)) flags[arg.key] = true;
    };
    check();
    // Observe document, not documentElement (null before <html> under beforeBoot).
    const observer = new MutationObserver(() => {
      check();
      if (flags[arg.key]) observer.disconnect();
    });
    if (!flags[arg.key]) observer.observe(document, { childList: true, subtree: true });
  };
  if (beforeBoot) {
    await page.addInitScript(probe, { selector, key });
  } else {
    await page.evaluate(probe, { selector, key });
  }
}

// flashProbeSeen reads back the probe's result (false if it never ran).
export async function flashProbeSeen(page: Page, key: string): Promise<boolean> {
  return page.evaluate(
    (k) => Boolean((window as unknown as Record<string, boolean>)[k]),
    key,
  );
}

/**
 * Opens the quiz view's overflow menu. Its secondary actions - Share, Export,
 * Edit quiz, the mode switch, Delete - live in a collapsed <details> (#1245),
 * so they are not visible until it is opened. No-ops when already open.
 */
export async function openQuizOverflow(page: Page): Promise<void> {
  const overflow = page.getByTestId('quiz-overflow');
  await expect(overflow).toBeAttached();

  if (await overflow.evaluate((el) => (el as HTMLDetailsElement).open)) {
    return;
  }

  await overflow.locator('summary').click();
  await expect(overflow).toHaveJSProperty('open', true);
}
