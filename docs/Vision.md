# topbanana — vision and design

A self-hosted quiz platform for friend groups. Friends author quizzes and share them; other friends play, either solo against a leaderboard or live together in front of a TV.

## Goals

- **Easy to self-host.** Single `docker compose up` should be enough. SQLite as the only datastore.
- **Low friction to play.** Joining from a shared link must not require an account up front.
- **Friend-group scale.** Tens of users per instance, not thousands.
- **No reinvented wheels for auth.** Use a hosted/library option that works out of the box without external services unless the operator wants them.

## Non-goals (for now)

- Public discovery, marketplaces, monetization.
- Realtime fanout to thousands of concurrent players.
- Multi-tenancy with hard isolation between admin orgs.
- Synchronous "everyone answers the same question at the same time" gameplay (the Kahoot pattern). Players take a quiz at their own pace and see other finishers land on a shared leaderboard.

## What exists today

Phase 1 has landed and a chunk of Phase 1.5 too. The doc below describes the running app at v2026.5.3 plus the public-start-page and share work merged on `main` after the latest release.

### Components

| Component | Audience | Stack |
|---|---|---|
| **Public start page** | Visitors | Server-rendered Go `html/template` at `GET /`. Surfaces popular quizzes (last 30 days) and most active players. A discreet footer link enters the admin area. |
| **Admin UI** | Quiz authors | Server-rendered Go `html/template` under `/admin/*`. Tailwind v4 styling. Plain forms plus a tiny HTMX swap on the question-reorder buttons. |
| **Auth pages** | Players and admins | `/login`, `/register`, `/logout`. Server-rendered with the shared Tailwind theme. Anonymous players upgrade in place when they register; their scores follow. |
| **JSON API** | Player client | `/api/*` routes wrapped in an `EnsurePlayer` middleware that mints an anonymous player row + session cookie on first call. Per-game endpoints (`/api/games/{gameID}/*`) gate on participant membership. |
| **Player client** | Anyone with a link | SPA at `/client/`, Alpine.js + Tailwind v4, vanilla JS services. Fullscreen gameplay surface. Live leaderboard via SSE. |
| **TV view** | *(not built)* | — |

### Schema (post-migration `20260520180000`)

