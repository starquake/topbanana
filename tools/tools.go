//go:build tools

// Package tools is a stub that exists so dependabot's gomod ecosystem can
// track the pinned versions of CLI tools we install via release binaries
// (see GOLANGCI_VERSION in the Makefile). The `tools` build tag keeps
// the import out of normal builds — running `go build ./tools/...`
// without `-tags=tools` is a no-op.
//
// When a dependabot PR bumps a version here, the maintainer must also
// bump the matching variable in the Makefile and the `with: version:`
// in the corresponding GitHub Actions workflow. There is no automation
// for that mirror — flag the manual step in the PR.
package tools

import (
	_ "github.com/golangci/golangci-lint/v2/cmd/golangci-lint"
)
