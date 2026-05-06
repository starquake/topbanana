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

## What exists today

This is the starting point — the doc below treats this as load-bearing, not aspirational.

### Components

| Component | Audience | Stack |
|---|---|---|
| **Admin UI** | Quiz authors | Server-rendered Go `html/template` under `/admin/*`. Plain forms, no JS framework. |
| **JSON API** | Player client | `/api/*` routes. No auth. |
| **Player client** | Anyone with a link | SPA at `/client/`, Alpine.js + Bulma, vanilla JS services. Mobile-friendly. |
| **TV view** | *(not built)* | — |

### Schema

`players (id, username UNIQUE, email UNIQUE NOT NULL, created_at)` — one seeded admin row.
`quizzes (id, title, slug UNIQUE, description, created_at)`.
`questions (id, quiz_id, text, position, image_url)` — `image_url` field exists, not yet rendered in the player client.
`options (id, question_id, text, is_correct)`.
`games (id VARCHAR(20), quiz_id, created_at, started_at)` — `id` is an xid (unguessable).
`game_participants (id, game_id, player_id NOT NULL, joined_at)`.
`game_questions (id, game_id, question_id, started_at, expired_at)` — per-question 10s window, hardcoded.
`game_answers (id, game_id, player_id NOT NULL, game_question_id, option_id, answered_at)` — `UNIQUE(game_id, player_id, game_question_id)`.

### Game flow (single-player)

`internal/game/game.go` implements: create game → loop over `GET /questions/next` and `POST /answers` → `GET /results`. Scoring is `1000 - (duration / 1s * 1000)` capped at zero, so a speed bonus is already in. Sequence is documented in `docs/Games.md`.

### Slugs

`IDFromSlugID` parses `my-quiz-title-123` → id 123. So slugs are SEO-friendly and *guessable* — anyone can iterate IDs. They are not secrets.

### Self-hosting

Multi-stage Dockerfile → distroless. `docker-compose.yml` mounts a volume for the SQLite file. Env vars in `internal/config/config.go`: `APP_ENV`, `HOST`, `PORT`, `DB_URI`, `CLIENT_DIR`, connection pool tuning. `DB_URI` enforced in production.

### Known gaps and TODOs in the code

- `internal/clientapi/clientapi.go:165` and `:300` — hardcoded `playerID=1`. Every gameplay attributes everything to the seeded admin. This is the load-bearing TODO — anonymous play, leaderboard, and auth all unblock by replacing it.
- `internal/game/game.go:62` — comment questioning whether `expired_at` should be a duration instead of an absolute timestamp.
- `internal/game/game.go:309` — comment questioning whether the 1-second peak window for the speed bonus is the right number.
- Player client never renders `image_url`.
- No leaderboard table or query.
- No login form, no session middleware, no password column, no role column.
- No multiplayer state machine — `game_participants` exists but `CreateGame` only ever inserts one row.

## Phasing, anchored to the code

### Phase 1 (finishing single-player honestly)

1. Decide on the player-identity model (Q1 below). Replace `playerID=1`.
2. Admin auth — at minimum, protect the `/admin/*` routes.
3. Per-quiz leaderboard — schema, query, page in player client.
4. Render `image_url` in the player client.

### Phase 1.5

- Author-controlled per-question time limits.
- Optional Google / magic-link sign-in for players who want to claim scores.

### Phase 2

- Live multiplayer: room codes, host control, per-round reveal.
- TV view component.
- Maybe video questions and admin uploads.

### Nice to have

- Numeric / "closest wins" question type.

## Decisions

The decisions below are locked in. Rationale kept short for future-me.

### 1. Player identity — `players` is the universal actor

`game_participants.player_id` is `NOT NULL` and `players.email` is `UNIQUE NOT NULL`, so today there is no way to record a play without inventing an email.

