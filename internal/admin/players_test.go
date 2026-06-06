package admin_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
)

func TestHandlePlayersList_RendersRows(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	// One row per account type so the rendered table exercises every
	// AccountTypeLabel branch. The admin row created first claims the
	// "first credentialled registrant" admin slot explicitly via the
	// requested role, so the OAuth and password rows below stay in their
	// intended tiers.
	env.seedVerifiedPlayer(t, "adminuser", "admin@example.test", auth.RoleAdmin)
	env.seedOAuthPlayer(t, "alice", "alice@example.test", "google", "sub-alice")
	env.seedCredentialledPlayer(t, "bob", "bob@example.test", auth.RolePlayer)
	env.seedPlayer(t, "anon-xyz")

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/players", nil)
	HandlePlayersList(slog.New(slog.DiscardHandler), nil, env.lister).ServeHTTP(w, req)

	if got, want := w.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	body := w.Body.String()
	for _, want := range []string{"admin", "password", "oauth (google)", "anonymous", "alice", "bob", "anon-xyz"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q; body=%q", want, body)
		}
	}
}

func TestHandlePlayersList_RendersRoleBadges(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	// The first credentialled registrant claims the admin slot; a host row
	// (promoted after creation, since host is assignment-only) and a
	// plain-player row pin the badge mapping. The badges carry the literal
	// role word inside a <span>, so the assertions match the rendered tag
	// rather than the bare word (which also appears as the account-type
	// label).
	env.seedVerifiedPlayer(t, "adminuser", "admin@example.test", auth.RoleAdmin)
	env.seedHostPlayer(t, "hostuser", "host@example.test")
	env.seedVerifiedPlayer(t, "playeruser", "player@example.test", auth.RolePlayer)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/players", nil)
	HandlePlayersList(slog.New(slog.DiscardHandler), nil, env.lister).ServeHTTP(w, req)

	if got, want := w.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	body := w.Body.String()
	if got, want := body, ">admin</span>"; !strings.Contains(got, want) {
		t.Errorf("body missing admin badge %q; body=%q", want, got)
	}
	if got, want := body, ">host</span>"; !strings.Contains(got, want) {
		t.Errorf("body missing host badge %q; body=%q", want, got)
	}
}

// TestPlayerRowBadgeFlags pins the role-to-badge-flag mapping directly:
// admin sets only IsAdmin, host sets only IsHost, plain player sets
// neither. A row is exactly one role, so at most one badge ever shows.
func TestPlayerRowBadgeFlags(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	adminID := env.seedVerifiedPlayerID(t, "adminuser", "admin@example.test", auth.RoleAdmin)
	hostID := env.seedHostPlayer(t, "hostuser", "host@example.test")
	playerID := env.seedVerifiedPlayerID(t, "playeruser", "player@example.test", auth.RolePlayer)

	ctx := t.Context()
	listed, err := env.lister.ListPlayersByOnboardingState(ctx, auth.OnboardingStateAll, 1000, 0)
	if err != nil {
		t.Fatalf("ListPlayersByOnboardingState err = %v, want nil", err)
	}
	built, err := BuildPlayerRows(ctx, env.lister, listed)
	if err != nil {
		t.Fatalf("BuildPlayerRows err = %v, want nil", err)
	}
	rows := make(map[int64]*PlayerRow, len(built))
	for _, r := range built {
		rows[r.ID] = r
	}

	for _, tc := range []struct {
		name                string
		id                  int64
		wantAdmin, wantHost bool
	}{
		{name: "admin", id: adminID, wantAdmin: true, wantHost: false},
		{name: "host", id: hostID, wantAdmin: false, wantHost: true},
		{name: "player", id: playerID, wantAdmin: false, wantHost: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			row := rows[tc.id]
			if row == nil {
				t.Fatalf("no player row for id %d", tc.id)
			}
			if got, want := row.IsAdmin, tc.wantAdmin; got != want {
				t.Errorf("IsAdmin = %v, want %v", got, want)
			}
			if got, want := row.IsHost, tc.wantHost; got != want {
				t.Errorf("IsHost = %v, want %v", got, want)
			}
		})
	}
}

func TestHandlePlayersList_EmptyList(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	// Filter on a bucket no seeded row falls into so the list renders
	// empty even though the migration seeds an admin row.
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/admin/players?state=oauth", nil,
	)
	HandlePlayersList(slog.New(slog.DiscardHandler), nil, env.lister).ServeHTTP(w, req)

	if got, want := w.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := w.Body.String(), "No players match this filter."; !strings.Contains(got, want) {
		t.Errorf("body should contain empty-state %q; body=%q", want, got)
	}
}

func TestHandlePlayersList_StoreErrorRenders500(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	env.closeStore(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/players", nil)
	HandlePlayersList(slog.New(slog.DiscardHandler), nil, env.lister).ServeHTTP(w, req)

	if got, want := w.Code, http.StatusInternalServerError; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

func TestHandlePlayersList_RequestsCorrectOffset(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	// Seed one full page plus one extra anonymous row. page=2 must apply
	// offset = PlayersPerPage, so exactly the single overflow row lands
	// on the second page. The marker name pins which row that is.
	const marker = "page2-marker"
	env.seedPlayer(t, marker)
	for i := range PlayersPerPage {
		env.seedPlayer(t, fmt.Sprintf("filler-%03d", i))
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/players?page=2", nil)
	HandlePlayersList(slog.New(slog.DiscardHandler), nil, env.lister).ServeHTTP(w, req)

	if got, want := w.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	body := w.Body.String()
	// The marker is the oldest seeded row, so created_at DESC ordering
	// pushes it onto page 2 once the page-1 offset is applied.
	if got, want := body, marker; !strings.Contains(got, want) {
		t.Errorf("page 2 body should contain the overflow row %q", want)
	}
	// A page-1 filler must not appear: that would mean offset=0 leaked in.
	if got, want := body, "filler-099"; strings.Contains(body, want) {
		t.Errorf("page 2 body should not contain page-1 row %q", got)
	}
}

func TestHandlePlayersList_FilterStatePassedToStore(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	// One unverified (password, not verified) row and one verified row.
	// ?state=unverified must list only the former. The names are chosen
	// so neither is a substring of the other.
	env.seedCredentialledPlayer(t, "pending-pat", "pat@example.test", auth.RolePlayer)
	env.seedVerifiedPlayer(t, "confirmed-casey", "casey@example.test", auth.RolePlayer)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/admin/players?state=unverified", nil,
	)
	HandlePlayersList(slog.New(slog.DiscardHandler), nil, env.lister).ServeHTTP(w, req)

	if got, want := w.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	body := w.Body.String()
	if got, want := body, "pending-pat"; !strings.Contains(got, want) {
		t.Errorf("filtered body should contain %q", want)
	}
	if strings.Contains(body, "confirmed-casey") {
		t.Errorf("filtered body should not contain the verified row; body=%q", body)
	}
}

func TestHandlePlayersList_UnknownStateFallsBackToAll(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	// A bogus ?state= must behave like "all", so both a credentialled and
	// an anonymous row appear.
	env.seedCredentialledPlayer(t, "cred-user", "cred@example.test", auth.RolePlayer)
	env.seedPlayer(t, "anon-user")

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/admin/players?state=bogus", nil,
	)
	HandlePlayersList(slog.New(slog.DiscardHandler), nil, env.lister).ServeHTTP(w, req)

	if got, want := w.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	body := w.Body.String()
	for _, want := range []string{"cred-user", "anon-user"} {
		if !strings.Contains(body, want) {
			t.Errorf("fallback-to-all body should contain %q; body=%q", want, body)
		}
	}
}

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
