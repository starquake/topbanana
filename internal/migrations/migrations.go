// Package migrations embeds the migration scripts
package migrations

import "embed"

// FS is the embedded filesystem
//
//go:embed *.sql
var FS embed.FS
