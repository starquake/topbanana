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
check: lint sql-lint build test-coverage

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

.PHONY: clean
clean:
	rm -rf $(BUILD_DIR)
