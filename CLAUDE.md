AL# topbanana

## Commands

```bash
make check            # lint + sql-lint + build + all tests — run before every commit
make test             # unit tests only
make test-integration # integration tests (-tags=integration)
make test-e2e         # end-to-end browser tests (Playwright; requires Node.js)
make smoke            # validate startup against the existing dev DB (no HTTP listener)
```

## Commits and PRs

Per-change order of operations:

1. Implement the change.
2. Run the full local check suite: `make lint-fix`, `make check`, `make smoke`, `make test-e2e`. Fix anything they surface.
3. Run the `/review` + `/go-style-review` loop. Fix every actionable finding; re-run until both return clean. The diff that ships to GitHub must already be review-clean — findings landing as follow-up commits are friction we don't want. Hold the line on this step order even if asked "isn't the review supposed to be after push?": it is not.
4. Stage the files explicitly (`git add <paths>` — never `-A` or `.`), so secrets and binaries don't sneak in.
5. Commit with a plain-language subject line. Avoid jargon; prefer simple verbs ("change", "update", "fix", "add", "remove"); start with a capital letter; single short subject line, no body or rationale paragraphs.
6. Push the branch and open a draft PR. PR body follows "Linking a PR to a ticket" below.
7. Ask explicitly: "Did the review look OK?" or equivalent. Wait for the user's explicit go-ahead — silence is not consent. Do not merge before sign-off; do not merge while the PR is still a Draft.

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
- **Integration test** (`test/integration/`, `-tags=integration`) — anything that touches the real server, DB, HTTP routing, or embedded assets. The harness provides a real server, real DB, and cookie jars; add scenarios to an existing `*_test.go` or create a new one.
- **E2E test** (`test/e2e/`, Playwright) — behaviour that only makes sense in a real browser: clicks, navigation, form flows, JS-driven UI.

One-off scripts are reserved for genuinely interactive debugging that can't be expressed as a test (e.g. eyeballing a visual layout, attaching a debugger). If you find yourself writing the same `curl` twice, it's a test.

## Workflow

