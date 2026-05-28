package admin_test

import (
	"testing"

	. "github.com/starquake/topbanana/internal/admin"
)

func TestNavSection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "dashboard", path: "/admin", want: ""},
		{name: "dashboard trailing slash", path: "/admin/", want: ""},
		{name: "quizzes list", path: "/admin/quizzes", want: "quizzes"},
		{name: "quiz detail", path: "/admin/quizzes/42", want: "quizzes"},
		{name: "quiz new", path: "/admin/quizzes/new", want: "quizzes"},
		{name: "quiz import", path: "/admin/quizzes/import", want: "quizzes"},
		{name: "players list", path: "/admin/players", want: "players"},
		{name: "player detail", path: "/admin/players/7", want: "players"},
		{name: "email", path: "/admin/email", want: "email"},
		{name: "email test", path: "/admin/email/test", want: "email"},
		{name: "unknown section", path: "/admin/other", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got, want := NavSection(tt.path), tt.want; got != want {
				t.Errorf("NavSection(%q) = %q, want %q", tt.path, got, want)
			}
		})
	}
}
