package auth_test

import (
	"testing"

	"github.com/starquake/topbanana/internal/auth"
)

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
