SHELL=/bin/bash -o pipefail

ifneq (,$(wildcard ./.env))
    include .env
    export
endif

# APP_ENV unset means fail-secure (Secure cookies, SESSION_KEY required) —
# the production-safe default. For local dev that's friction, so default it
# to "development" here. ?= keeps whatever .env or the parent shell set;
# only an actually-unset value gets the development override.
APP_ENV ?= development
export APP_ENV

BUILD_DIR := build
BIN_DIR := $(BUILD_DIR)/bin
COV_DIR := $(BUILD_DIR)/coverage

# Host detection. Used by both the Tailwind and golangci-lint download
# targets to pick the right release asset. Defined here once instead of
# inside each tool section so future binary pins can reuse the values.
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)

# golangci-lint version + binary path. Defined up here (not next to the
# download rule lower down) because Make expands prerequisites at parse
# time: if `lint: $(GOLANGCI_BIN)` runs before this variable is defined,
# the prereq evaluates to empty and the download never fires. The
# version MUST match `with: version:` in the lint job of .github/workflows/ci.yml
# — bump both together; dependabot does not track this field.
GOLANGCI_VERSION := v2.12.2
GOLANGCI_BIN     := $(BIN_DIR)/golangci-lint

# sqlc version + binary path. Same parse-time-expansion reason for the
# placement. Dependabot watches /tools/go.mod for new releases; mirror
# any bump there into the version below.
SQLC_VERSION := v1.31.1
SQLC_BIN     := $(BIN_DIR)/sqlc

# mailpit version + binary path. Used by the e2e suite as a local SMTP
# catch-all so the browser tests can drive the real verify and invite
# email round-trips (the app sends to mailpit; the specs read the
# message back over mailpit's HTTP API). Pinned like the Tailwind binary:
# mailpit is not a Go module tool, so dependabot does not track it - bump
# the version here manually.
MAILPIT_VERSION := v1.30.1
MAILPIT_BIN     := $(BIN_DIR)/mailpit

# Developer check before committing. Includes smoke (#349) so the
# migration-against-existing-data class of bug — which test-coverage's
# fresh DB can't catch — fails locally before CI does.
.PHONY: check
check: lint lint-ascii sql-lint sqlc-check tailwind-check build test-coverage smoke

.PHONY: lint
lint: $(GOLANGCI_BIN)
	$(GOLANGCI_BIN) run

.PHONY: lint-fix
lint-fix: $(GOLANGCI_BIN)
	$(GOLANGCI_BIN) run --fix

.PHONY: sql-lint
sql-lint: $(SQLC_BIN)
	$(SQLC_BIN) vet

# Regenerate the sqlc layer (internal/db/) from internal/queries/.
# Hand-editing internal/db/ is a hard rule violation; run this after any
# query change and commit the result.
.PHONY: sqlc-generate
sqlc-generate: $(SQLC_BIN)
	$(SQLC_BIN) generate

# Fail when internal/db/ is out of sync with internal/queries/. `sqlc
# vet` only lints the queries; it does not catch a query that was edited
# or deleted without re-running generate, so stale generated code can
# ship (it did once - the deleted ListAllPlayers/CountAllPlayers funcs
# lingered in internal/db/players.sql.go). Mirrors the tailwind-check
# gap-filler for the CSS layer.
#
# `sqlc diff` compares freshly-generated output against the files on disk
# and exits non-zero when they differ - in-memory, without writing or
# consulting git. The previous `generate` + `git diff` approach conflated
# "stale" with "regenerated but not yet committed", so every feature
# branch that touched internal/queries/ failed this check locally until
# the result was committed (a false failure on #546/#548). Comparing the
# generated output to the working tree fixes that while staying correct
# on CI, where the files are already committed.
.PHONY: sqlc-check
sqlc-check: $(SQLC_BIN)
	@$(SQLC_BIN) diff || { \
	    echo "ERROR: internal/db is out of date - run \`make sqlc-generate\` to regenerate it."; \
	    exit 1; \
	}
	@echo "internal/db is up to date."