- `players (id, display_name UNIQUE, email UNIQUE, password_hash, role, display_name_claimed, created_at)` — `email` and `password_hash` are nullable so anonymous play works without inventing either. `display_name_claimed` flips to 1 when the player explicitly picks a name (register flow or PATCH `/api/players/me`).
- `quizzes (id, title, slug UNIQUE, description, created_at, updated_at)`.
- `questions (id, quiz_id, text, position, image_url)`. Per-question time limit (`time_limit_seconds`) is still hardcoded at 10s in the service (open ticket #99).
- `options (id, question_id, text, is_correct)`.
- `games (id VARCHAR(20), quiz_id, created_at, started_at)` — `id` is an xid (unguessable).
- `game_participants (id, game_id, player_id, quiz_id, joined_at)` — `quiz_id` is denormalised from the parent game so a UNIQUE INDEX on `(player_id, quiz_id)` can enforce one attempt per player per quiz at the DB level.
- `game_questions (id, game_id, question_id, started_at, expired_at)` — per-question 10s answer window plus a 3s reveal countdown anchored by `started_at` sitting in the future.
- `game_answers (id, game_id, player_id, game_question_id, option_id, answered_at)` — `UNIQUE(game_id, player_id, game_question_id)`.
- Indexes: `games(quiz_id)` for leaderboard joins; `game_answers(player_id)` for the claim-name fan-out.

### Game flow (single-player)

`internal/game/game.go` implements: create game → loop `GET /questions/next` and `POST /answers` → `GET /results`. Scoring uses a 3-second reveal beat anchored by `StartedAt` in the future + a 10-second answer window; the speed bonus is `1000 − (duration / window * 1000)` clamped at zero. Sequence is documented in `docs/Games.md`.

Each `(player, quiz)` pair gets exactly one attempt, enforced at the DB by the `game_participants` UNIQUE index. Admins can reset a player's attempt from the quiz view.

### Slugs

`IDFromSlugID` parses `my-quiz-title-123` → id 123. Slugs are SEO-friendly and *guessable* — anyone can iterate IDs. They are not secrets.

### Self-hosting

Multi-stage Dockerfile → distroless. `docker-compose.yml` mounts a volume for the SQLite file. Env vars in `internal/config/config.go`: `APP_ENV`, `HOST`, `PORT`, `DB_URI`, `CLIENT_DIR`, `WEB_STATIC_DIR`, `SESSION_KEY`, `ADMIN_EMAILS`, `REGISTRATION_ENABLED`, `REVEAL_DELAY`. `SESSION_KEY` is required in production and the server refuses to start with an empty or default value.

### Notable Phase 1.5 additions on top of Phase 1

- **Live leaderboard.** SSE stream at `/api/quizzes/{slugID}/leaderboard/stream` pushes a fresh leaderboard payload to every subscriber when a new finisher lands or a player claims a name.
- **Reveal countdown + correctness reveal.** Each question opens with a 3-second cyan reveal beat before the answer timer starts; after a wrong pick the correct option lights up.
- **Fullscreen gameplay.** The player client takes the whole viewport during a round; the brand mark hides until the leaderboard view.
- **Admin JSON import.** A dedicated `/admin/quizzes/import` form accepts a quiz definition in a single JSON document, designed to be filled out by an LLM.
- **Public home page.** `GET /` ranks popular quizzes by play count over the last 30 days, lists the most active players (claimed display names only), and exposes a footer link into `/admin`.
- **Share affordance.** A reusable `share.js` module powers Share buttons on the public home cards and on the player client (start + finish screens). Uses `navigator.share` when available; otherwise opens a dialog with WhatsApp / Telegram / Reddit / X / Copy.

### Known gaps and open tickets

- **#99** — per-question time limit is still hardcoded at 10s. `questions.time_limit_seconds` column not yet added.
- **#103** — quiz visibility (`public` / `unlisted` / `private`) not implemented. Home queries note where the visibility gate must land once the column exists.
- **#168** — image uploads (and eventually audio/video) still rely on operator-pasted URLs. No `MEDIA_DIR` endpoint.
- **#171 / #172** — Google sign-in and magic-link sign-in not implemented; password reset (#112) and email verification (#111) likewise outstanding.
- **#167** — intermezzo / between-question slides not implemented.
- **#180** — client-side countdown not yet skew-corrected.
- **#234** — a player's running score is shown on the gameplay HUD but not summarised before / after the quiz in a dedicated view.
- **#244** — leaderboard doesn't yet show in-progress players with a "still answering" indicator.
- **#199** — drag-and-drop reordering of questions; Up/Down buttons exist but no DnD yet.
- **#281** — quiz ownership: every admin can edit or delete every quiz today. The decision recorded in #169 narrows this to creator-only edits; the migration + ownership checks live on this ticket.
- Build infra: no TypeScript / esbuild for the player client (#200); still vanilla ES modules.

## Phasing, anchored to the current state

### Phase 1 — DONE

The original Phase 1 list (player identity, admin auth, leaderboard, image rendering) all landed in v2026.5.0. Anonymous → claimed player flow, CSRF, mobile polish, and the synthwave reskin came alongside.

### Phase 1.5 — in flight

Done so far: live leaderboard (SSE), reveal countdown + correctness reveal, fullscreen gameplay, admin JSON import, public home page (#166), share buttons (#176), CalVer release line, deploy hardening.

Outstanding tickets (see "Known gaps" above): #99, #103, #111, #112, #167, #168, #171, #172, #180, #199, #200, #234, #237, #244, #281.

### Phase 2 — not started

- Live multiplayer: room codes, host control, per-round reveal — the SSE plumbing from Phase 1.5 is the foundation but the room/state-machine layer hasn't been built.
- TV view component.
- Maybe video questions and admin uploads (#168 is the gateway).

### Nice to have

- Numeric / "closest wins" question type.

## Decisions

The decisions below are mostly locked in. Status notes mark which ones are implemented and where they live.

### 1. Player identity — `players` is the universal actor — **IMPLEMENTED**

`players.email` is nullable, `password_hash` and `role` columns are present, `display_name_claimed` distinguishes auto-petname rows from explicitly-claimed names. Anonymous play creates a `players` row tied to a session cookie. Registering as an anonymous visitor upgrades the existing row in place via `ClaimPlayer`. First-sign-in-wins for conflicting credentials.

### 2. Sessions — signed cookie, no DB table — **IMPLEMENTED**

`internal/session` issues a signed cookie (player id + HMAC, HttpOnly, SameSite=Lax). `Secure` is gated on `APP_ENV=production` so the dev server works over plain HTTP on a LAN. No `sessions` table. CSRF middleware (`internal/csrf`) covers every unsafe admin and auth POST.

Auth stack delivered:
- `bcrypt` via `golang.org/x/crypto`.
- Login / logout / register routes — admins and players share them.
- Operator can rotate a player's password with `cmd/server -reset-password`.

Google OAuth and magic-link sign-in are still **outstanding** (#171, #172).

### 3. Quiz visibility — enum on top of existing slugs — **NOT IMPLEMENTED**

Still tracked by #103. The home page queries already carry a TODO to gate on `visibility = 'public'` once the column lands.

### 4. Leaderboard — computed on the fly, one play per player — **IMPLEMENTED**

The per-quiz leaderboard query joins `game_answers` back to questions and aggregates per player. `Service.GetQuizLeaderboard` filters to *completed* games (every quiz question issued). The one-attempt rule is now enforced by the DB-level `UNIQUE INDEX game_participants_player_quiz_idx`, not just a session cookie — clearing cookies no longer lets a player back in (#273).

Off-podium players (#181) see their own row below the visible top-N.

### 5. Question expiration — nullable per-question override — **NOT IMPLEMENTED**

Still tracked by #99. The default is the unexported `defaultExpiration = 10 * time.Second` in `internal/game/game.go`. The reveal beat (`defaultRevealDelay = 3 * time.Second`) is configurable via `REVEAL_DELAY` env var so e2e and load-test runs can shrink it.

### 6. Numeric / "closest wins" questions — parked

Unchanged.

### 7. Live multiplayer transport — SSE + HTTP POST — **PARTIALLY IMPLEMENTED**

SSE shipped in v2026.5.3 (`/api/quizzes/{slugID}/leaderboard/stream`). The fanout uses an in-process hub (`internal/leaderboard`) that subscribers receive on; `Service.SubmitAnswer` publishes a refresh tick after a successful insert. The room-and-host-control layer for true synchronous play remains Phase 2.

### 8. Image / video storage — `image_url` contract, optional admin upload — **PARTIALLY IMPLEMENTED**

`image_url` rendering in the player client landed in v2026.5.0. Author-side uploads (writing to a `MEDIA_DIR`) are still tracked by #168.

### 9. Admin role — first registrant + env override — **IMPLEMENTED**

First user with a `password_hash` is promoted to admin atomically in SQL. `ADMIN_EMAILS=alice@example.test,bob@example.test` env var pre-seeds additional admin promotions. No finer-grained permissions.

### 10. Deployment model — single instance, multiple admins, creator-only edits — **PARTIALLY IMPLEMENTED**

Decision recorded in #169. The shape of the deployment is:

- **One instance** per friend group. No tenant routing, no per-instance branding.
- **Multiple admins** can register on the same instance.
- **All quizzes are visible to everyone** on the instance — no per-tenant data partitioning.
- **Only the creating admin can edit or delete a quiz.** Read-only for every other admin. Finer-grained permissions are a future ask, not a current requirement.

The "all admins read everything, one admin writes" half is implicit in the current code (every admin can edit every quiz, which over-grants on the read-only requirement *and* the creator-only requirement). The creator-only-edit half is tracked by #281: add `quizzes.created_by_player_id`, gate the admin mutating routes on it, hide the affordances in the UI.

Multi-tenancy (option 3 in the #169 analysis) is explicitly out of scope. If a second friend group wants a separate instance, they get a separate docker-compose deployment.

## What the next push looks like

Phase 1.5 has the biggest stack of unfinished tickets:

1. **#281** — quiz ownership: `created_by_player_id` column + creator-only edit / delete enforcement. Settles the #169 decision in code.
2. **#99** — wire `questions.time_limit_seconds` through the schema, admin form, and game service.
3. **#103** — `quizzes.visibility` enum + landing-page filter + private-quiz auth check.
4. **#168** — admin upload endpoint writing to a `MEDIA_DIR` volume.
5. **#171 / #172** — magic-link first (no third-party setup), then Google OAuth gated by env var (`markbates/goth` remains the leading candidate).
6. **#234** — explicit score summary on the finish screen.

Phase 2 design pass should land before the room/host-control state machine work begins.
