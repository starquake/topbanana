package admin_test

import (
	"testing"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
)

func TestAccountTypeLabel(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		row  *auth.PlayerListRow
		want string
	}{
		"admin role wins over everything": {
			row:  &auth.PlayerListRow{Role: auth.RoleAdmin, HasPassword: true, HasOAuth: true},
			want: "admin",
		},
		"password before oauth": {
			row: &auth.PlayerListRow{
				Role:          auth.RolePlayer,
				HasPassword:   true,
				HasOAuth:      true,
				OAuthProvider: "google",
			},
			want: "password",
		},
		"oauth with provider": {
			row:  &auth.PlayerListRow{Role: auth.RolePlayer, HasOAuth: true, OAuthProvider: "google"},
			want: "oauth (google)",
		},
		"oauth without provider falls back": {
			row:  &auth.PlayerListRow{Role: auth.RolePlayer, HasOAuth: true},
			want: "oauth",
		},
		"no credentials, no oauth, no admin role": {
			row:  &auth.PlayerListRow{Role: auth.RolePlayer},
			want: "anonymous",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got, want := AccountTypeLabel(tc.row), tc.want; got != want {
				t.Errorf("AccountTypeLabel = %q, want %q", got, want)
			}
		})
	}
}

func TestParsePageParam(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		raw  string
		want int
	}{
		"blank falls back to 1": {raw: "", want: 1},
		"negative clamps to 1":  {raw: "-3", want: 1},
		"zero clamps to 1":      {raw: "0", want: 1},
		"non-numeric clamps":    {raw: "abc", want: 1},
		"positive passes":       {raw: "7", want: 7},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got, want := ParsePageParam(tc.raw), tc.want; got != want {
				t.Errorf("ParsePageParam(%q) = %d, want %d", tc.raw, got, want)
			}
		})
	}
}

func TestTotalPagesFor(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		total, perPage int64
		want           int
	}{
		"zero rows yields zero pages":  {total: 0, perPage: 100, want: 0},
		"exact multiple":               {total: 200, perPage: 100, want: 2},
		"partial page rounds up":       {total: 250, perPage: 100, want: 3},
		"single row still one page":    {total: 1, perPage: 100, want: 1},
		"negative perPage yields zero": {total: 10, perPage: 0, want: 0},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got, want := TotalPagesFor(tc.total, tc.perPage), tc.want; got != want {
				t.Errorf("TotalPagesFor(%d, %d) = %d, want %d", tc.total, tc.perPage, got, want)
			}
		})
	}
}
