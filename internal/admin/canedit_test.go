package admin_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
)

// TestCanEditQuiz pins the creator-or-super-admin edit predicate
// (#281/#319): the quiz creator may edit, any super admin may edit any
// quiz, and an unrelated regular admin may not edit a quiz they did not
// create.
func TestCanEditQuiz(t *testing.T) {
	t.Parallel()

	const creatorID = int64(42)

	tests := []struct {
		name    string
		player  *auth.Player
		present bool
		want    bool
	}{
		{
			name:    "creator allowed",
			player:  &auth.Player{ID: creatorID, Role: auth.RoleAdmin},
			present: true,
			want:    true,
		},
		{
			name:    "super admin allowed on another admin quiz",
			player:  &auth.Player{ID: 7, Role: auth.RoleAdmin, IsSuperAdmin: true},
			present: true,
			want:    true,
		},
		{
			name:    "unrelated regular admin denied",
			player:  &auth.Player{ID: 7, Role: auth.RoleAdmin},
			present: true,
			want:    false,
		},
		{
			name:    "no session player denied",
			present: false,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes/1", nil)
			if tt.present {
				req = req.WithContext(auth.WithPlayer(req.Context(), tt.player))
			}

			if got, want := admin.CanEditQuiz(req, creatorID), tt.want; got != want {
				t.Errorf("CanEditQuiz = %v, want %v", got, want)
			}
		})
	}
}
