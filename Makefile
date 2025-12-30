SHELL=/bin/bash -o pipefail

ifneq (,$(wildcard ./.env))
    include .env
    export
endif

.PHONY: check
check: lint build test

.PHONY: lint
lint:
	golangci-lint run

.PHONY: lint-fix
lint-fix:
	golangci-lint run --fix

.PHONY: build
build:
	mkdir -p bin
	go build -o bin/ ./...

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

.PHONY: clean
clean:
	rm -rf bin/