// Package tmpl embeds the application templates
package tmpl

import "embed"

// FS is the embedded filesystem
//
//go:embed components/*.gohtml admin/layouts/*.gohtml admin/errors/*.gohtml admin/pages/*.gohtml admin/partials/*.gohtml auth/layouts/*.gohtml auth/pages/*.gohtml home/layouts/*.gohtml home/pages/*.gohtml host/layouts/*.gohtml host/pages/*.gohtml
var FS embed.FS
