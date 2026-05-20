# Release notes

What changed in each released version of Top Banana. The per-PR engineering history lives on each [GitHub release](https://github.com/starquake/topbanana/releases).

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
