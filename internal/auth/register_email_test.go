package auth_test

import (
	"testing"

	"github.com/starquake/topbanana/internal/auth"
)

// TestLooksLikeEmail pins the validator the register form runs before
// hitting the store (#111 PR1). Loose by design - tight validation
// belongs at the SMTP / DNS layer - but tight enough to catch the
// obvious typos.
func TestLooksLikeEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"simple", "alice@example.test", true},
		{"subdomain", "alice@mail.example.test", true},
		{"plus addressing", "alice+work@example.test", true},
		{"numeric local", "12345@example.test", true},
		{"empty", "", false},
		{"no at sign", "aliceexample.test", false},
		{"two at signs", "alice@@example.test", false},
		{"local missing", "@example.test", false},
		{"host missing", "alice@", false},
		{"no dot in host", "alice@localhost", false},
		{"dot at start of host", "alice@.example", false},
		{"dot at start of multi-label host", "alice@.example.com", false},
		{"empty TLD", "alice@example.", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, want := auth.LooksLikeEmail(tt.in), tt.want; got != want {
				t.Errorf("LooksLikeEmail(%q) = %v, want %v", tt.in, got, want)
			}
		})
	}
}
