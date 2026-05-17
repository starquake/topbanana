SHELL=/bin/bash -o pipefail

ifneq (,$(wildcard ./.env))
    include .env
    export
endif

BUILD_DIR := build
BIN_DIR := $(BUILD_DIR)/bin
COV_DIR := $(BUILD_DIR)/coverage

# Developer check before committing
.PHONY: check
check: lint sql-lint tailwind-check build test-coverage

.PHONY: lint
lint:
	golangci-lint run

.PHONY: lint-fix
lint-fix:
	golangci-lint run --fix

.PHONY: sql-lint
sql-lint:
	sqlc vet

.PHONY: build
build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/ ./...

# Run only unit tests (excludes files with //go:build integration)
.PHONY: test
test:
	go test -v -race ./...

# Run only integration tests
.PHONY: test-integration
test-integration:
	go test -v -race -tags=integration ./test/integration/...

# Run all tests
.PHONY: test-all
test-all:
	go test -v -race -tags=integration ./...

# Run tests matching the given name regex. Pass NAME=TestFoo to filter, or
# NAME='TestFoo/sub-case' for a sub-test. The integration build tag is set
# so both unit and integration tests are reachable.
#
# Example: make test-one NAME=TestConfig_SecureCookies
.PHONY: test-one
test-one:
	@test -n "$(NAME)" || { echo "Usage: make test-one NAME=TestFoo"; exit 1; }
	go test -v -race -tags=integration -run '$(NAME)' ./...

.PHONY: test-coverage
test-coverage:
	mkdir -p $(COV_DIR)
	go test -v -race -tags=integration -coverpkg=$(shell go list ./... | grep -v -E "dbtest|testutil" | paste -sd "," -) -coverprofile=$(COV_DIR)/coverage.out ./...
	go tool cover -func=$(COV_DIR)/coverage.out

.PHONY: test-coverage-html
test-coverage-html: test-coverage
	go tool cover -html=$(COV_DIR)/coverage.out

# Run end-to-end browser tests. Requires Node.js and Playwright. The
# `npx playwright install` step downloads Chromium on first run.
.PHONY: test-e2e
test-e2e:
	cd test/e2e && npm ci && npx playwright install chromium firefox && npm test

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
# The generated output (internal/web/static/css/admin.css) IS committed,
# so the binary only has to exist on machines that intend to regenerate it.
# CI can call `make tailwind-check` to catch drift.

TAILWIND_VERSION    := v4.3.0
TAILWIND_BIN        := $(BIN_DIR)/tailwindcss-v4
TAILWIND_INPUT      := internal/web/static/css/_tailwind.css
TAILWIND_OUTPUT     := internal/web/static/css/admin.css

# Pick the right asset for the current host. The release page ships
# Linux x64/arm64, macOS x64/arm64, and Windows x64 — which covers every
# machine we care about. Other hosts fall through to the Linux x64 binary
# (works under WSL).
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)
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
# would diverge from the committed admin.css and fail tailwind-check.
.PHONY: tailwind-watch
tailwind-watch: $(TAILWIND_BIN)
	$(TAILWIND_BIN) -i $(TAILWIND_INPUT) -o $(TAILWIND_OUTPUT) --watch --minify

# Regenerate into a temp file and diff against the committed admin.css. Wired
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

# Run the Go server in development. Pair with `make tailwind-watch` in a
# second terminal to regenerate admin.css on template edits.
.PHONY: server
server:
	go run ./cmd/server/

.PHONY: clean
clean:
	rm -rf $(BUILD_DIR)
