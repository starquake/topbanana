// Package migrations embeds the migration scripts
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
