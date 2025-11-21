// Package tmpl embeds the application templates
package tmpl

import "embed"

// FS is the embedded filesystem
//
//go:embed admin/layouts/*.gohtml admin/pages/*.gohtml
var FS embed.FS
