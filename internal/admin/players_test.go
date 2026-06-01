package admin_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
)

// stubPlayerLister satisfies auth.PlayerLister for the admin handler
// tests. Each function field is set per-test so failures are localised
// to the case under exercise; unset fields return harmless zero values.
type stubPlayerLister struct {
	listPlayersByOnboardingState func(
		ctx context.Context, state string, limit, offset int64,
	) ([]*auth.PlayerListRow, error)
	countPlayersInOnboardingState func(ctx context.Context, state string) (int64, error)
	countPlayersByOnboardingState func(ctx context.Context) (map[string]int64, error)
	listPlayerFinishStats         func(ctx context.Context, ids []int64) ([]*auth.PlayerStats, error)
}

func (s stubPlayerLister) ListPlayersByOnboardingState(
	ctx context.Context, state string, limit, offset int64,
) ([]*auth.PlayerListRow, error) {
	if s.listPlayersByOnboardingState == nil {
		return nil, errors.New("listPlayersByOnboardingState not supplied in stub")
	}

	return s.listPlayersByOnboardingState(ctx, state, limit, offset)
}

func (s stubPlayerLister) CountPlayersInOnboardingState(ctx context.Context, state string) (int64, error) {
	if s.countPlayersInOnboardingState == nil {
		return 0, errors.New("countPlayersInOnboardingState not supplied in stub")
	}

	return s.countPlayersInOnboardingState(ctx, state)
}

func (s stubPlayerLister) CountPlayersByOnboardingState(ctx context.Context) (map[string]int64, error) {
	if s.countPlayersByOnboardingState == nil {
		return nil, errors.New("countPlayersByOnboardingState not supplied in stub")
	}

	return s.countPlayersByOnboardingState(ctx)
}

