# topbanana

## Commands

```bash
make check            # lint + sql-lint + build + all tests — run before every commit
make test             # fast suite; integration tests skip under -short
make test-integration # everything, incl. integration (no -short)
make test-e2e         # end-to-end browser tests (Playwright; requires Node.js)
make smoke            # validate startup against the existing dev DB (no HTTP listener)
```

## Commits and PRs

Per-change order of operations:

1. Implement the change.
2. Run the full local check suite: `make lint-fix`, `make check`, `make smoke`, `make test-e2e`. Fix anything they surface.
3. Run the `/code-review` + `/go-style-review` loop on the diff; fix every actionable finding and re-run until both are clean. **The diff must be review-clean before push, not after.** Clear the golangci cache first (`rm -rf ~/.cache/golangci-lint`) and run the loop yourself — a warm-cache green or a dev-agent's self-reported "clean" can hide findings.
4. Stage the files explicitly (`git add <paths>` — never `-A` or `.`), so secrets and binaries don't sneak in.
5. Commit with a plain-language subject line. Avoid jargon; prefer simple verbs ("change", "update", "fix", "add", "remove"); start with a capital letter; single short subject line, no body or rationale paragraphs.
6. Push the branch and open a draft PR. PR body follows "Linking a PR to a ticket" below.
7. Ask explicitly: "Did the review look OK?" or equivalent. Wait for the user's explicit go-ahead — silence is not consent. Do not merge before sign-off; do not merge while the PR is still a Draft.

**No WIP commits in shipping history.** If a crash or handoff forces a checkpoint commit to make in-flight work durable, fold it back into one clean commit (`git reset --soft main`, then recommit with a plain subject) before opening the PR.

**Sign-off does not carry.** A "looks good" covers only the diff that was on GitHub at that moment. If you make any further change after sign-off — fixing a lint issue, addressing a comment, anything — commit and push it, then ask for sign-off again on the new lines.

### Linking a PR to a ticket

- The commit subject and PR title stay **clean of `Closes #N` / `Fixes #N` keywords**. They describe what changed, not which ticket they reference.
- When a PR resolves a tracked ticket, put `Closes #N` in the **PR description** (`gh pr create --body "Closes #N"`). GitHub auto-closes the issue on squash merge because it picks up the keyword from the merged commit body, which is the PR description.
- For a PR that only partially addresses a ticket, omit the keyword and close the ticket manually with a summary once all slices land.
- A PR that has no associated ticket gets an empty body (`--body ""`); nothing else belongs there.

## Testing

Every change or new feature must have tests. The command sequence to run before marking work done is the per-change workflow at the top of this file; this section is only about *what* to test and *where* to put the test.

`make check` only exercises a fresh DB, so migration or startup issues that only surface against populated data otherwise slip through. `make smoke` runs `go run ./cmd/server/ -check` to parse config, open the dev DB, run migrations, and exit — no port juggling, no leftover process.

**Write a test instead of an ad-hoc check script.** When you need to verify something works — a new endpoint, a flow, a config side effect, an asset getting served — express it as a test, not as a one-off `curl`, `wget`, scripted Playwright session, or bash probe. A scripted check verifies behaviour once at implementation time and gets thrown away; a test catches regressions forever.

Pick the right layer:

- **Unit test** (`*_test.go` next to the code) — pure logic, no I/O.
- **Integration test** — anything touching real I/O (server, DB, HTTP routing, embedded assets). Gated by `testing.Short()`, **not** a build tag: they `t.Skip` under `-short` via a choke point — `dbtest.Open`/`OpenUnmigrated`/`SetupTestDB` for layer tests, `startServer` for full-stack. So `make test` (`-short`) skips them; `make check` / `make test-coverage` / CI run them. **Pair each test file with a same-named source file, stdlib-style** — `foo.go` is tested by `foo_test.go`, and that one `foo_test.go` holds both its unit and integration tests; never add a topic-named `bar_test.go` with no `bar.go` — split the oversized source to match, or name the test after its source. Exempt, exactly as in the stdlib: `export_test.go`, `testmain_test.go`, test-only doubles/helpers with no `Test` funcs, and `internal/migrations/*_test.go` (they exercise `.sql`, with no Go source to pair). Tracked by #1021, which also adds an advisory `make lint-test-pairing`. Three homes:
  - **Full-stack / black-box** tests, driven through the running server (package `integration_test`), live in `test/integration/` and share its server + DB + cookie-jar harness.
  - **Layer tests** that exercise one store/service directly against a real DB (via `dbtest.Open`) live **beside the code they test** (e.g. `internal/store/`, `internal/game/`) — model: `internal/store/round_test.go`. Do not relocate these into `test/integration/`.
  - **Migration tests** live in `internal/migrations` (package `migrations_test`) — model: `internal/migrations/rounds_test.go`.
