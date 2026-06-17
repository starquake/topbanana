package auth

import "strings"

// LooksLikeEmail is the shared shape check used by the register flow,
// the verify-resend gate, and the in-session email-change flow (#497).
// Deliberately loose: one '@', non-empty local part, host with a dot
// that does not start or end with one. Tight validation belongs at the
// SMTP / DNS layer, not in the form handler.
func LooksLikeEmail(s string) bool {
	if s == "" {
		return false
	}
	local, host, ok := strings.Cut(s, "@")
	if !ok || local == "" || host == "" {
		return false
	}
	if strings.Count(s, "@") > 1 {
		return false
	}
	if strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	if !strings.Contains(host, ".") {
		return false
	}

	return true
}
