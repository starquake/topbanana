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
check: lint lint-ascii sql-lint sqlc-check tailwind-check js-check build test-coverage smoke

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
	    | grep -vE '20260506000000_add_player_auth_columns\.sql|20260520200000_quiz_creator\.sql|20260528100000_require_email_for_credentialled_players\.sql|20260529160000_roles_player_host_admin\.sql|20260530000000_add_rounds\.sql|20260606120000_session_runner\.sql|20260607120000_session_round_results\.sql|20260611120000_persistent_rooms\.sql|20260612120000_session_quiz_nullable\.sql' \
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
	go test -v -race -coverpkg=$(shell go list ./... | grep -v -E "dbtest|testutil|seed-dev|internal/db$$" | paste -sd "," -) -coverprofile=$(COV_DIR)/coverage.out ./...
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
# @theme directive in frontend/web/css/tailwind.css. The source lives under
# frontend/ (alongside the JS source), not in the served internal/*/static
# trees, so the embed never ships it; only the generated output below is
# committed and embedded.
#
# The generated output (internal/assets/static/css/app.css) IS committed,
# so the binary only has to exist on machines that intend to regenerate it.
# CI can call `make tailwind-check` to catch drift.

TAILWIND_VERSION    := v4.3.0
TAILWIND_BIN        := $(BIN_DIR)/tailwindcss-v4
TAILWIND_INPUT      := frontend/web/css/tailwind.css
TAILWIND_OUTPUT     := internal/assets/static/css/app.css

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
	curl -sSfL --retry 5 --retry-delay 2 --retry-all-errors -o $@ \
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

# --- esbuild (player client + web/host JS bundles) ---------------------------
#
# Both the player client and the web/host app JS are bundled per entry point
# with esbuild and the minified output is committed (like app.css), so the
# binary embeds the bundles and the distroless image needs no Node at build
# time. Source stays as plain ES modules under frontend/{client,web}; only the
# bundled output lands in the served internal/*/static trees (#721). The
# genuinely-duplicated client logic (server clock, the per-question countdown,
# the standings bar graph, the share dialog) lives once in frontend/shared and
# is inlined into each tree's bundle through the @shared alias, so neither
# bundle fetches a module from the other tree at runtime (#721, slice 2).
#
# esbuild is a dev-only build tool, installed from the committed
# package.json + package-lock.json into node_modules/ (gitignored). The
# JS_DEPS sentinel reinstalls only when the lockfile changes.

ESBUILD_BIN     := node_modules/.bin/esbuild
JS_DEPS         := node_modules/.package-lock.json

# Source lives under frontend/; the bundles are emitted into the served
# internal/*/static/js/dist trees and committed.
JS_CLIENT_SRC   := frontend/client
JS_CLIENT_OUT   := internal/client/static/js/dist
JS_CLIENT_ENTRIES := $(JS_CLIENT_SRC)/app.js $(JS_CLIENT_SRC)/join.js

JS_WEB_SRC      := frontend/web
JS_WEB_OUT      := internal/assets/static/js/dist
# host-bigscreen + share are the live-game / share bundles; the rest are small
# standalone admin/auth page scripts that are bundled (minified) too, so the
# served js tree holds only built output and never un-minified source (#756).
JS_WEB_ENTRIES  := $(JS_WEB_SRC)/host-bigscreen.js $(JS_WEB_SRC)/share.js \
                   $(JS_WEB_SRC)/cooldown.js $(JS_WEB_SRC)/copy-prompt.js \
                   $(JS_WEB_SRC)/password-length.js $(JS_WEB_SRC)/quiz-reorder.js \
                   $(JS_WEB_SRC)/home.js

# Third-party libraries (Alpine, anime.js, SortableJS) are sourced from the
# pinned npm packages (package.json) and re-emitted by esbuild as standalone
# classic scripts at the served vendor paths the templates reference (#897).
# Each entry under frontend/vendor imports its npm package and assigns the
# upstream global (window.Alpine / window.anime / window.Sortable), so the
# template <script> tags and window.* access are unchanged -- only the source of
# the committed file moved from a hand-dropped download to the npm build. The
# web vendor dir gets all three. The player client loads Alpine + anime too, but
# from this same web-served copy at /static/js/vendor/ rather than a per-client
# duplicate, so only this one tree is built (the client shells reference the
# /static/ URLs; see internal/client/static/index.html and join.html).
JS_VENDOR_SRC     := frontend/vendor
JS_VENDOR_WEB_OUT := internal/assets/static/js/vendor
JS_VENDOR_WEB_ENTRIES := $(JS_VENDOR_SRC)/alpine.min.js \
                   $(JS_VENDOR_SRC)/anime.umd.min.js $(JS_VENDOR_SRC)/sortable.min.js

# Alpine 3 targets modern evergreen browsers; es2020 matches the syntax the
# source already uses (async/await, optional chaining). The @shared alias
# resolves the cross-tree modules in frontend/shared so each bundle inlines
# them and stays self-contained.
ESBUILD_FLAGS   := --bundle --minify --format=esm --target=es2020 \
                   --alias:@shared=./frontend/shared

# The vendor libs load as classic <script> tags (not type="module") and expose
# window.* globals, so they bundle as self-executing IIFEs rather than ESM.
ESBUILD_VENDOR_FLAGS := --bundle --minify --format=iife --target=es2020

$(JS_DEPS): package.json package-lock.json
	npm ci
	@touch $@