# Advisory grep for cross-file rationale in comments (#177). Server-side
# comments that explain what the frontend does with a value rot silently
# when the frontend changes. This target surfaces candidates to rewrite;
# it never fails the build. Test files are excluded because a test
# comment is pinned by the test itself. internal/db/ is excluded because
# it's regenerated from internal/queries/.
.PHONY: lint-cross-refs
lint-cross-refs:
	@rg -n '(?i)\b(frontend|client-side)\b' \
	   --type go --type sql \
	   -g '!*_test.go' -g '!internal/db/**' \
	   internal/ || echo "no cross-file refs found"

# Advisory lint (#360): flags any migration that disables FK
# enforcement wholesale via `PRAGMA foreign_keys = OFF` instead of
# the documented `PRAGMA defer_foreign_keys = ON`. Excludes the
# grandfathered 20260506000000 migration that pre-dates the rule.
# Advisory: prints hits, never fails the build. Grep instead of rg
# so contributors without ripgrep installed still get the signal.
.PHONY: lint-migrations
lint-migrations:
	@hits=$$(grep -lE 'PRAGMA[[:space:]]+foreign_keys[[:space:]]*=[[:space:]]*OFF' \
	    internal/migrations/*.sql 2>/dev/null \
	    | grep -vE '20260506000000_add_player_auth_columns\.sql|20260520200000_quiz_creator\.sql|20260528100000_require_email_for_credentialled_players\.sql|20260529160000_roles_player_host_admin\.sql|20260530000000_add_rounds\.sql' \
	    || true); \
	if [ -n "$$hits" ]; then \
	    echo "lint-migrations: the following migrations use PRAGMA foreign_keys = OFF;"; \
	    echo "                 prefer PRAGMA defer_foreign_keys = ON (see CLAUDE.md)."; \
	    echo "$$hits" | sed 's/^/  /'; \
	else \
	    echo "lint-migrations: no offending migrations."; \
	fi

# Fails the build on non-ASCII bytes in Go/SQL sources (sqlc v1.31.1
# breaks downstream queries on em-dashed SQL comments; CLAUDE.md hard
# rule). golangci-lint's asciicheck only covers identifiers, so this
# grep is the gate for comments and string literals. Wired into `check`
# and the CI build job (#483).
.PHONY: lint-ascii
lint-ascii:
	@hits=$$(LC_ALL=C grep -rnP '[^\x00-\x7f]' \
	    --include='*.go' --include='*.sql' \
	    --exclude-dir='internal/db' \
	    internal/ cmd/ 2>/dev/null || true); \
	if [ -n "$$hits" ]; then \
	    echo "lint-ascii: non-ASCII bytes in source (em dashes break sqlc; see CLAUDE.md):"; \
	    echo "$$hits" | sed 's/^/  /'; \
	    exit 1; \
	else \
	    echo "lint-ascii: no non-ASCII bytes in Go or SQL sources."; \
	fi

.PHONY: build
build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/ ./...

# Fast suite: integration tests skip under -short (via the dbtest /
# startServer choke points), so this runs only the pure-logic tests.
.PHONY: test
test:
	go test -v -short -race ./...

# Everything, including integration tests (no -short, so the choke-point
# skips do not fire).
.PHONY: test-integration
test-integration:
	go test -v -race ./...

# Run all tests
.PHONY: test-all
test-all:
	go test -v -race ./...

# Run tests matching the given name regex. Pass NAME=TestFoo to filter, or
# NAME='TestFoo/sub-case' for a sub-test. No -short, so integration tests
# are reachable too.
#
# Example: make test-one NAME=TestConfig_SecureCookies
.PHONY: test-one
test-one:
	@test -n "$(NAME)" || { echo "Usage: make test-one NAME=TestFoo"; exit 1; }
	go test -v -race -run '$(NAME)' ./...

# Coverage must run the full suite (no -short) so integration tests count
# toward the .testcoverage.yml threshold.
.PHONY: test-coverage
test-coverage:
	mkdir -p $(COV_DIR)
	go test -v -race -coverpkg=$(shell go list ./... | grep -v -E "dbtest|testutil" | paste -sd "," -) -coverprofile=$(COV_DIR)/coverage.out ./...
	go tool cover -func=$(COV_DIR)/coverage.out

.PHONY: test-coverage-html
test-coverage-html: test-coverage
	go tool cover -html=$(COV_DIR)/coverage.out

# Run end-to-end browser tests. Requires Node.js and Playwright. The
# `npx playwright install` step downloads Chromium on first run.
#
# Chromium and Firefox each get their own Playwright invocation forked
# into the background so they run in parallel locally — same shape as
# the CI matrix (#252). Each invocation:
#   - listens on a distinct port (TOPBANANA_E2E_PORT) so the two
#     webServer instances do not collide on :8181
#   - lets playwright.config.ts mint its own SQLite temp dir
#     (TOPBANANA_E2E_DATA_DIR is left unset so the config's
#     mkdtempSync fallback fires per-invocation)
# Total wall-clock cost on a local box is whichever browser is
# slower, not the sum.
#
# Stdout interleaves but Playwright's `list` reporter prefixes every
# line with the browser project, so it stays readable.
#
# Exit status is the OR of the two child statuses so make fails if
# either browser fails.
#
# Shell gotcha: `cd test/e2e && cmd1 & cmd2` is parsed as
# `(cd test/e2e && cmd1) & cmd2`, so the cd only applies to the
# backgrounded subshell — cmd2 then runs in the original cwd and
# fails to find playwright.config.ts. The body below uses
# `cd test/e2e || exit 1;` instead so the cd lands in the parent
# shell and both backgrounded playwright runs inherit it.
.PHONY: test-e2e
test-e2e: $(MAILPIT_BIN)
	cd test/e2e && npm ci && npx playwright install chromium firefox
	cd test/e2e && TOPBANANA_MAILPIT_BIN=$(abspath $(MAILPIT_BIN)) npx playwright test

# Smoke-test the binary against the existing dev DB: parses config, opens
# the DB, runs migrations, and exits 0. Catches startup / migration / config
# regressions that `make check` (fresh DB only) wouldn't surface.
#
# On a fresh checkout the dev DB doesn't exist; SQLite creates it and goose
# applies every migration, so the first `make smoke` run also seeds an empty
# topbanana.sqlite in the working directory. Subsequent runs reuse it.
.PHONY: smoke
smoke:
	go run ./cmd/server/ -check

# Populate the local dev DB from dev/fixtures/quizzes.json so the
# player and admin UIs are easy to eyeball with realistic content. The
# seeder is idempotent on quizzes (re-running skips slug collisions
# via quiz.ErrSlugTaken), but each invocation creates a fresh batch of
# anonymous players + finished games — so repeated runs grow the
# popular and active-players lists.
.PHONY: seed-dev
seed-dev:
	go run ./cmd/seed-dev/

# --- Tailwind ---------------------------------------------------------------
#
# We use the Tailwind CLI v4 standalone binary so there is no Node.js, npm,
# or node_modules in this repo. The binary is downloaded into $(BIN_DIR) on
# first use (which is gitignored via build/) and reused on subsequent runs.
#
# v4 dropped tailwind.config.js — configuration now lives in CSS via the
# @theme directive in internal/web/static/css/_tailwind.css. The leading
# underscore tells go:embed to skip the file (the same convention Go uses
# for test helpers), so the source CSS is not shipped in the binary even
# though it sits next to the generated output.
#
# The generated output (internal/web/static/css/app.css) IS committed,
# so the binary only has to exist on machines that intend to regenerate it.
# CI can call `make tailwind-check` to catch drift.

TAILWIND_VERSION    := v4.3.0
TAILWIND_BIN        := $(BIN_DIR)/tailwindcss-v4
TAILWIND_INPUT      := internal/web/static/css/_tailwind.css
TAILWIND_OUTPUT     := internal/web/static/css/app.css

# Pick the right asset for the current host. The release page ships
# Linux x64/arm64, macOS x64/arm64, and Windows x64 — which covers every
# machine we care about. Other hosts fall through to the Linux x64 binary
# (works under WSL). UNAME_S / UNAME_M live at the top of the file.
ifeq ($(UNAME_S),Linux)
    ifeq ($(UNAME_M),aarch64)
        TAILWIND_ASSET := tailwindcss-linux-arm64
    else
        TAILWIND_ASSET := tailwindcss-linux-x64
    endif
else ifeq ($(UNAME_S),Darwin)
    ifeq ($(UNAME_M),arm64)
        TAILWIND_ASSET := tailwindcss-macos-arm64
    else
        TAILWIND_ASSET := tailwindcss-macos-x64
    endif
else
    TAILWIND_ASSET := tailwindcss-linux-x64
endif

$(TAILWIND_BIN):
	@mkdir -p $(BIN_DIR)
	@echo "Downloading Tailwind CLI $(TAILWIND_VERSION) ($(TAILWIND_ASSET))..."
	curl -sSfL -o $@ \
	    https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/$(TAILWIND_ASSET)
	chmod +x $@

.PHONY: tailwind
tailwind: $(TAILWIND_BIN)
	$(TAILWIND_BIN) -i $(TAILWIND_INPUT) -o $(TAILWIND_OUTPUT) --minify

# --watch + --minify so every rebuild matches what `make tailwind` would
# produce — without --minify the watcher emits pretty-printed CSS that
# would diverge from the committed app.css and fail tailwind-check.
.PHONY: tailwind-watch
tailwind-watch: $(TAILWIND_BIN)
	$(TAILWIND_BIN) -i $(TAILWIND_INPUT) -o $(TAILWIND_OUTPUT) --watch --minify

# Regenerate into a temp file and diff against the committed app.css. Wired
# into `make check` so a template class change without `make tailwind` fails
# pre-commit instead of slipping into a PR.
.PHONY: tailwind-check
tailwind-check: $(TAILWIND_BIN)
	@tmp=$$(mktemp) && \
	    $(TAILWIND_BIN) -i $(TAILWIND_INPUT) -o $$tmp --minify 2>/dev/null && \
	    if ! diff -q $$tmp $(TAILWIND_OUTPUT) >/dev/null; then \
	        echo "ERROR: $(TAILWIND_OUTPUT) is out of date — run \`make tailwind\` and commit the result."; \
	        diff -u $(TAILWIND_OUTPUT) $$tmp || true; \
	        rm -f $$tmp; \
	        exit 1; \
	    fi; \
	    rm -f $$tmp; \
	    echo "$(TAILWIND_OUTPUT) is up to date."

# --- golangci-lint -----------------------------------------------------------
#
# GOLANGCI_VERSION / GOLANGCI_BIN are defined at the top of the file so
# the `lint` and `lint-fix` targets can use them as prerequisites; the
# rest of the wiring (host asset selection + download rule) lives here.
#
# Same vendoring pattern as Tailwind: download once into $(BIN_DIR)
# (gitignored via build/), reuse on every subsequent run.

# The release tarball strips the leading `v` from the version in both the
# directory name and the asset filename — keep a stripped copy for the URL.
GOLANGCI_VER_NUM := $(GOLANGCI_VERSION:v%=%)

ifeq ($(UNAME_S),Linux)
    ifeq ($(UNAME_M),aarch64)
        GOLANGCI_ASSET := linux-arm64
    else
        GOLANGCI_ASSET := linux-amd64
    endif
else ifeq ($(UNAME_S),Darwin)
    ifeq ($(UNAME_M),arm64)
        GOLANGCI_ASSET := darwin-arm64
    else
        GOLANGCI_ASSET := darwin-amd64
    endif
else
    GOLANGCI_ASSET := linux-amd64
endif

GOLANGCI_DIR     := golangci-lint-$(GOLANGCI_VER_NUM)-$(GOLANGCI_ASSET)
GOLANGCI_TARBALL := $(GOLANGCI_DIR).tar.gz

$(GOLANGCI_BIN):
	@mkdir -p $(BIN_DIR)
	@echo "Downloading golangci-lint $(GOLANGCI_VERSION) ($(GOLANGCI_ASSET))..."
	@tmp=$$(mktemp -d) && \
	    curl -sSfL -o $$tmp/golangci.tar.gz \
	        https://github.com/golangci/golangci-lint/releases/download/$(GOLANGCI_VERSION)/$(GOLANGCI_TARBALL) && \
	    tar -xzf $$tmp/golangci.tar.gz -C $$tmp && \
	    mv $$tmp/$(GOLANGCI_DIR)/golangci-lint $@ && \
	    rm -rf $$tmp
	@chmod +x $@

# --- sqlc --------------------------------------------------------------------
#
# SQLC_VERSION / SQLC_BIN are defined at the top of the file for the same
# parse-time-expansion reason as GOLANGCI_BIN. Asset naming differs from
# golangci-lint (sqlc uses underscores and ships the binary at the root
# of the tarball, not inside a versioned directory), so the two sections
# don't share a target.

SQLC_VER_NUM := $(SQLC_VERSION:v%=%)

ifeq ($(UNAME_S),Linux)
    ifeq ($(UNAME_M),aarch64)
        SQLC_ASSET := linux_arm64
    else
        SQLC_ASSET := linux_amd64
    endif
else ifeq ($(UNAME_S),Darwin)
    ifeq ($(UNAME_M),arm64)
        SQLC_ASSET := darwin_arm64
    else
        SQLC_ASSET := darwin_amd64
    endif
else
    SQLC_ASSET := linux_amd64
endif

SQLC_TARBALL := sqlc_$(SQLC_VER_NUM)_$(SQLC_ASSET).tar.gz

$(SQLC_BIN):
	@mkdir -p $(BIN_DIR)
	@echo "Downloading sqlc $(SQLC_VERSION) ($(SQLC_ASSET))..."
	@tmp=$$(mktemp -d) && \
	    curl -sSfL -o $$tmp/sqlc.tar.gz \
	        https://github.com/sqlc-dev/sqlc/releases/download/$(SQLC_VERSION)/$(SQLC_TARBALL) && \
	    tar -xzf $$tmp/sqlc.tar.gz -C $$tmp && \
	    mv $$tmp/sqlc $@ && \
	    rm -rf $$tmp
	@chmod +x $@

# --- mailpit -----------------------------------------------------------------
#
# Release assets are mailpit-<os>-<arch>.tar.gz with the binary at the
# tarball root, so the download mirrors the sqlc target. The asset arch
# tokens are amd64/arm64 (like golangci-lint), not sqlc's underscore form.

ifeq ($(UNAME_S),Linux)
    ifeq ($(UNAME_M),aarch64)
        MAILPIT_ASSET := mailpit-linux-arm64
    else
        MAILPIT_ASSET := mailpit-linux-amd64
    endif
else ifeq ($(UNAME_S),Darwin)
    ifeq ($(UNAME_M),arm64)
        MAILPIT_ASSET := mailpit-darwin-arm64
    else
        MAILPIT_ASSET := mailpit-darwin-amd64
    endif
else
    MAILPIT_ASSET := mailpit-linux-amd64
endif

MAILPIT_TARBALL := $(MAILPIT_ASSET).tar.gz

$(MAILPIT_BIN):
	@mkdir -p $(BIN_DIR)
	@echo "Downloading mailpit $(MAILPIT_VERSION) ($(MAILPIT_ASSET))..."
	@tmp=$$(mktemp -d) && \
	    curl -sSfL -o $$tmp/mailpit.tar.gz \
	        https://github.com/axllent/mailpit/releases/download/$(MAILPIT_VERSION)/$(MAILPIT_TARBALL) && \
	    tar -xzf $$tmp/mailpit.tar.gz -C $$tmp && \
	    mv $$tmp/mailpit $@ && \
	    rm -rf $$tmp
	@chmod +x $@

# Run the Go server in development. Pair with `make tailwind-watch` in a
# second terminal to regenerate app.css on template edits.
.PHONY: server
server:
	go run ./cmd/server/

# Run the Go server with auto-restart on source changes. Watches cmd/,
# internal/, and the go.mod/go.sum pair; ignores the generated sqlc
# output and the regenerated Tailwind bundle so a `make tailwind-watch`
# in a second terminal doesn't bounce the server. SIGTERM lets the
# server's shutdown handler drain in-flight requests before exit.
# watchexec is user-installed (see CLI tools in CLAUDE.md / README).
.PHONY: dev
dev:
	@command -v watchexec >/dev/null 2>&1 || { echo "watchexec not found — install from https://github.com/watchexec/watchexec"; exit 1; }
	watchexec \
	    --restart \
	    --stop-signal SIGTERM \
	    --watch cmd \
	    --watch internal \
	    --watch go.mod \
	    --watch go.sum \
	    --ignore 'internal/db/**' \
	    --ignore 'internal/web/static/css/app.css' \
	    -- go run ./cmd/server/

.PHONY: clean
clean:
	rm -rf $(BUILD_DIR)
