SHELL=/bin/bash -o pipefail

# include .env

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

.PHONY: test
	go test -v ./...

.PHONY: clean
	rm -rf bin/