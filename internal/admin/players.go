package admin

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
)

// playersPerPage is the page size of the admin players list (#423).
// The acceptance criteria pick 100 as a "reasonable default"; the
// constant lives here so the template's "Showing N of M" math and the
// store call share one value.
const playersPerPage = 100

// PlayerRow is one row in the admin players list template. Mirrors the
// shape of auth.PlayerListRow + auth.PlayerStats merged together with
// the AccountType label pre-derived so the template stays declarative.
type PlayerRow struct {
	ID             int64
	Username       string
	Email          string
	AccountType    string
	CreatedAt      time.Time
	FinishedCount  int
	LastFinishedAt *time.Time
}

// playersPageData backs the playerslist.gohtml template.
type playersPageData struct {
	Title      string
	Players    []*PlayerRow
	Page       int
	TotalPages int
	TotalRows  int64
	HasPrev    bool
	HasNext    bool
	PrevPage   int
	NextPage   int
	RangeStart int64
	RangeEnd   int64
}

// HandlePlayersList renders /admin/players (#423). One row per player
// with the derived account-type label, finished-quiz count, and a link
// to the (future) per-player detail view. Pagination is a simple
// ?page=N query param; page sizes above [playersPerPage] are not
// negotiable from the URL.
func HandlePlayersList(logger *slog.Logger, csrfMgr *csrf.Manager, lister auth.PlayerLister) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/playerslist.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := loadPlayersPage(w, r, logger, csrfMgr, lister)
		if !ok {
			return
		}
		render.Render(w, r, http.StatusOK, data)
	})
}

// loadPlayersPage runs the count + list + stats queries for the admin
// players list and builds the template data. Split out of
// HandlePlayersList so the handler closure stays under revive's
// function-length limit; on any store failure it writes a 500 page and
// returns ok=false so the caller can early-return.
func loadPlayersPage(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	lister auth.PlayerLister,
) (playersPageData, bool) {
	page := parsePageParam(r.URL.Query().Get("page"))

	total, err := lister.CountAllPlayers(r.Context())
	if err != nil {
		logger.ErrorContext(r.Context(), "error counting players", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return playersPageData{}, false
	}

	totalPages := totalPagesFor(total, playersPerPage)
	if totalPages == 0 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	offset := int64(page-1) * playersPerPage
	rows, err := lister.ListAllPlayers(r.Context(), playersPerPage, offset)
	if err != nil {
		logger.ErrorContext(r.Context(), "error listing players", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return playersPageData{}, false
	}

	players, err := buildPlayerRows(r, lister, rows)
	if err != nil {
		logger.ErrorContext(r.Context(), "error loading finish stats", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return playersPageData{}, false
	}

	rangeStart := int64(0)
	if len(players) > 0 {
		rangeStart = offset + 1
	}

	return playersPageData{
		Title:      "Admin Dashboard - Players",
		Players:    players,
		Page:       page,
		TotalPages: totalPages,
		TotalRows:  total,
		HasPrev:    page > 1,
		HasNext:    page < totalPages,
		PrevPage:   page - 1,
		NextPage:   page + 1,
		RangeStart: rangeStart,
		RangeEnd:   offset + int64(len(players)),
	}, true
}

// buildPlayerRows merges a page of [auth.PlayerListRow] with the
// per-player finish-stats lookup into the template's PlayerRow shape.
func buildPlayerRows(
	r *http.Request, lister auth.PlayerLister, rows []*auth.PlayerListRow,
) ([]*PlayerRow, error) {
	ids := make([]int64, 0, len(rows))
	for _, p := range rows {
		ids = append(ids, p.ID)
	}
	statsByID, err := finishStatsByID(r, lister, ids)
	if err != nil {
		return nil, err
	}

	out := make([]*PlayerRow, 0, len(rows))
	for _, p := range rows {
		pr := &PlayerRow{
			ID:          p.ID,
			Username:    p.Username,
			Email:       p.Email,
			AccountType: accountTypeLabel(p),
			CreatedAt:   p.CreatedAt,
		}
		if s, ok := statsByID[p.ID]; ok {
			pr.FinishedCount = s.FinishedCount
			pr.LastFinishedAt = s.LastFinishedAt
		}
		out = append(out, pr)
	}

	return out, nil
}

// parsePageParam clamps the ?page= query value to a sensible positive
// integer. Anything blank, non-numeric, or below 1 falls back to page 1
// so a hand-edited URL never crashes the handler.
func parsePageParam(raw string) int {
	if raw == "" {
		return 1
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 1
	}

	return n
}

// totalPagesFor returns the ceiling of total/perPage. Zero rows yields
// zero pages so the handler can decide whether to surface a "no players"
// state or pin to page 1.
func totalPagesFor(total, perPage int64) int {
	if total <= 0 || perPage <= 0 {
		return 0
	}

	return int((total + perPage - 1) / perPage)
}

// finishStatsByID issues the per-page finish-stats lookup and returns a
// playerID-keyed map for O(1) row enrichment. An empty id slice short-
// circuits the store call so the lister never sees a `WHERE id IN ()`.
func finishStatsByID(
	r *http.Request, lister auth.PlayerLister, ids []int64,
) (map[int64]*auth.PlayerStats, error) {
	if len(ids) == 0 {
		return map[int64]*auth.PlayerStats{}, nil
	}
	stats, err := lister.ListPlayerFinishStats(r.Context(), ids)
	if err != nil {
		return nil, fmt.Errorf("list player finish stats: %w", err)
	}
	out := make(map[int64]*auth.PlayerStats, len(stats))
	for _, s := range stats {
		out[s.PlayerID] = s
	}

	return out, nil
}

// accountTypeLabel maps a player row's role + credential + OAuth state
// onto the user-facing label the acceptance criteria spell out (#423).
// Order matches the spec: admin role wins over everything else, then
// a password hash, then any OAuth identity, then "anonymous" for the
// EnsurePlayer-minted rows that never claimed an identity.
func accountTypeLabel(p *auth.PlayerListRow) string {
	switch {
	case p.Role == auth.RoleAdmin:
		return "admin"
	case p.HasPassword:
		return "password"
	case p.HasOAuth:
		if p.OAuthProvider != "" {
			return "oauth (" + p.OAuthProvider + ")"
		}

		return "oauth"
	default:
		return "anonymous"
	}
}