.PHONY: js
js: $(JS_DEPS) js-vendor
	$(ESBUILD_BIN) $(JS_CLIENT_ENTRIES) $(ESBUILD_FLAGS) --outdir=$(JS_CLIENT_OUT)
	$(ESBUILD_BIN) $(JS_WEB_ENTRIES) $(ESBUILD_FLAGS) --outdir=$(JS_WEB_OUT)

# Re-emit the npm-sourced vendor libs at their served paths. The committed
# output is drift-checked by js-check, like the app bundles and app.css.
.PHONY: js-vendor
js-vendor: $(JS_DEPS)
	$(ESBUILD_BIN) $(JS_VENDOR_WEB_ENTRIES) $(ESBUILD_VENDOR_FLAGS) --outdir=$(JS_VENDOR_WEB_OUT)

# Rebuild on change during development. One target per long-running watcher
# (the client and web bundles write to different served dist dirs, so they
# can't share a single esbuild invocation). Each mirrors `make js` flags so
# the watcher output matches what `make js` produces and never drifts from the
# committed bundles.
.PHONY: js-watch-client
js-watch-client: $(JS_DEPS)
	$(ESBUILD_BIN) $(JS_CLIENT_ENTRIES) $(ESBUILD_FLAGS) --outdir=$(JS_CLIENT_OUT) --watch

.PHONY: js-watch-web
js-watch-web: $(JS_DEPS)
	$(ESBUILD_BIN) $(JS_WEB_ENTRIES) $(ESBUILD_FLAGS) --outdir=$(JS_WEB_OUT) --watch

# Rebuild each tree into a temp dir and diff against the committed bundles.
# Wired into `make check` so a JS change without `make js` fails pre-commit
# instead of shipping a stale bundle. Mirrors tailwind-check; covers both the
# client and web/host bundles.
.PHONY: js-check
js-check: $(JS_DEPS)
	@$(MAKE) --no-print-directory js-check-one \
	    JS_CHECK_ENTRIES="$(JS_CLIENT_ENTRIES)" JS_CHECK_OUT="$(JS_CLIENT_OUT)" \
	    JS_CHECK_FLAGS="$(ESBUILD_FLAGS)"
	@$(MAKE) --no-print-directory js-check-one \
	    JS_CHECK_ENTRIES="$(JS_WEB_ENTRIES)" JS_CHECK_OUT="$(JS_WEB_OUT)" \
	    JS_CHECK_FLAGS="$(ESBUILD_FLAGS)"
	@$(MAKE) --no-print-directory js-check-one \
	    JS_CHECK_ENTRIES="$(JS_VENDOR_WEB_ENTRIES)" JS_CHECK_OUT="$(JS_VENDOR_WEB_OUT)" \
	    JS_CHECK_FLAGS="$(ESBUILD_VENDOR_FLAGS)"

.PHONY: js-check-one
js-check-one:
	@tmp=$$(mktemp -d) && \
	    $(ESBUILD_BIN) $(JS_CHECK_ENTRIES) $(JS_CHECK_FLAGS) --outdir=$$tmp >/dev/null 2>&1 && \
	    if ! diff -rq $$tmp $(JS_CHECK_OUT) >/dev/null; then \
	        echo "ERROR: $(JS_CHECK_OUT) is out of date — run \`make js\` and commit the result."; \
	        diff -ru $(JS_CHECK_OUT) $$tmp || true; \
	        rm -rf $$tmp; \
	        exit 1; \
	    fi; \
	    rm -rf $$tmp; \
	    echo "$(JS_CHECK_OUT) is up to date."

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
	    curl -sSfL --retry 5 --retry-delay 2 --retry-all-errors -o $$tmp/golangci.tar.gz \
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
	    curl -sSfL --retry 5 --retry-delay 2 --retry-all-errors -o $$tmp/sqlc.tar.gz \
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
	    curl -sSfL --retry 5 --retry-delay 2 --retry-all-errors -o $$tmp/mailpit.tar.gz \
	        https://github.com/axllent/mailpit/releases/download/$(MAILPIT_VERSION)/$(MAILPIT_TARBALL) && \
	    tar -xzf $$tmp/mailpit.tar.gz -C $$tmp && \
	    mv $$tmp/mailpit $@ && \
	    rm -rf $$tmp
	@chmod +x $@

# Build stamp for local `go run` dev (#663). Unlike `go build`, `go run`
# does NOT embed VCS info, so without these ldflags the admin footer and
# /version would show the commit as "unknown". Mirrors the Dockerfile's
# ldflags (the version package reads them); Commit gets a "-dirty" marker
# when the tree differs from HEAD. `make build` needs none of this - it
# uses `go build`, which stamps VCS for free.
VERSION_PKG := github.com/starquake/topbanana/internal/version
VERSION_LDFLAGS := -X $(VERSION_PKG).Version=$(shell cat VERSION 2>/dev/null) \
                   -X $(VERSION_PKG).Commit=$(shell git rev-parse HEAD 2>/dev/null)$(shell git diff --quiet HEAD 2>/dev/null || echo -dirty) \
                   -X $(VERSION_PKG).Date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Run the Go server in development. Pair with `make tailwind-watch` in a
# second terminal to regenerate app.css on template edits.
.PHONY: server
server:
	go run -ldflags "$(VERSION_LDFLAGS)" ./cmd/server/

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
	    --shell=none \
	    --watch cmd \
	    --watch internal \
	    --watch go.mod \
	    --watch go.sum \
	    --ignore 'internal/db/**' \
	    --ignore 'internal/assets/static/css/app.css' \
	    -- go run -ldflags "$(VERSION_LDFLAGS)" ./cmd/server/

.PHONY: clean
clean:
	rm -rf $(BUILD_DIR)