func (s stubPlayerLister) ListPlayerFinishStats(
	ctx context.Context, ids []int64,
) ([]*auth.PlayerStats, error) {
	if s.listPlayerFinishStats == nil {
		return nil, errors.New("listPlayerFinishStats not supplied in stub")
	}

	return s.listPlayerFinishStats(ctx, ids)
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

// emptyStateCounts returns a state-count map with every known bucket
// at zero. Test helpers use it as the baseline so adding a bucket only
// touches this helper.
func emptyStateCounts() map[string]int64 {
	return map[string]int64{
		auth.OnboardingStateAnonymous:  0,
		auth.OnboardingStateUnverified: 0,
		auth.OnboardingStateOAuth:      0,
		auth.OnboardingStateVerified:   0,
	}
}

func TestHandlePlayersList_RendersRows(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	finishedAt := time.Date(2026, 5, 20, 18, 30, 0, 0, time.UTC)
	rows := []*auth.PlayerListRow{
		{
			ID: 1, DisplayName: "admin", Email: "a@example.com", Role: auth.RoleAdmin,
			HasPassword: true, CreatedAt: createdAt, OnboardingState: auth.OnboardingStateVerified,
		},
		{
			ID: 2, DisplayName: "alice", Email: "alice@example.com", Role: auth.RolePlayer,
			HasOAuth: true, OAuthProvider: "google", CreatedAt: createdAt,
			OnboardingState: auth.OnboardingStateOAuth,
		},
		{
			ID: 3, DisplayName: "bob", Email: "bob@example.com", Role: auth.RolePlayer,
			HasPassword: true, CreatedAt: createdAt, OnboardingState: auth.OnboardingStateUnverified,
		},
		{
			ID: 4, DisplayName: "anon-xyz", Role: auth.RolePlayer, CreatedAt: createdAt,
			OnboardingState: auth.OnboardingStateAnonymous,
		},
	}
	stats := []*auth.PlayerStats{
		{PlayerID: 2, FinishedCount: 4, LastFinishedAt: &finishedAt},
	}

	lister := stubPlayerLister{
		countPlayersInOnboardingState: func(_ context.Context, _ string) (int64, error) {
			return int64(len(rows)), nil
		},
		countPlayersByOnboardingState: func(_ context.Context) (map[string]int64, error) {
			counts := emptyStateCounts()
			counts[auth.OnboardingStateVerified] = 1
			counts[auth.OnboardingStateOAuth] = 1
			counts[auth.OnboardingStateUnverified] = 1
			counts[auth.OnboardingStateAnonymous] = 1

			return counts, nil
		},
		listPlayersByOnboardingState: func(
			_ context.Context, _ string, _, _ int64,
		) ([]*auth.PlayerListRow, error) {
			return rows, nil
		},
		listPlayerFinishStats: func(_ context.Context, _ []int64) ([]*auth.PlayerStats, error) {
			return stats, nil
		},
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/players", nil)
	HandlePlayersList(slog.New(slog.DiscardHandler), nil, lister).ServeHTTP(w, req)

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

func TestHandlePlayersList_EmptyList(t *testing.T) {
	t.Parallel()

	lister := stubPlayerLister{
		countPlayersInOnboardingState: func(_ context.Context, _ string) (int64, error) {
			return 0, nil
		},
		countPlayersByOnboardingState: func(_ context.Context) (map[string]int64, error) {
			return emptyStateCounts(), nil
		},
		listPlayersByOnboardingState: func(
			_ context.Context, _ string, _, _ int64,
		) ([]*auth.PlayerListRow, error) {
			return nil, nil
		},
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/players", nil)
	HandlePlayersList(slog.New(slog.DiscardHandler), nil, lister).ServeHTTP(w, req)

	if got, want := w.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := w.Body.String(), "No players match this filter."; !strings.Contains(got, want) {
		t.Errorf("body should contain empty-state %q; body=%q", want, got)
	}
}

func TestHandlePlayersList_StoreErrorRenders500(t *testing.T) {
	t.Parallel()

	lister := stubPlayerLister{
		countPlayersByOnboardingState: func(_ context.Context) (map[string]int64, error) {
			return nil, errors.New("boom")
		},
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/players", nil)
	HandlePlayersList(slog.New(slog.DiscardHandler), nil, lister).ServeHTTP(w, req)

	if got, want := w.Code, http.StatusInternalServerError; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

func TestHandlePlayersList_RequestsCorrectOffset(t *testing.T) {
	t.Parallel()

	var gotLimit, gotOffset int64
	lister := stubPlayerLister{
		countPlayersInOnboardingState: func(_ context.Context, _ string) (int64, error) {
			return int64(PlayersPerPage*3) + 5, nil
		},
		countPlayersByOnboardingState: func(_ context.Context) (map[string]int64, error) {
			return emptyStateCounts(), nil
		},
		listPlayersByOnboardingState: func(
			_ context.Context, _ string, limit, offset int64,
		) ([]*auth.PlayerListRow, error) {
			gotLimit, gotOffset = limit, offset

			return nil, nil
		},
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/players?page=2", nil)
	HandlePlayersList(slog.New(slog.DiscardHandler), nil, lister).ServeHTTP(w, req)

	if got, want := gotLimit, int64(PlayersPerPage); got != want {
		t.Errorf("limit = %d, want %d", got, want)
	}
	if got, want := gotOffset, int64(PlayersPerPage); got != want {
		t.Errorf("offset = %d, want %d", got, want)
	}
}

func TestHandlePlayersList_FilterStatePassedToStore(t *testing.T) {
	t.Parallel()

	var gotCountState, gotListState string
	lister := stubPlayerLister{
		countPlayersInOnboardingState: func(_ context.Context, state string) (int64, error) {
			gotCountState = state

			return 0, nil
		},
		countPlayersByOnboardingState: func(_ context.Context) (map[string]int64, error) {
			return emptyStateCounts(), nil
		},
		listPlayersByOnboardingState: func(
			_ context.Context, state string, _, _ int64,
		) ([]*auth.PlayerListRow, error) {
			gotListState = state

			return nil, nil
		},
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/admin/players?state=unverified", nil,
	)
	HandlePlayersList(slog.New(slog.DiscardHandler), nil, lister).ServeHTTP(w, req)

	if got, want := gotCountState, auth.OnboardingStateUnverified; got != want {
		t.Errorf("count state = %q, want %q", got, want)
	}
	if got, want := gotListState, auth.OnboardingStateUnverified; got != want {
		t.Errorf("list state = %q, want %q", got, want)
	}
}

func TestHandlePlayersList_UnknownStateFallsBackToAll(t *testing.T) {
	t.Parallel()

	var gotState string
	lister := stubPlayerLister{
		countPlayersInOnboardingState: func(_ context.Context, state string) (int64, error) {
			gotState = state

			return 0, nil
		},
		countPlayersByOnboardingState: func(_ context.Context) (map[string]int64, error) {
			return emptyStateCounts(), nil
		},
		listPlayersByOnboardingState: func(
			_ context.Context, _ string, _, _ int64,
		) ([]*auth.PlayerListRow, error) {
			return nil, nil
		},
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/admin/players?state=bogus", nil,
	)
	HandlePlayersList(slog.New(slog.DiscardHandler), nil, lister).ServeHTTP(w, req)

	if got, want := gotState, auth.OnboardingStateAll; got != want {
		t.Errorf("state = %q, want %q", got, want)
	}
}
