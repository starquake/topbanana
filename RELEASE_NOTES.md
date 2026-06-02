# Release notes

What changed in each released version of Top Banana! The per-PR engineering history lives on each [GitHub release](https://github.com/starquake/topbanana/releases).

## v2026.6.0 — 2026-06-02

Quiz breaks become named rounds. Accounts gain Player, Host, and Admin roles with player management and email invites, and new sign-ups verify their email before they are signed in.

### Players
- Registering no longer signs you in. The confirmation page asks you to verify your email, and pages that need an account stay unreachable until you verify and log in.
- The registration form no longer reveals whether an email already has an account; registering with an in-use address shows the same confirmation a new one does.
- Resetting your password signs you in automatically.
- Changing your email requires your current password, and the confirmation reads the same whether or not the new address is already in use.
- Each round opens with an intro and closes with a recap during play.
- The start page has a Newest tab alongside Popular.
- Before starting a quiz, players see links to their profile and the quiz catalog.
- The password field shows an inline hint while the entry is too short.
- The cooldown buttons on the sign-in and forgot-password forms count down and re-enable without a page reload.
- A quiz played as a guest carries onto the account created when an email invite is accepted.
- After a password reset, a signed-in account is no longer caught in a login redirect loop.
- Submitting a form left open for a long time, such as logging out, no longer fails with a permission error.
- Password managers no longer treat the display-name field as a login username.

### Hosts
- Quiz breaks are replaced by named rounds, which group questions and can be created, edited, reordered, and deleted.
- The round form places the summary above the name and corrects the name helper text.
- Quiz import accepts rounds in the pasted JSON.
- The quiz import prompt has a copy button and revised guidance.
- Admins can invite people by email; the invite link creates an already-verified account.
- Pending invites can be viewed, resent, and revoked from an invite-management page reachable from the admin navbar.
- Each player is assigned a role of Player, Host, or Admin from a settings page.
- The first registered account becomes an Admin.
- Admins can view a player's onboarding state, set a player's display name and password, and mark a player's email verified.
- A persistent navigation links every admin section.

### Visual / chrome
- The Top Banana! logo links back to the home page from every auth page.
- The brand name appears as "Top Banana!" throughout.
- The round intro and round results cards animate in.
- The reset-password, verify-email, and forgot-password inputs have visible borders.

### Behind the scenes
- The per-IP login cooldown is configurable through LOGIN_COOLDOWN.
- The reset-password and promote-admin break-glass tools run with only the database configured.
- Database writes use immediate SQLite transactions, avoiding write-conflict errors under concurrent load.

## v2026.5.9 — 2026-05-28

Email is the login credential. Email verification is required to sign in, and new self-service pages cover password change, email change, and verify-link request. Quiz breaks ship to players.

### Players
- The login form takes email instead of username. The register form takes email as the credential; the display name field is optional and falls back to a generated name when blank.
- Register has a confirm-password field.
- Email verification is required at sign-in. A registrant who tries to log in before clicking the verify link sees a banner asking them to verify, and the link is resent.
- A new "Didn't get the link?" affordance on `/login`, `/register`, and `/verify-email/pending` opens a public form at `/verify-email/request` that accepts an email address and sends a fresh verify link. Works without signing in.
- A `/profile/password` page accepts a password change. Existing sessions on other devices are signed out; the current tab stays signed in.
- A `/profile/email` page accepts an email change. The new address takes effect after the verify link is clicked.
- A `/forgot-password` page sends a reset link to the address on the account.
- Repeated wrong login attempts get a "too many attempts" message and a short cooldown before another try is accepted.
- Non-production page titles carry the environment as a prefix (`[staging]`, `[development]`).
- The site is installable as a Progressive Web App from supported browsers.
- Quiz breaks added by the host appear between questions during play.

### Hosts
- A new `/admin/email` diagnostics page shows SMTP configuration, the configured `BASE_URL`, and a running send log. A test-send button delivers a probe email and records the attempt.
- A new `/admin/players` page lists every player with account type, email, created date, finished-quiz count, and last-finished-at. The list is paginated.
- Quiz breaks have a full admin CRUD. Breaks slot between questions and reorder via the same arrow controls as questions.
- Quiz import accepts breaks alongside questions.

### Behind the scenes
- A schema migration deletes any credentialled rows that pre-date email capture, then a CHECK constraint enforces "credentialled rows must have an email" going forward.
- `ADMIN_USERNAMES` is renamed to `ADMIN_EMAILS`; the allowlist matches against the verified email at register.
- Per-IP rate limiters on `/login`, the verify-resend endpoint, the forgot-password endpoint, and the admin email tester honour `X-Forwarded-For` from trusted proxies.
- JSON request bodies are capped at 64 KiB.
- `BASE_URL` and `ADMIN_EMAILS` are read from GitHub Actions variables, not secrets — both are non-sensitive and gain nothing from log masking.

## v2026.5.8 — 2026-05-25

Google sign-in is part of the standard production deployment.

- The production deployment requires Google OAuth env vars to be set, matching staging. A deploy missing any of the three fails at boot instead of silently rendering `/login` without the "Sign in with Google" button.

## v2026.5.7 — 2026-05-25

Google sign-in, a profile page, account-aware home and claim-name surfaces, and a new admin players list.

### Players
- Google sign-in is available alongside the existing password sign-in.
- A profile page at `/profile` shows the signed-in player's name and accepts a rename.
- The home page shows the signed-in player's name and a log-out button. Anonymous sessions still see only the "Log in" footer link.
- The claim-name modal no longer opens for signed-in players, and pre-fills the input with the current name when an anonymous player re-opens it.
- The claim-name modal also offers "Log in" and "Sign in with Google" buttons, except when opened mid-quiz.
- Games finished anonymously are moved onto the signed-in account on next sign-in, unless that account already has a finished game for the same quiz.

### Hosts
- A new admin page at `/admin/players` lists every player on the instance with username, account type (admin, password, OAuth, anonymous), email, created date, finished-quiz count, and last-finished-at. The list is paginated.
- The per-question image input and thumbnail no longer render in the admin question form or question list. The backend still accepts and stores image URLs.

### Behind the scenes
- The staging deployment requires Google OAuth env vars to be set, matching production.

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
- The Top Banana! logo on the player view and the home page links back to the start page.
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