- **E2E test** (`test/e2e/`, Playwright) — behaviour that only makes sense in a real browser: clicks, navigation, form flows, JS-driven UI.

One-off scripts are reserved for genuinely interactive debugging that can't be expressed as a test (e.g. eyeballing a visual layout, attaching a debugger). If you find yourself writing the same `curl` twice, it's a test. The Playwright MCP (`browser_*` tools) falls under the same rule: use it to *look* at the live UI interactively, never as the way you assure a behaviour — anything worth guaranteeing goes into a `test/e2e/` spec that runs in CI.

**A flaky test is a bug to file, not to rerun past.** When a test fails then passes on a plain rerun, open a GitHub issue capturing it rather than retrying until green.

**The coverage gate is live.** CI enforces a total-coverage floor (currently 80); the authoritative `threshold-total` lives in the coverage step of `.github/workflows/ci.yml` — the action's default of `-1` overrides any value in `.testcoverage.yml`, so set it in the workflow.

**HTTP handler tests are integration tests, not stub-driven unit tests.** Pin a handler end-to-end (router -> middleware -> handler -> store -> DB) against a real store on a `dbtest` DB, not a stub that restates what the store should return — a stub passes even when the real wiring (routing, query, serialization) is broken. The old grandfathered stub-driven handler tests were converted in #638 (reversing #30).

Keep a purpose-built **fault-injection double** only where a real store genuinely cannot reproduce a case: a forced petname collision, the specific internal error string a leak test asserts is *not* exposed, a `GetX` failure on a path a real FK makes otherwise unreachable. Those are legitimate fakes (like a mailer spy or a closed DB), not tautological store stubs -- keep them, and keep their tests as untagged unit tests. For an ordinary "store errored" branch, prefer a closed DB over a double.

## Workflow

