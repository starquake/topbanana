# Congratulations! You are the Top Banana!
Top Banana! is a self-hosted quiz service built with Go, focussing on simplicity and easy deployment.

[![Go Report Card](https://goreportcard.com/badge/github.com/starquake/topbanana)](https://goreportcard.com/report/github.com/starquake/topbanana)
![coverage](https://raw.githubusercontent.com/starquake/topbanana/badges/.badges/main/coverage.svg)

## AI use

I use AI extensively while writing this codebase. Every single line of code is reviewed and signed off before it lands. Media (sounds, images, videos) is not AI-generated.

If contributions come in later, I'll start from [Ghostty's AI_POLICY.md](https://github.com/ghostty-org/ghostty/blob/main/AI_POLICY.md) as a baseline.

## Demo

Try it live at **[demo.topbanana.app](https://demo.topbanana.app/)**. One click signs you in to a shared host account — no sign-up — so you can build and host a quiz. Everything resets daily.

### Game

https://github.com/user-attachments/assets/cb8c8f0b-d2e3-40e7-9753-fa029fd1b20a

### Admin interface

![Admin interface](https://github.com/user-attachments/assets/6746a9b3-68db-46c5-8161-5b3d59fd7664)

## Features
- **Quiz authoring**: Create and edit quizzes from the admin UI — title, description, and multi-option questions.
- **Gameplay**: Each player plays at their own pace; the leaderboard updates as they finish.
- **Self-hosted**: Run as a Go binary or via Docker.

## Installation

### Local development

Prerequisites:

- **Go** — the version pinned in [`go.mod`](go.mod).
- **Node.js 20+** — only needed for `make test-e2e`. The rest of the workflow runs without it.
- **`golangci-lint` and `sqlc`** are downloaded automatically into `build/bin/` by the Makefile targets that need them.

Clone, build, and run:

```bash
git clone https://github.com/starquake/topbanana.git
cd topbanana
make build
go run ./cmd/server/
```

The server listens on `http://localhost:8080` by default. Verify it's up:

```bash
curl http://localhost:8080/healthz
# {"status":"ok","checks":{"database":"healthy"}}
```

`go run ./cmd/server/` writes the SQLite database to `topbanana.sqlite` in the working directory and runs pending migrations on every start.

### Docker Compose

The repo's [`docker-compose.yml`](docker-compose.yml) builds the image from the project's [`Dockerfile`](Dockerfile) and runs it with a named volume for the SQLite file:

```bash
docker compose up --build
```

The compose file sets `REGISTRATION_ENABLED=true` and `ADMIN_EMAILS=admin@example.test`, so a fresh stack lets you register the first admin without further setup. The SQLite database lives in the `topbanana_data` Docker volume at `/home/nonroot/data/topbanana.sqlite`.

### Local email testing

The compose file ships a [Mailpit](https://github.com/axllent/mailpit) sibling service so outgoing mail does not leave the host. Bring the stack up with `docker compose up --build`, then uncomment the `SMTP_HOST` / `SMTP_PORT` / `SMTP_FROM` / `SMTP_TLS=false` block on the `app` service to point the mailer at Mailpit. The inbox is at `http://localhost:8025`; the diagnostics view at `/admin/email` shows the current SMTP wiring, sends a test message, and lists the last 20 send attempts with the verbatim SMTP error on failure.

SMTP is optional. Without any `SMTP_*` env vars the server boots, the diagnostics page renders a "disabled (no-op)" status badge, and the test-send button returns a clear "email is not configured on this instance" message instead of a 500.

### Bootstrapping the first admin

Registration is closed by default. Two ways to create the first admin:

1. **Open registration briefly.** Start the server with `REGISTRATION_ENABLED=true`, visit `/register`, sign up — the first password-bearing registrant is auto-promoted to admin — then restart with the variable unset.
2. **Whitelist an email.** Set `ADMIN_EMAILS=alice@example.test` (comma-separated), keep registration open, and any user that registers with one of those emails is promoted on signup. Useful for self-hosted deployments where you control the email up front.

### Version stamp

The admin footer and the public `GET /version` endpoint report which release and commit are running. There is nothing to set: the release version is baked in from the committed `VERSION` file at build time, and the commit comes from the build (or, for a local `go build`/`go run`, from the working checkout, including a `-dirty` marker). A `production` deploy shows the release (`v2026.6.0 (abc1234)`); every other environment shows the commit (`development (abc1234)`).

To self-host **production** with an accurate release number, build from a release tag or run a released image. An untagged `main` build run as `production` shows the last release's `VERSION` alongside its newer commit, because `VERSION` only changes when a release is cut — the commit is the disambiguator.

## Project Structure

Following the official guidelines for a [standard Go server project layout](https://go.dev/doc/modules/layout#server-project).

### Folders
- `cmd/server`: Application entrypoint.
- `cmd/seed-dev`: Seeds the local dev database with example quizzes. The `-seed` flag picks the seed set: `test` (the default) loads the small fixture quizzes, while `demo` restores a set of showcase quizzes (classical-music sights and sounds, animal sounds, and a text quiz) built from committed public-domain quiz archives. Both sets also seed a few anonymous players and finished games so the leaderboard and popular lists have data.
- `deployments`: Docker compose configurations for the staging, production, and demo deployments.
- `docs`: Documentation for the project.
- `internal/`: Private library code, including domain logic, database operations, HTTP handlers.
  - `absurl`: Builds absolute URLs from a request for share links and Open Graph cards.
  - `admin`: Business logic for the admin interface.
  - `auth`: Session players and role-based access helpers.
  - `client`: Player client shell — embedded HTML/JS/CSS.
  - `clientapi`: JSON API used by the player client.
  - `config`: Configuration management.
  - `csrf`: CSRF token issuance and validation.
  - `database`: Database connection and utilities.
  - `db`: Database operations and models generated by `sqlc`.
  - `dbtest`: Helpers for testing database operations.
  - `game`: Business logic for gameplay.
  - `handlers`: HTTP utilities for handlers, parsing query parameters, encoding and decoding JSON, and more.
  - `health`: Health check endpoint.
  - `home`: Public landing page and `/quizzes` directory.
  - `leaderboard`: Live leaderboard pub/sub hub used by the SSE stream.
  - `migrations`: Database migrations.
  - `queries`: SQL queries used by `sqlc`.
  - `quiz`: Business logic for quiz creation and management.
  - `server`: HTTP server, routes, and middleware.
  - `session`: Cookie session encoding and verification.
  - `store`: Database storage layer for quizzes and games.
  - `testutil`: Helpers for testing the application.
  - `web`: Admin templates and embedded static assets (`html/template` + Tailwind).
- `test`: Integration and end-to-end tests.

### Files
- `.golangci.yaml`: Configuration for `golangci-lint`.
- `docker-compose.yml`: Docker compose configuration for development.
- `Dockerfile`: Dockerfile for building the application.
- `sqlc.yaml`: Configuration for `sqlc`.
- `Makefile`: Makefile for common tasks.

## Configuration

Top Banana! is configured through environment variables. Sensible defaults apply in development; production deployments must set at least `SESSION_KEY` and `DB_URI`.

### Server

- **`APP_ENV`** — `development` (default) or `production`. Production mode enforces `SESSION_KEY` and `DB_URI`.
- **`HOST`** — interface to bind. Defaults to `localhost`. The provided Docker image overrides this to `0.0.0.0`; set it explicitly in your own compose / k8s manifest if you bind directly to the binary.
- **`PORT`** — TCP port. Defaults to `8080`.
- **`DB_URI`** — modernc.org/sqlite connection string. Defaults in development to a local `file:topbanana.sqlite` with WAL, `busy_timeout`, and `foreign_keys` pragmas already applied; **required** in production.
- **`MEDIA_DIR`** — filesystem directory for uploaded images and audio. Defaults to `./media`. The Docker image writes it under the data volume (`/home/nonroot/data/media`) so uploads survive restarts; point it at a persistent path in your own deployment.
- **`TRUSTED_PROXY_IPS`** — comma-separated CIDR allow-list of reverse proxies whose `X-Forwarded-For` header the per-IP rate limiters should trust. Empty (default) means no proxy, so limiters bucket on the direct connection address. Set it when running behind a reverse proxy so rate limiting sees the real client IP.
- **`CLIENT_DIR`** — development-only override that serves the player client from a directory on disk instead of the embedded FS, so HTML/JS edits hot-reload on page reload.
- **`WEB_STATIC_DIR`** — development-only override that serves the admin/auth/home static assets (Tailwind output at `/static/`) from a directory on disk instead of the embedded FS. Set to `internal/assets/static` alongside `CLIENT_DIR=internal/client/static` for full live-reload coverage; a `make tailwind` regen then lands on the next request without a binary restart.

### Database tuning

- **`DB_MAX_OPEN_CONNS`** — `database/sql` max open connections.
- **`DB_MAX_IDLE_CONNS`** — max idle connections held in the pool.
- **`DB_CONN_MAX_LIFETIME`** — Go duration string (e.g. `30m`) after which idle connections are recycled.

### Auth and access

- **`SESSION_KEY`** — secret used to HMAC-sign session cookies. Defaults to a random ephemeral key in development; **required** in production. Treat as a credential — rotating it invalidates every active session.
- **`ADMIN_EMAILS`** — comma-separated list of email addresses. A registrant whose trimmed + lowercased email matches an entry is promoted to `admin` on registration. The very first password-bearing registrant becomes admin regardless of this list. Defaults to empty.
- **`REGISTRATION_ENABLED`** — when `false` (the default), `GET/POST /register` return `404` and the "No account? Register" link is hidden on `/login`. Set to `true` to allow new sign-ups — typically just long enough to bootstrap your first admin, then unset to lock the instance down.

### Email

Outgoing mail (email verification, password reset, invites) is optional. Without `SMTP_HOST` the mailer is a no-op: the server boots and the diagnostics page at `/admin/email` shows a "disabled" badge.

- **`SMTP_HOST`**, **`SMTP_PORT`**, **`SMTP_FROM`** — SMTP server, port (1-65535), and From address. All three are required to enable mail; `SMTP_USERNAME` / `SMTP_PASSWORD` add optional auth.
- **`SMTP_TLS`** — require STARTTLS on the connection. Defaults to `true`; set `false` only for a plain local catch-all like Mailpit.
- **`BASE_URL`** — public URL of the instance (e.g. `https://quiz.example.com`), used as the prefix for links embedded in outgoing emails. Set it in production so verify/reset/invite links point at your real host.

### Gameplay

- **`REVEAL_DELAY`** — Go duration string (e.g. `1500ms`) for the per-question reveal beat. Defaults to a small value chosen for live play.
- **`SESSION_START_COUNTDOWN`** — Go duration string (e.g. `60s`) for the host's "Start in 60s" last-call countdown in a hosted live session. Defaults to 60 seconds.

## Troubleshooting

- **`address already in use` on `:8080`** — another process holds the port. Either stop it or set `PORT` to a free one (`PORT=8081 go run ./cmd/server/`).
- **`SESSION_KEY must be set in production`** — `APP_ENV=production` and no `SESSION_KEY`. Generate one (`openssl rand -hex 32`), set it, and restart.
- **`database is locked` under load** — SQLite serialises writes. The compose file's connection string already enables WAL mode and a `busy_timeout`; for local dev add the same `?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)` to your `DB_URI`.
- **`make check` reports `app.css is out of date`** — Tailwind output drifted. Run `make tailwind` and commit the regenerated `internal/assets/static/css/app.css`.
- **E2E setup** — see [`docs/e2e.md`](docs/e2e.md) for Playwright prerequisites and the `make test-e2e` workflow.

## Development

### Code Style
This project uses conventions used by the standard library and the following style guides:
- [Go Style Guide](https://google.github.io/styleguide/go/)
- [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)

### Running the dev server
- `make server` runs the Go server once.
- `make dev` runs it with [watchexec](https://github.com/watchexec/watchexec), which SIGTERMs and restarts it on Go, template, or migration changes. Pair with `make tailwind-watch` in a second terminal for CSS regeneration on template edits.

### Running tests
- To run unit tests: `make test`
- To run integration tests: `make test-integration`
- To run all tests: `make test-all`
- To check test coverage for all packages: `make test-coverage`
- To view test coverage in your browser: `make test-coverage-html`
- To run end-to-end browser tests: `make test-e2e` (requires Node.js — see [`docs/e2e.md`](docs/e2e.md))

### Pre-commit check
Run `make check` to run linters, build the project, and run all tests with coverage.
