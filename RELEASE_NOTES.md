# Release notes

What changed in each released version of Top Banana. The per-PR engineering history lives on each [GitHub release](https://github.com/starquake/topbanana/releases).

## v2026.5.6 — 2026-05-24

Live leaderboard adds in-progress players, two new host controls (per-quiz visibility, per-question time limit), and a batch of leaderboard and timing fixes.

### Players
- The live leaderboard lists every participant, including those who joined a quiz but have not answered a question yet.
- The countdown on the player view stays in sync with the server clock.
- The leaderboard's answer time reflects when the player tapped, not when the server received the request.
- The finish-screen leaderboard shows an error and a retry option when its fetch fails, instead of staying on "Loading leaderboard…" forever.
- The live leaderboard closes the stream and shows a stale-data indicator after repeated SSE errors.
- The question-entrance animation and the post-finish leaderboard slide-in animation run again.
- The "most active players" list on the start page uses the same 30-day window as "popular quizzes".

### Hosts
- Each quiz can be set to public, unlisted, or private. Public quizzes appear on the home page and `/quizzes`; unlisted quizzes are reachable only by direct link; private quizzes are visible only to their creator and admins.
- Each question can be given its own time limit, set by the quiz author.
- Admin form validation errors render inline on the form instead of as a generic error page.
- The admin landing page shows an "Import quiz" tile next to the existing "New quiz" tile.
- Admin modals are real dialogs: focus stays trapped inside while open, Escape closes them, and focus returns to the element that opened them.

### Visual / chrome
- The "Browse all" link on the start page's popular-quizzes block sits at the bottom of the block instead of next to the section heading.
- The inline pencil for renaming yourself on a leaderboard row is gone. The claim-name modal is the only way to set a display name.

### Behind the scenes
- Session cookies carry the `Secure` flag in every environment except `development`; previously only production set the flag.
- The Docker image declares a `HEALTHCHECK`; CI runs `make smoke` on every change.
- HTTP idle connections close after a configured timeout.
- `/healthz` probes are no longer logged at Info on every request.
- Panics in HTTP handlers are recovered into a 500; SIGTERM triggers a clean shutdown.
- A new `make dev` target restarts the server when sources change.
- The startup log includes the play URL alongside the admin URL, and the admin URL points at `/admin` instead of `/admin/quizzes`.

## v2026.5.5 — 2026-05-22

Mid-question resume on reload, a public quizzes list, and per-quiz creator ownership.

### Players
- A page reload mid-quiz now resumes on the same question with the timer intact, instead of dropping the player back on the start screen.
- A page at `/quizzes` lists every quiz. Clicking a card opens the play page for that quiz.
- The leaderboard now appears on the start screen as well as on the finish screen, with the requesting player's row highlighted on both.
- Two different players answering the same question now see the answer options in different orders.
- The "couldn't submit — please try again" banner now appears only for transient failures (server error, dropped connection). 4xx errors advance the game instead of pinning the banner.
- A successful login or registration as a non-admin player lands on the home page; admins still land on the admin quiz list.
- Quiz images now appear in link previews for `/play/...` URLs alongside the title.

### Hosts
- Each quiz now has a creator. Only the creator and admins can edit, delete, or reset a quiz; everyone can still play it.
- Submitting a quiz with a title that conflicts with an existing quiz's URL now shows an inline error on the form instead of returning a 500.
- The seed admin no longer sees the "claim your display name" modal after finishing a quiz.

### Visual / chrome
- On mobile, the home page no longer shifts by a footer's height when the browser's URL bar collapses or expands.
- The player view no longer lets mobile browsers scroll the URL bar off-screen during a quiz.

### Behind the scenes
- The Alpine.js and anime.js bundles are now served from `/client/js/vendor/` instead of being fetched from jsdelivr at page load.

## v2026.5.4 — 2026-05-20

A public start page, share buttons, and tighter gates on the game endpoints.

### Players
- The start page at `/` lists popular quizzes from the last 30 days and the most active players. A footer link goes into the admin area.
- Share button on three surfaces: each popular-quiz card on the home page, the quiz start screen, and the finish screen. On mobile it opens the native share sheet; on desktop it opens a dialog with Copy, WhatsApp, Telegram, Reddit, and X.
- The "I scored ..." text in a result share reflects the actual score, including after a page refresh or when revisiting an already-completed quiz.
- A failed answer submission (server error, dropped connection) now shows a "couldn't submit — please try again" banner instead of stalling.
- One attempt per quiz per account is now enforced at the database level, so two parallel Start Game requests can't both succeed. Clearing cookies still mints a fresh anonymous player — the system has no way to tie a new session back to a previous one.
- A game URL no longer reveals the participants' scores or advances the game when the requester is not a participant.

### Hosts
- Answer options on the admin quiz view sit behind a per-question spoiler toggle for screen-sharing safety.
- Quiz titles and descriptions appear in link previews for `/play/...` URLs pasted into WhatsApp, Slack, Discord, etc.

### Visual / chrome
- The Top Banana logo on the player view and the home page links back to the start page.
- Primary buttons on the player start screen and the auth pages — "Start Game", "Set your name", "Log in", "Register" — moved to the right of their rows. Secondary controls (Share, register / log-in cross-links) sit on the left.

### Behind the scenes
- A `REVEAL_DELAY` environment variable shortens the per-question reveal beat. The default 3-second pause stays in production; load tests and demos can drop it to a few hundred milliseconds.
- Server 500 responses no longer include wrapped Go error messages in the body.

## v2026.5.3 — 2026-05-19

Live leaderboard updates, a fullscreen gameplay screen, and a finished visual refresh across every page.

### Players
- The leaderboard updates as new players finish a quiz; no page refresh needed.
- Each question opens with a short reveal countdown before the answer timer starts.
- When you pick the wrong option, the correct option is highlighted after the buzz.
- The gameplay screen takes the full viewport during a round, with bigger tap targets.

### Hosts
- A new "Import quiz" page accepts a single JSON document and creates the quiz from it.

### Across the site
- One shared dark theme for the player view, admin pages, and sign-in pages.
- The browser tab now shows a banana icon.

## v2026.5.2 — 2026-05-13

Security and deploy fixes only; nothing user-visible. The server now refuses to start in production with an empty or default session key.

## v2026.5.1 — 2026-05-13

Operational changes only. Staging now uses the same session-key handling as production; `docker-compose` files updated to match.

## v2026.5.0 — 2026-05-13

First Calendar-Versioned release. Adds accounts, per-quiz leaderboards, shareable links, and mobile UI fixes. Each player still plays a quiz at their own pace; there is no synchronous "everyone answers at once" mode.

### Players
- Register an account, log in, log out.
- Play without an account: visitors get an auto-generated display name on first load.
- If you played anonymously and then register, your scores follow you onto the new account.
- The leaderboard at the end of a quiz lists the top scorers, with your row highlighted.
- If you place outside the visible top-N, your own rank and score appear under the leaderboard.
- One attempt per quiz per player. Hosts can reset an individual attempt.
- Questions can include an image (set by the host).
- If you don't answer before the timer runs out, the round moves on instead of stalling.
- New colour scheme and type on the player view.

### Hosts
- Quiz questions can be reordered with Up/Down buttons in the admin.
- Each quiz has a shareable URL of the form `/play/<slug>-<id>`. The admin share dialog copies it to the clipboard or opens WhatsApp / Signal pre-filled.
- The admin quiz view lists every player who finished a quiz, their score, and a per-player reset button.
- The admin quiz list shows the question count and the "last edited" time per quiz.
- `cmd/server -reset-password` rotates a player's password from the host shell.

### Mobile
- Touch targets across the player client are larger.
- The claim-name modal stays above the on-screen keyboard.
- Hover styles no longer stick after a tap on touch devices.
- Form fields no longer keep a stale focus ring after losing focus.

### Security
- Every login, register, and admin POST is protected by CSRF tokens.
- Cookies carry the `Secure` flag only when `APP_ENV=production`, so the dev server stays reachable over plain HTTP on a LAN.

## v0.1.1 — 2026-01-13

Dependency updates only. No user-facing changes.

## v0.1.0 — 2026-01-11

Initial alpha. Anonymous play, admin quiz authoring, a basic per-quiz leaderboard. Every later release builds on this.
