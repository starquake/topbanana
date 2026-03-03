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

.PHONY: test-coverage
test-coverage:
	mkdir -p $(COV_DIR)
	go test -v -race -tags=integration -coverpkg=$(shell go list ./... | grep -v -E "dbtest|testutil" | paste -sd "," -) -coverprofile=$(COV_DIR)/coverage.out ./...
	go tool cover -func=$(COV_DIR)/coverage.out

.PHONY: test-coverage-html
test-coverage-html: test-coverage
	go tool cover -html=$(COV_DIR)/coverage.out

.PHONY: clean
clean:
	rm -rf $(BUILD_DIR)