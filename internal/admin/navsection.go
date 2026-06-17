package admin

import "strings"

// navSection maps a request path to the admin nav section it belongs to,
// so the shared top bar can mark the active section. The empty string
// means the overview at /admin (no inline link is active).
func navSection(path string) string {
	switch {
	case strings.HasPrefix(path, "/admin/quizzes"):
		return "quizzes"
	case strings.HasPrefix(path, "/admin/players"):
		return "players"
	case strings.HasPrefix(path, "/admin/invites"):
		return "invites"
	case strings.HasPrefix(path, "/admin/email"):
		return "email"
	case strings.HasPrefix(path, "/admin/settings"):
		return "settings"
	default:
		return ""
	}
}
