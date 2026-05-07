D# End-to-end browser tests

Top Banana's E2E suite drives a real headless Chromium against a real `cmd/server` binary booted on a free port with an isolated SQLite file. Tests live under [`test/e2e/`](../test/e2e/) and use [Playwright](https://playwright.dev) (Node.js).

These tests are intentionally separate from `make check` — they are slower and depend on a Node + browser toolchain.

## Prerequisites

- Node.js 20 (or newer LTS)
- Go (already required by the rest of the project)

## First-time setup

```bash
cd test/e2e
npm ci
npx playwright install chromium firefox
```

On Debian/Ubuntu you can run `npx playwright install --with-deps chromium firefox` to install the browsers' system libraries via apt at the same time. CI does this. On Fedora and other distros, install the system deps via your package manager and use the bare `playwright install chromium firefox` (the `--with-deps` flag only knows apt-get).

## Running

```bash
make test-e2e
```

That runs `npm ci`, `npx playwright install chromium firefox`, and `npm test`. After the first run the install steps are no-ops thanks to the lockfile and Playwright's browser cache.

## What is covered

| Spec | Flow |
|---|---|
| `tests/auth.spec.ts` | Register the bootstrap admin → confirm `/admin/quizzes` loads → log out via `POST /logout` → confirm `/admin/quizzes` redirects to `/login` → log back in → confirm `/admin/quizzes` loads again. Run against Chromium and Firefox. |

Coverage of the player game loop and admin quiz-create flow is tracked separately as PR 2 of #108.

## Debugging

| Mode | Command |
|---|---|
| Watch tests in a real browser | `cd test/e2e && npx playwright test --headed` |
| Step through with the Playwright Inspector | `cd test/e2e && PWDEBUG=1 npx playwright test` |
| Open the interactive UI mode | `cd test/e2e && npx playwright test --ui` |
| View the HTML report from the last run | `cd test/e2e && npx playwright show-report` |

When a test fails the `playwright-report/` directory contains the trace, screenshots, and a video. CI uploads it as a build artifact.

## How the test harness works

`playwright.config.ts` binds the server to a fixed port (8181 by default; override with `TOPBANANA_E2E_PORT`), creates a temp SQLite path, and tells Playwright's `webServer` block to launch `go run ./cmd/server` with environment variables that:

- bind the server to that port,
- point `DB_URI` at the temp SQLite file (so each run is isolated),
- set `REGISTRATION_ENABLED=true` (so `/register` is reachable),
- pin a fixed `SESSION_KEY` (so cookies are deterministic across the run),
- whitelist the per-browser usernames the suite registers via `ADMIN_USERNAMES` (every browser project's registrant becomes admin even though only the first registration triggers the bootstrap-admin rule).

`goose` migrates the empty SQLite file on boot, so no separate migration step is needed.

## Adding a new test

1. Create a new `*.spec.ts` file under `test/e2e/tests/`.
2. Use `import { test, expect } from '@playwright/test'` and write tests against `page` / `request`.
3. The default `baseURL` is set in `playwright.config.ts`, so navigate with relative paths like `page.goto('/admin/quizzes')`.
4. Run `make test-e2e` to verify locally before opening a PR.

## CI

The workflow at [`.github/workflows/e2e.yml`](../.github/workflows/e2e.yml) runs on every PR and push to `main`, separately from the Go `CI` workflow. Failures upload the `playwright-report/` directory as an artifact so you can inspect traces without re-running locally.
