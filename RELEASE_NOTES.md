# Release notes

What changed in each released version of Top Banana! The per-PR engineering history lives on each [GitHub release](https://github.com/starquake/topbanana/releases).

## v2026.7.1 — 2026-07-04

This release fixes scoring, connection recovery, uploads, and account safety, and adds host-facing details on the quiz and upload screens.

### Players
- A solo answer that arrives after a question's time is up now scores zero.
- A solo game that cannot load the next question now shows a Retry option instead of freezing, including on the first question.
- In a live game, the player screen recovers on its own after the connection drops, and shows a connection warning while it is out.
- The full list of a quiz's questions is no longer available before a timed game starts.

### Hosts
- The big screen reconnects on its own after its connection drops, instead of staying on "Reconnecting...".
- Starting a live room before a quiz is assigned is now rejected instead of leaving the room stuck.
- Once a live game is ended it stays ended; an in-flight update can no longer restart it.
- A large or slow image or audio upload now finishes and saves once, instead of failing partway and creating a duplicate.
- A host can no longer change the answer options on another host's quiz.
- The quiz view shows a media icon and the answer count on each question.
- The image and audio upload screens show the size and count limits.
- The admin media library shows each file's original uploaded name as a caption.
- The sample and field notes on the import screen now match what the importer accepts.

### Behind the scenes
- New sites keep public registration off unless an operator turns it on.
- On an instance with no email configured, the password-reset form is no longer offered.
- Signing in to an account that has not confirmed its email no longer reveals whether the password was correct.
- Claiming a guest account no longer risks its removal by the inactive-account cleanup.
- Deleting a quiz now removes its uploaded images and audio from storage.
- Only trusted pushes to the project can trigger a deployment.

## v2026.7.0 — 2026-07-02

This release adds recovery controls for interrupted live games and clearer handling of failed images and quiz-list load errors.

### Players
- A live player can reconnect from the connection-trouble banner, and can return to the join screen when the game is no longer available.
- A question image that fails to load shows an "Image unavailable" placeholder instead of an empty gap, on the play screen and the big screen.
- When the quiz list fails to load, the start screen shows an error and a Retry button instead of appearing to have no quizzes.
- Keyboard focus stays within an open dialog and returns to the control that opened it when the dialog closes.

### Visual / chrome
- A card no longer briefly flashes when the game moves to a new round.

### Behind the scenes
- The app no longer sends a Server response header naming the web server.
- An optional demo mode can run a public, sign-up-free instance that resets daily; it is off by default.

## v2026.6.7 — 2026-06-27

This release adds audio to quiz questions, and quiz export and import as an archive that includes a quiz's images and sounds.

### Players
- A question can play an audio clip on the solo play screen and the big screen.
- An audio loading screen shows while a question's audio loads.
- Question audio continues to play on later questions, including on iOS.
- Sound effects during a game are updated, play at a lower volume, and a question's audio plays after the sound effect finishes.
- In a live game, the answer options are shuffled for each session.
- A solo game no longer repeats the current question when advancing to the next one.

### Hosts
- A quiz can be exported as an archive file that includes its images and sounds.
- A quiz archive can be imported to recreate the quiz with its images and sounds; pasting quiz JSON is still available.
- An audio clip can be uploaded to a quiz and attached to a question, with an optional description.
- A question's audio can repeat up to three times.

### Visual / chrome
- The host quiz picker shows a monitor icon in place of the larger-screen note.

### Behind the scenes
- The app sends browser security headers: a content security policy, MIME-type and clickjacking protections, and HSTS over HTTPS.
- Uploaded images and audio persist on the data volume across deploys.

## v2026.6.6 — 2026-06-19

This release adds images to quiz questions, removes several brief flashes when play starts and when rounds change, and shows round and score details in the gameplay headers.

### Players
- A question can show an image on the solo play screen and the big screen.
- The first question of a new round no longer briefly shows the previous question.
- A link to a quiz already finished goes straight to the leaderboard without flashing the quiz title and description first.
- The start screen no longer briefly shows the name and browse options before it finishes loading.
- The solo and live play headers show the round number, the question number within the round, and the running score.
- A question's image loads before the question appears.
- The solo results leaderboard no longer reloads while it is on screen.
- Opening "Manage quizzes" without being signed in goes to the login page instead of a sign-out screen.

### Hosts
- A question can be given an uploaded image instead of an image link.
- Each quiz has an image library for uploading, viewing, and deleting images, and several images can be uploaded at once.
- A library image opens in a larger view with a loading indicator while the full image loads.
- A cancelled image upload no longer leaves an entry in the library.
- The big screen shows the round and question numbers during a live question.
- The quiz list keeps its edit and delete buttons reachable by showing at most two columns.
- On a small screen the quiz list shows a note that hosting needs a larger screen instead of a cut-off host button.
- Deleting a quiz or resetting a player updates in place without reloading the page.

### Visual / chrome
- The solo play screen aligns its image, answer colours, and verdict styling with the live answer pad and big screen.

### Behind the scenes
- A failed login can no longer lock another person out of their account.
- Image uploads are capped per host.

## v2026.6.5 — 2026-06-13

The quiz to host is picked from the host area instead of the admin section.

### Players
- A result link copied from a finished game includes the quiz title and the player's score, not only the link.

### Hosts
- The quiz to host is picked from a list in the host area, which shows a live-session indicator when a room is already open.
- A round boundary can be given a custom duration.
- The quiz list shows how many times each quiz has been played.

### Visual / chrome
- The player screens drop the top bar; navigation moves to a footer.
- Solo and live quizzes show a person or people icon on their mode badge.
- The join QR code on the session screen is larger.

## v2026.6.4 — 2026-06-10

A host now opens one room and runs quizzes through it without players re-entering a code; the player client installs as a standalone app on iOS, and every page shares one top navigation bar.

### Players
- The player client installs as a standalone app on iOS, opening without the Safari address bar; content clears the notch, home indicator, and side insets.
- The screen stays awake during a live game.
- A guest at the live-game name prompt can sign in and rejoin under their account display name.
- The sign-in message for an account that still needs to verify its email no longer repeats the email address.

### Hosts
- A host opens one room and runs quizzes through it: players join once, and the room stays open even when the host browses away.
- The host picks each quiz from the quiz list; picking a quiz arms it in the lobby, and the host presses Start when players are ready, for the first quiz and every later one.
- Choosing a different quiz while a game is running asks for confirmation before ending the current session.
- The join code stays on the big screen while a quiz is running, so latecomers can still join.
- The quiz list marks each quiz Solo or Live, adds a Solo / Live / All filter, and a button switches a quiz between the two modes without opening the edit form.
- The hosting display, renamed from lobby to big screen, shows a larger join code and answer options sized for a shared screen.

### Visual / chrome
- Navigation is one consistent top bar across the home, sign-in, and admin pages, with the logo on the left and account controls on the right; the footer holds only the version and wordmark.
- The solo quiz reveal replaces the full-screen Correct / Wrong splash with a small verdict line.

### Behind the scenes
- An allowlisted account is promoted to admin when its email is verified or when it signs in with Google.
- A cooled-down sign-in attempt returns the same generic response as a wrong password, even when the password is correct.
- Requests that change data are accepted only from Top Banana's own pages.
- A round or question window of zero length no longer disrupts scoring.

## v2026.6.3 — 2026-06-08

Top Banana now runs live hosted quizzes, where a host opens a session and players answer each question together, with standings shown between rounds.

### Players
- A live quiz is joined by scanning the QR code or entering the room code on the host's screen.
- All players answer each question at the same time; a short get-ready beat precedes the answers, then the correct option and the order in which players answered are revealed.
- Standings appear between rounds and on a final screen, shown as a bar graph that animates as scores change.
- A reload, a dropped connection, or switching away from the tab returns the player to the current question or the lobby.
- A logged-in player who has set a display name joins under that name without the name-entry step.
- Each live game is its own session: its scores stay with that session, do not change a quiz's solo leaderboard, and a quiz can be hosted again for new players.

### Hosts
- A live session presents on a shared screen with the room code, QR code, live roster, the current question, answered order, and standings.
- Quizzes have a solo or live play mode, set when a quiz is created or imported.
- The host can start a last-call countdown that begins the game when it ends.
- The quiz authoring screen has a clearer layout and reorder controls; the admin area links to the host's profile, with log out moved to the footer.
- Deleting a round of a quiz that has already been played now works.

### Visual / chrome
- Standings bars animate as scores grow and rows change rank, and render correctly at a score of zero on every platform.
- The host screen's join URL is easier to read, and question results are announced to screen readers.

### Behind the scenes
- A private quiz's title and description no longer appear on its public share-link preview.
- The server requires a sufficiently long SESSION_KEY and checks the database connection settings at startup.

## v2026.6.2 — 2026-06-04

The site fonts now appear on the first visit instead of only after a refresh.

## v2026.6.1 — 2026-06-04

Hosts can reorder rounds and questions by dragging and email a player when changing their role. The player screen no longer flashes unstyled content while it loads, and the admin area shows the deployed release version.

### Hosts
- Rounds and questions can be reordered by dragging them on the quiz page; the up and down buttons remain for touch devices.
- When changing a player's role, an admin can choose to email the player about the change.
- The admin area shows the deployed release version.

### Visual / chrome
- The player screen no longer flashes unstyled content while it loads.

### Behind the scenes
- Fonts load from the server instead of a third-party CDN.
- A periodic cleanup removes stale anonymous players and abandoned games.

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