When starting work on a new ticket: switch to `main`, run `git pull`, then create a new branch. Branch names use dashes (not slashes) and are prefixed with the ticket number — e.g. `1-fix-bugs`. Ask for the ticket number if not provided. Omit the prefix if there is no ticket.
When asked to update a branch, do it by rebasing onto main (`git rebase origin/main`), never by merging.
Never suggest merging on your own and never infer that a merge is wanted (CI passing is not approval, and an earlier PR's merge instruction does not carry to the next PR). Run `gh pr merge` only when the user explicitly asks for *this* PR — phrases like "merge it", "go ahead and merge", or "commit, push, merge" in one breath count as that ask.
After each piece of work, provide the PR URL: `https://github.com/starquake/topbanana/compare/<branch-name>`
When implementing review findings or other non-trivial code changes, delegate to the appropriate dev agent — `backend-dev` for Go code under `internal/`, `cmd/`, migrations and queries; `frontend-dev` for the player client and admin templates — so project conventions are consistently applied.

## CI required checks

The `main` branch is protected: a PR cannot merge until every check in the required list has passed. The required list lives on the repo (set via `gh api -X PUT repos/starquake/topbanana/branches/main/protection`), not in this file, so it can drift silently. Current required contexts: `build`, `lint`, `e2e (chromium)`, `e2e (firefox)`.

**When you add or rename a workflow job under `.github/workflows/`**, update the required-checks list in the same PR. Otherwise the new job is purely advisory and a PR can merge while it fails. Update via:

```bash
gh api repos/starquake/topbanana/branches/main/protection | jq '.required_status_checks.contexts'   # show current
gh api -X PUT repos/starquake/topbanana/branches/main/protection --input <payload.json>             # replace
```

When removing a workflow job, drop its context from the required list too — leaving a stale required-context name makes every PR mergeable-blocked indefinitely.

## Deploys

There are two environments — **staging** and **production** — and they are **independent pipelines**, not a soak-and-promote chain. A change can be live in staging for weeks without ever reaching production; production never auto-promotes from staging.

What triggers what (`.github/workflows/deploy.yml`):

- **Staging deploys on every merge to `main`.** The `Docker` workflow builds an image tagged `edge` (from `main`) plus the commit SHA, and a successful Docker run on the `main` branch fires the `deploy-staging` job. The staging compose pulls `edge`; goose runs any pending migrations on container boot; a 12×5s health-check loop gates the deploy as successful.
- **Production deploys when a `v*.*.*` git tag is pushed.** The Docker workflow builds an image tagged `{version}` (e.g. `2026.5.8`) plus `{major}.{minor}`; a successful Docker run on a `v*` tag fires the `deploy-production` job, which pulls that exact version tag. There is no "promote whatever is in staging" button — production is whatever the `v*.*.*` tag points at.
- **Manual trigger** is also available via `workflow_dispatch` with an `environment` input (`staging` or `production`) — used to redeploy without a new code change.

Practical consequences for work in flight:

- A PR merge means staging gets the change within minutes. Production stays on the last tag until a new one is cut.
- A schema migration on `main` runs on staging the next time the container boots. The same migration runs on production the next time a tag is released and deployed. **Do not assume "merged to main" means "live in production"** — only "live in staging".
- A risky change can sit on staging across multiple `main` merges before it ships. The risk window is when the tag is cut; all changes since the previous tag deploy together.
- Both jobs build a fresh `.env` on the server from GitHub secrets + variables. **Secrets** are values that should be masked in logs (`SESSION_KEY`, `GOOGLE_CLIENT_SECRET`, `SMTP_PASSWORD`, ...). **Variables** are not (`BASE_URL`, `REGISTRATION_ENABLED`, `ADMIN_EMAILS`). Both kinds are scoped per-environment in GitHub Actions — the staging job sees the `staging` environment's secrets+vars, the production job sees `production`'s. A value set on `staging` is not visible to `production` and vice versa.

## Comments

Default to writing **no** comments. Code with well-named identifiers, small functions, and clear control flow explains *what* it does — that's not the comment's job. A comment earns its place only when the *why* is non-obvious: a hidden constraint, a subtle invariant, a workaround for a specific bug, behaviour that would surprise a reader.

If removing the comment wouldn't confuse a future reader, don't write it.

- Don't restate the code (`// increment i`, `// open the file`, `// the X package` above a `package x` declaration).
- Don't reference the current task, fix, or caller (`// used by X`, `// added for Y`, `// handles the case from issue #123`) — those belong in the PR description and rot as the codebase evolves.
- Don't write multi-paragraph rationale or step-by-step narration. One short line per real `why` is usually enough.
- Issue links are fine when they're load-bearing: `// see #165` stays accurate because issues don't move.

When in doubt, leave the comment out. A reviewer who finds the code unclear will ask, and that's a better signal of what actually needs a comment.

## Comments that reach across files

WHY comments are encouraged when the rationale isn't obvious from the code. But a WHY that explains *what the other side of the system expects* is fragile — if the other side changes, the comment silently lies. The reader trusts it, and is misled.

Treat cross-file rationale by category:

1. **Server explains what the frontend will do with this** — high rot risk. **Rewrite as self-contained why**. Explain the local rationale only: "this column tracks whether the player explicitly chose their username; defaults false so callers can tell auto-generated names apart from claimed ones." Lose the "the frontend gates X on this" framing.

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

Table rebuilds (the SQLite idiom for `ALTER COLUMN`, FK changes, etc.) must use `PRAGMA defer_foreign_keys = ON`, not `PRAGMA foreign_keys = OFF`. The deferred form keeps FK enforcement on and just postpones the check until COMMIT, so a broken rebuild fails loudly during the migration rather than silently leaving dangling references. See migration `20260520180000_unique_participant_per_player_quiz.sql` for the canonical pattern. Two pre-rule migrations (`20260506000000_add_player_auth_columns.sql` and `20260520200000_quiz_creator.sql`) are grandfathered.

Run `make lint-migrations` to surface any new migration that violates the convention. The target is advisory (exit 0 on hit; only prints offenders) and excludes the grandfathered ones.

## Hard rules

- **Do not edit `internal/db/`** — generated by `sqlc generate` from `internal/queries/*.sql`.
- **No SQL in Go files** — add queries to `internal/queries/` and regenerate.
- **No third-party HTTP framework or ORM.**
- **ASCII only in `.go` and `.sql` sources.** No em dash, en dash, or smart quotes. A Unicode em dash in a SQL comment silently breaks downstream queries in sqlc v1.31.1. `make lint-ascii` surfaces hits.
- **Never `kill` a process you didn't start.** If a leftover dev process (e.g. `make tailwind-watch`, `go run ./cmd/server/`, a port collision on :8080) is interfering, ask the user whether they're still running it before terminating. They may be debugging or have a separate terminal pinned to it.