**Decision**: keep a single `players` table. Make `email` nullable. Add `password_hash`, `role`, and provider columns. Anonymous play creates a `players` row tied to a session cookie. Signing in sets credentials on the existing row (claim). For the case where someone signs in with credentials that already match another row, first-sign-in-wins for now — proper merge can come later.

### 2. Sessions — signed cookie, no DB table

**Decision**: store session as a signed cookie (player id + signature, HttpOnly, SameSite=Lax). No `sessions` table. Trade-off: can't force logout-everywhere by deleting a row — fine at friend-group scale.

Auth stack:

- `password_hash` and `role` columns on `players`.
- `bcrypt` via `golang.org/x/crypto`.
- Login / logout / register routes shared between admin and player flows.
- Google OAuth gated by env var (`GOOGLE_OAUTH_CLIENT_ID`). Will use [`markbates/goth`](https://github.com/markbates/goth) — actively maintained as of early 2026, MIT, ~6.5k stars, slow-but-steady release cadence (which is fine for an OAuth library).
- Magic-link email deferred until SMTP config is in scope.

### 3. Quiz visibility — enum on top of existing slugs

**Decision**: keep guessable `title-words-id` slugs. Add a `visibility` enum on `quizzes`: `public` / `unlisted` / `private`.

- `public` — appears in `GET /api/quizzes`.
- `unlisted` — reachable by direct URL, not listed.
- `private` — requires login, owner-only (or whatever future ACL we want).

### 4. Leaderboard — computed on the fly, one play per player

**Decision**: no `quiz_scores` table for now. Compute the leaderboard with a query over `game_answers` joined back to questions. Each player gets one play per quiz on the leaderboard. We can't enforce this strictly without login — a player who clears cookies can play again — but the cookie/session check is good enough for now.

Move to a denormalized score table only if a query gets slow.

### 5. Question expiration — nullable per-question override

**Decision**: add a nullable `time_limit_seconds` to `questions`. Default 10s when null. Admin form gets an optional input. No data migration beyond the new column.

### 6. Numeric / "closest wins" questions — parked

**Decision**: not in scope. If revived later, the shape is a `question_type` column (`multiple_choice` / `numeric`) plus `numeric_answer` and `numeric_tolerance`. Scoring branches on type. No change to existing multiple-choice questions.

### 7. Live multiplayer transport — SSE + HTTP POST

**Decision**: when phase 2 lands, use SSE for the TV view and player updates (one-way fanout, no sticky-session headache, plays nicely behind reverse proxies). Player answers and host actions go over plain HTTP POSTs. WebSocket only if we hit a real limitation.

### 8. Image / video storage — `image_url` contract, optional admin upload

**Decision**: `image_url` (and later `video_url`) stays as the contract — a string the player client renders. Admin can paste any external URL. In phase 2, add an admin upload endpoint that writes to a `MEDIA_DIR` volume and returns the resulting URL — same column either way.

### 9. Admin role — first registrant + env override

**Decision**: first user to register becomes `admin`; admin can promote others through the admin UI. Operator can pre-seed admins via `ADMIN_USERNAMES=alice,bob`. No finer permissions.

## Phase 1 build order

1. **Schema migration**: `players.password_hash` (nullable), `players.role`, `players.email` → nullable. New `quizzes.visibility` enum. New `questions.time_limit_seconds` (nullable).
2. **Auth plumbing**: register / login / logout, signed-cookie sessions, middleware for `/admin/*`. First-registrant-becomes-admin + `ADMIN_USERNAMES` env override.
3. **Anonymous session → player row**: replace the two `playerID=1` TODOs at `clientapi.go:165` and `:300`.
4. **Quiz visibility** wired through API listing and the player landing page.
5. **Leaderboard**: on-the-fly query, one-play-per-player view in the player client.
6. **Per-question time limit** read from `questions.time_limit_seconds`, falling back to the existing 10s default.
7. **Render `image_url`** in the question view.

Phase 1.5 and 2 each get their own design pass.