When starting work on a new ticket: switch to `main`, run `git pull`, then create a new branch. Branch names use dashes (not slashes) and are prefixed with the ticket number — e.g. `1-fix-bugs`. Ask for the ticket number if not provided. Omit the prefix if there is no ticket.
When asked to update a branch, do it by rebasing onto main (`git rebase origin/main`), never by merging.
Never suggest merging on your own and never infer that a merge is wanted (CI passing is not approval, and an earlier PR's merge instruction does not carry to the next PR). Run `gh pr merge` only when the user explicitly asks for *this* PR — phrases like "merge it", "go ahead and merge", or "commit, push, merge" in one breath count as that ask.
After each piece of work, provide the PR URL: `https://github.com/starquake/topbanana/compare/<branch-name>`
When implementing review findings or other non-trivial code changes, delegate to the appropriate dev agent — `backend-dev` for Go code under `internal/`, `cmd/`, migrations and queries; `frontend-dev` for the player client and admin templates — so project conventions are consistently applied.

## CI required checks

The `main` branch is protected by a repository **ruleset** ("Default"), not classic branch protection (the `branches/main/protection` API 404s). It is **strict**, so a PR must be up to date with `main` and pass every required check to merge. The required list lives in the ruleset, not this file, so it can drift silently. Current required contexts: `build`, `lint`, `e2e (chromium)`, `e2e (firefox)` — all jobs of the single `CI` workflow (`.github/workflows/ci.yml`), matched by job name.

**When you add or rename a workflow job**, update the ruleset's required checks in the same PR, or the job is only advisory. Edit via the rulesets API — GET the ruleset, modify `required_status_checks` (each entry needs `integration_id: 15368`, the Actions app), PUT it back:

```bash
RID=$(gh api repos/starquake/topbanana/rulesets --jq '.[] | select(.name=="Default").id')
gh api repos/starquake/topbanana/rulesets/$RID --jq '.rules[] | select(.type=="required_status_checks") | [.parameters.required_status_checks[].context]'
```

When removing a job, drop its context from the ruleset too — a stale required-context name blocks every PR indefinitely.

## Deploys

Staging and production are **independent pipelines** (`.github/workflows/deploy.yml`), not a soak-and-promote chain — production never auto-promotes from staging.

The image is **built once, after the suite is green**, and reused — the `CI` workflow's `docker-build` job `needs: [build, lint, e2e]`, so a published image always implies the tests passed for that commit (#630). `deploy.yml` keys on the `CI` workflow succeeding (not a separate Docker build), so a deploy only fires when tests + image are both green.

- **Staging** deploys on every merge to `main`: `docker-build` pushes `edge` + `sha-<commit>` tags after the suite passes, a successful `CI` run on `main` fires `deploy-staging`, goose runs pending migrations on container boot (12x5s health-check loop gates success).
- **Production** deploys when a `v*.*.*` tag is pushed: the `promote` job **retags the existing `sha-<commit>` image** (the one built and tested on `main`) to `{version}` (e.g. `2026.5.8`) + `{major}.{minor}` — no rebuild and no re-run of the suite. A successful `CI` run on the tag fires `deploy-production` pulling that exact version. Production is whatever the latest `v*.*.*` tag points at.
- **Manual**: `workflow_dispatch` with an `environment` input redeploys without a code change.

Consequences for work in flight: "merged to `main`" means live in **staging**, not production. Production stays on the last tag until a new one is cut, and all changes since the previous tag ship together when it is. A schema migration runs on staging at next container boot, on production at next tag deploy.

Both jobs build a fresh `.env` from GitHub **secrets** (masked in logs: `SESSION_KEY`, `GOOGLE_CLIENT_SECRET`, `SMTP_PASSWORD`, ...) and **variables** (unmasked: `BASE_URL`, `REGISTRATION_ENABLED`, `ADMIN_EMAILS`). Both are scoped per-environment — a value set on `staging` is not visible to `production`.

## Comments

Default to writing **no** comments. Code with well-named identifiers, small functions, and clear control flow explains *what* it does — that's not the comment's job. A comment earns its place only when the *why* is non-obvious: a hidden constraint, a subtle invariant, a workaround for a specific bug, behaviour that would surprise a reader.

If removing the comment wouldn't confuse a future reader, don't write it.

- Don't restate the code (`// increment i`, `// open the file`, `// the X package` above a `package x` declaration).
- Don't reference the current task, fix, or caller (`// used by X`, `// added for Y`, `// handles the case from issue #123`) — those belong in the PR description and rot as the codebase evolves.
- Don't write multi-paragraph rationale or step-by-step narration. One short line per real `why` is usually enough.
- Issue links are fine when they're load-bearing: `// see #165` stays accurate because issues don't move.

When in doubt, leave the comment out. A reviewer who finds the code unclear will ask, and that's a better signal of what actually needs a comment.

## Comments that reach across files

WHY comments are encouraged when the rationale isn't obvious. But a WHY that explains *what the other side of the system expects* is fragile: if that side changes, the comment silently lies and misleads the reader.

Treat cross-file rationale by category:

1. **Server explains what the frontend will do with this** — high rot risk. **Rewrite as self-contained why**. Explain the local rationale only: "this column tracks whether the player explicitly chose their display name; defaults false so callers can tell auto-generated names apart from claimed ones." Lose the "the frontend gates X on this" framing.

2. **Test comment above an assertion that pins the invariant** — fine. The test fails loudly if the invariant breaks, so the comment can't drift far. Keep the cross-file wording if it adds context.

3. **Reference to a specific test that pins the invariant** — fine. Phrase as `// invariant pinned by TestFooBar` and let grep verify the test name still exists. Better than a prose ref because it's machine-checkable.

4. **Issue or PR link** — fine. Issues don't move. `// see #165` stays accurate.

Run `make lint-cross-refs` periodically to surface candidates. The target is advisory — it greps `internal/` for cross-file keywords and prints hits; treat each as a candidate to rewrite, not a hard CI fail.

## User-facing copy

Anything a player or quiz host will read — release notes, README, UI strings, error messages — is written in plain, factual language. There is nothing to sell.

- Describe what is there, not how exciting it is. "The leaderboard updates as new players finish" beats "Watch scores land on the leaderboard in real time."
- Cut framing words: "polished", "headline release", "the big one", "real multiplayer", "a moment to read", etc. They add length, not information.
- Drop intro paragraphs that summarise a release as a narrative arc. A one-line factual lead is enough.
- Use neutral verbs (added, now shows, accepts, rotates) instead of marketing ones (unleashes, lets you, gives you).
- Avoid PR/issue numbers and internal-infra mentions (Tailwind migration, sqlc, CI tweaks) in end-user copy. They belong in the per-release GitHub notes or commit history.
- When mentioning behaviour that could be misread as something it isn't — e.g. "multiplayer" — say what is actually true ("each player plays at their own pace; the leaderboard updates as they finish") instead of using the shorter label.
- Group by the reader's role only when it helps them find their entries (Players / Hosts). Drop role headers when one section would be empty.

The same voice applies to commit messages and PR descriptions: short, declarative, no salesy adjectives.

## Migrations

Table rebuilds (the SQLite idiom for `ALTER COLUMN`, FK changes, etc.) need care with foreign keys, and which pattern to use depends on whether the rebuilt table is FK-referenced by others:

- **Child table** (nothing references it): use `PRAGMA defer_foreign_keys = ON` inside goose's default transaction. It keeps FK enforcement on but postpones the check to COMMIT, so a broken rebuild fails loudly instead of leaving dangling references. Canonical: `20260520180000_unique_participant_per_player_quiz.sql`.
- **Parent table** (other tables reference it, e.g. `players`): `defer_foreign_keys` does NOT work here — dropping the parent registers child-reference violations that the deferred check at COMMIT still trips on (verified against `modernc.org/sqlite` v1.31.x). Use `-- +goose NO TRANSACTION`, then `PRAGMA foreign_keys = OFF`, an explicit `BEGIN TRANSACTION ... COMMIT`, then `PRAGMA foreign_keys = ON`, and add the file to the `make lint-migrations` allowlist. Canonical: `20260529160000_roles_player_host_admin.sql`.

`PRAGMA foreign_key_check` is NOT a guard — it only returns violation rows and goose discards them, so a broken rebuild commits silently. To abort on a dangling reference, add the `_fk_guard` CHECK-constraint pattern before COMMIT (see `20260529160000`). The full how-to is in the `backend-dev` agent.

Run `make lint-migrations` to surface any new migration using `foreign_keys = OFF` outside the allowlist. It is advisory (exit 0; only prints offenders). The allowlist (the grep filter in the Makefile target) covers the grandfathered pre-rule files plus each deliberate parent rebuild; add new parent rebuilds there.

## Tooling

- **Prefer modern CLI tools**: `rg` / `fd` / `sd` / `yq` / `ast-grep` over `grep` / `find` / `sed` / `wc`. Reach for `ast-grep` first when matching code structure (invoke it as `ast-grep`, not `sg`). Inspect files with the Read tool, not `sed` / `cat`.
- **`golangci-lint` lives at `build/bin/golangci-lint`** (not on `PATH`). To clear its cache use `rm -rf ~/.cache/golangci-lint` — `golangci-lint cache clean` is a silent no-op because the binary isn't on `PATH`.
- **"main lint red but local clean" is usually a stale-cache phantom** — an unused-`nolint` flagged on a still-needed directive. `nolintlint.allow-unused: true` in `.golangci.yml` tolerates it; confirm via the failing check-run that only `lint` is red. `.golangci.yml` also excludes `.claude` — agents run in worktrees under `.claude/worktrees/`, and without the exclusion golangci scans those sibling checkouts and floods the run with phantom findings.
- **Don't install tooling that ships on the GitHub runner image** (e.g. `sqlite3`); lean on what's preinstalled.
- **One Make target per long-running dev process** (e.g. `tailwind-watch`, the server) — not a combined supervisor — so each is independently startable and killable.

## Hard rules

- **Do not edit `internal/db/`** — generated by `sqlc generate` from `internal/queries/*.sql`.
- **No SQL in Go files** — add queries to `internal/queries/` and regenerate.
- **No third-party HTTP framework or ORM.**
- **ASCII only in `.go` and `.sql` sources.** No em dash, en dash, or smart quotes. A Unicode em dash in a SQL comment silently breaks downstream queries in sqlc v1.31.1. `make lint-ascii` surfaces hits.
- **Never `kill` a process you didn't start.** If a leftover dev process (e.g. `make tailwind-watch`, `go run ./cmd/server/`, a port collision on :8080) is interfering, ask the user whether they're still running it before terminating. They may be debugging or have a separate terminal pinned to it.
