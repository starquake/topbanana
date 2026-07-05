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
- **Self-hosted**: Run the published Docker image, or build the Go binary from source.

## Quick start (Docker)

A pre-built image is published to the GitHub Container Registry, so you can run Top Banana! without cloning the repo:

- `ghcr.io/starquake/topbanana:latest` — the newest release (recommended).
- `ghcr.io/starquake/topbanana:2026.7` / `:2026.7.1` — a specific release, pinned to the major.minor or the exact version.
- `ghcr.io/starquake/topbanana:edge` — the latest `main`; may include changes not yet in a release.
- `ghcr.io/starquake/topbanana:sha-<commit>` — an exact build.

```bash
docker run -d --name topbanana \
  -p 8080:8080 \
  -v topbanana_data:/home/nonroot/data \
  -e SESSION_KEY="$(openssl rand -hex 32)" \
  -e REGISTRATION_ENABLED=true \
  -e ADMIN_EMAILS=you@example.com \
  ghcr.io/starquake/topbanana:latest
```

The server listens on `http://localhost:8080`. Verify it's up:

```bash
curl http://localhost:8080/healthz
# {"status":"ok","checks":{"database":"healthy"}}
```

The image runs in production mode, so `SESSION_KEY` is required — generate one with `openssl rand -hex 32` (rotating it invalidates every active session). The named volume keeps the SQLite database and uploaded media across restarts. With `REGISTRATION_ENABLED=true` and your address in `ADMIN_EMAILS`, sign up at `/register` to create the first admin, then drop `REGISTRATION_ENABLED` and restart to lock the instance down (see [Bootstrapping the first admin](#bootstrapping-the-first-admin)).

### Docker Compose

To run the same image with Compose, save this as `docker-compose.yml`:

```yaml
services:
  app:
    image: ghcr.io/starquake/topbanana:latest
    ports:
      - "8080:8080"
    environment:
      - SESSION_KEY=change-me # openssl rand -hex 32
      - REGISTRATION_ENABLED=true
      - ADMIN_EMAILS=you@example.com
    volumes:
      - topbanana_data:/home/nonroot/data
    restart: unless-stopped

volumes:
  topbanana_data:
```

Then start it and verify it's up:

```bash
docker compose up -d
curl http://localhost:8080/healthz
# {"status":"ok","checks":{"database":"healthy"}}
```

### Behind a reverse proxy (HTTPS)

To serve Top Banana! over HTTPS on your own domain, run it behind a reverse proxy. [linuxserver.io's SWAG](https://docs.linuxserver.io/general/swag/) bundles nginx, Let's Encrypt, and fail2ban in one container, so it obtains and renews TLS certificates for you — point a SWAG `proxy-conf` at the `topbanana` container on port 8080. There is a ready-made one at [`deployments/swag/topbanana.subdomain.conf`](deployments/swag/topbanana.subdomain.conf). Two settings pair with a proxy:

- **`BASE_URL`** — set it to your public URL (e.g. `https://quiz.example.com`) so links in outgoing emails resolve.
- **`TRUSTED_PROXY_IPS`** — set it to the proxy's address or CIDR so the per-IP rate limiters read the real client IP from `X-Forwarded-For` instead of the proxy's.

Building the image from source instead (and the bundled Mailpit mail catcher for local development) is covered in [`docs/development.md`](docs/development.md).

## Configuration

Top Banana! is configured through environment variables. Sensible defaults apply in development; production deployments must set at least `SESSION_KEY` and `DB_URI` (the Docker image already sets `DB_URI`).

In a Compose `environment:` list, write values **unquoted** (`- BASE_URL=https://quiz.example.com`). The `- KEY="value"` form keeps the quotes as part of the value, which breaks URL-typed settings like `BASE_URL`.

### Server

- **`APP_ENV`** — `development` (default) or `production`. Production mode enforces `SESSION_KEY` and `DB_URI`. The published Docker image defaults to `production`.
- **`HOST`** — interface to bind. Defaults to `localhost`. The Docker image overrides this to `0.0.0.0`; set it explicitly in your own manifest if you run the binary directly.
- **`PORT`** — TCP port. Defaults to `8080`.
- **`DB_URI`** — modernc.org/sqlite connection string. Defaults in development to a local `file:topbanana.sqlite` with WAL, `busy_timeout`, and `foreign_keys` pragmas already applied; **required** in production (the image sets it to a file under the data volume).
- **`MEDIA_DIR`** — filesystem directory for uploaded images and audio. Defaults to `./media`. The Docker image writes it under the data volume (`/home/nonroot/data/media`) so uploads survive restarts; point it at a persistent path in your own deployment.
- **`TRUSTED_PROXY_IPS`** — comma-separated CIDR allow-list of reverse proxies whose `X-Forwarded-For` header the per-IP rate limiters should trust. Empty (default) means no proxy, so limiters bucket on the direct connection address. Set it when running behind a reverse proxy so rate limiting sees the real client IP.

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

- **`SMTP_HOST`**, **`SMTP_PORT`**, **`SMTP_FROM`** — SMTP server, port (1-65535), and From address (a bare `you@example.com`, or `Name <you@example.com>` for a display name). All three are required to enable mail; `SMTP_USERNAME` / `SMTP_PASSWORD` add optional auth.
- **`SMTP_TLS`** — require STARTTLS on the connection. Defaults to `true`; set `false` only for a plain local catch-all like Mailpit.
- **`BASE_URL`** — public URL of the instance (e.g. `https://quiz.example.com`), used as the prefix for links embedded in outgoing emails. Set it in production so verify/reset/invite links point at your real host.

### Gameplay

- **`REVEAL_DELAY`** — Go duration string (e.g. `1500ms`) for the per-question reveal beat. Defaults to a small value chosen for live play.
- **`SESSION_START_COUNTDOWN`** — Go duration string (e.g. `60s`) for the host's "Start in 60s" last-call countdown in a hosted live session. Defaults to 60 seconds.

## Bootstrapping the first admin

Registration is closed by default. Two ways to create the first admin:

1. **Open registration briefly.** Start the server with `REGISTRATION_ENABLED=true`, visit `/register`, sign up — the first password-bearing registrant is auto-promoted to admin — then restart with the variable unset.
2. **Whitelist an email.** Set `ADMIN_EMAILS=alice@example.test` (comma-separated), keep registration open, and any user that registers with one of those emails is promoted on signup. Useful for self-hosted deployments where you control the email up front.

## Version stamp

The admin footer and the public `GET /version` endpoint report which release and commit are running. There is nothing to set: the release version is baked in from the committed `VERSION` file at build time, and the commit comes from the build. A `production` deploy shows the release (`v2026.6.0 (abc1234)`); a development build shows the commit (`development (abc1234)`).

To self-host **production** with an accurate release number, run a release image (`:latest` or a pinned `:2026.7.1`) rather than `:edge`. An `:edge` image run as production shows the last release's `VERSION` alongside its newer commit, because `VERSION` only changes when a release is cut — the commit is the disambiguator.

## Troubleshooting

- **`address already in use` on `:8080`** — another process holds the port. Publish a different host port (`-p 8081:8080`) or, when running the binary directly, set `PORT` to a free one.
- **`SESSION_KEY must be set in production`** — the instance is in production mode with no `SESSION_KEY`. Generate one (`openssl rand -hex 32`), set it, and restart.
- **`database is locked` under load** — SQLite serialises writes. The default connection string enables WAL mode and a `busy_timeout`; if you set a custom `DB_URI`, add the same `?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)`.

## Development

Building from source, the project layout, the dev server, and the test suite are covered in [`docs/development.md`](docs/development.md). To contribute, read [`CONTRIBUTING.md`](CONTRIBUTING.md).
