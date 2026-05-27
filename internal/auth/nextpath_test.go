package auth_test

import (
	"testing"

	"github.com/starquake/topbanana/internal/auth"
)

func TestSafeNextPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"simple absolute path", "/admin/email", "/admin/email"},
		{"absolute path with query", "/admin/email?q=1", "/admin/email?q=1"},
		{"absolute path with fragment", "/admin/email#section", "/admin/email#section"},
		{"deep path", "/admin/quizzes/42/questions/new", "/admin/quizzes/42/questions/new"},
		{"protocol-relative URL", "//evil.com/", ""},
		{"http URL", "http://evil.com/", ""},
		{"https URL", "https://evil.com/", ""},
		{"javascript scheme", "javascript:alert(1)", ""},
		{"data scheme", "data:text/html,hi", ""},
		{"backslash path", "\\evil.com", ""},
		{"mixed-slash path", "/admin\\evil.com", ""},
		{"percent-encoded backslash lower", "/%5cevil.com", ""},
		{"percent-encoded backslash upper", "/%5Cevil.com", ""},
		{"loops back to login", "/login", ""},
		{"loops back to login with query", "/login?next=/login", ""},
		{"loops back to register", "/register", ""},
		{"relative without leading slash", "admin/email", ""},
		{"single dot", ".", ""},
		{"parent dir", "..", ""},
		{"empty after slash is fine", "/", "/"},
		{"opaque", "mailto:x@example.test", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, want := auth.SafeNextPath(tt.in), tt.want; got != want {
				t.Errorf("SafeNextPath(%q) = %q, want %q", tt.in, got, want)
			}
		})
	}
}
