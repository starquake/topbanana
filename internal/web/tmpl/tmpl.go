// Package tmpl embeds the application templates
package tmpl

import "embed"

// FS is the embedded filesystem
//
//go:embed admin/layouts/*.gohtml admin/errors/*.gohtml admin/pages/*.gohtml admin/partials/*.gohtml auth/layouts/*.gohtml auth/pages/*.gohtml home/layouts/*.gohtml home/pages/*.gohtml
var FS embed.FS
