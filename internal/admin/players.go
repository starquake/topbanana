package admin

import (
	"context"
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

// playerRow is one row in the admin players list template. Mirrors
// the shape of auth.PlayerListRow + auth.PlayerStats merged with the
// AccountType + onboarding-state labels pre-derived so the template
// stays declarative.
type playerRow struct {
	ID              int64
	Username        string
	Email           string
	AccountType     string
	OnboardingState string
	IsAdmin         bool
	CreatedAt       time.Time
	FinishedCount   int64
	LastFinishedAt  *time.Time
}

// playersStateTab is one entry in the filter tab strip rendered above
// the table (#450). Count is the number of players in the bucket;
// IsActive flags the tab matching the current ?state= filter so the
// template can highlight it.
type playersStateTab struct {
	Label    string
	State    string
	Count    int64
	IsActive bool
	URL      string
}

// playersPageData backs the playerslist.gohtml template.
type playersPageData struct {
	Title      string
	Players    []*playerRow
	Tabs       []playersStateTab
	State      string
	Page       int
	TotalPages int
	TotalRows  int64
	HasPrev    bool
	HasNext    bool
	PrevPage   int
	NextPage   int
	PrevURL    string
	NextURL    string
	RangeStart int64
	RangeEnd   int64
}

// HandlePlayersList renders /admin/players (#423/#450). One row per
// player with the derived account-type label, finished-quiz count, a
// link to the per-player detail view, and a tab strip filtering by
// onboarding state. Pagination is a simple ?page=N query param; page
// sizes above [playersPerPage] are not negotiable from the URL.
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
	ctx := r.Context()
	state := parseStateParam(r.URL.Query().Get("state"))
	page := parsePageParam(r.URL.Query().Get("page"))

	stateCounts, err := lister.CountPlayersByOnboardingState(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "error counting players by state", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return playersPageData{}, false
	}

	total, err := lister.CountPlayersInOnboardingState(ctx, state)
	if err != nil {
		logger.ErrorContext(ctx, "error counting players", slog.Any("err", err))
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
	rows, err := lister.ListPlayersByOnboardingState(ctx, state, playersPerPage, offset)
	if err != nil {
		logger.ErrorContext(ctx, "error listing players", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return playersPageData{}, false
	}

	players, err := buildPlayerRows(ctx, lister, rows)
	if err != nil {
		logger.ErrorContext(ctx, "error building player rows", slog.Any("err", err))
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
		Tabs:       buildStateTabs(state, stateCounts),
		State:      state,
		Page:       page,
		TotalPages: totalPages,
		TotalRows:  total,
		HasPrev:    page > 1,
		HasNext:    page < totalPages,
		PrevPage:   page - 1,
		NextPage:   page + 1,
		PrevURL:    playersPageURL(state, page-1),
		NextURL:    playersPageURL(state, page+1),
		RangeStart: rangeStart,
		RangeEnd:   offset + int64(len(players)),
	}, true
}

// buildPlayerRows merges a page of [auth.PlayerListRow] with the
// per-player finish-stats lookup into the template's playerRow shape.
// The finish-stats lookup is skipped on an empty page so the lister
// never sees a `WHERE id IN ()`.
func buildPlayerRows(
	ctx context.Context, lister auth.PlayerLister, rows []*auth.PlayerListRow,
) ([]*playerRow, error) {
	statsByID := map[int64]*auth.PlayerStats{}
	if len(rows) > 0 {
		ids := make([]int64, 0, len(rows))
		for _, p := range rows {
			ids = append(ids, p.ID)
		}
		stats, err := lister.ListPlayerFinishStats(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("list player finish stats: %w", err)
		}
		for _, s := range stats {
			statsByID[s.PlayerID] = s
		}
	}

	out := make([]*playerRow, 0, len(rows))
	for _, p := range rows {
		pr := &playerRow{
			ID:              p.ID,
			Username:        p.Username,
			Email:           p.Email,
			AccountType:     accountTypeLabel(p),
			OnboardingState: p.OnboardingState,
			IsAdmin:         p.Role == auth.RoleAdmin,
			CreatedAt:       p.CreatedAt,
		}
		if s, ok := statsByID[p.ID]; ok {
			pr.FinishedCount = s.FinishedCount
			pr.LastFinishedAt = s.LastFinishedAt
		}
		out = append(out, pr)
	}

	return out, nil
}

// parseStateParam clamps the ?state= query value to one of the known
// onboarding-state buckets. Anything else (blank, unknown, mixed case)
// falls back to [auth.OnboardingStateAll] so a hand-edited URL never
// produces an empty result page.
func parseStateParam(raw string) string {
	switch raw {
	case auth.OnboardingStateAnonymous,
		auth.OnboardingStateUnverified,
		auth.OnboardingStateOAuth,
		auth.OnboardingStateVerified:
		return raw
	default:
		return auth.OnboardingStateAll
	}
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

// playersPageURL composes a /admin/players URL preserving the state
// filter for the supplied page number. Used by the template to build
// pagination + tab links without inlining the encoding rule. The state
// is omitted when it is the default ("all") so a fresh-from-bookmark URL
// stays clean.
func playersPageURL(state string, page int) string {
	q := ""
	if state != auth.OnboardingStateAll {
		q = "?state=" + state
	}
	if page >= 1 {
		if q == "" {
			q = "?page="
		} else {
			q += "&page="
		}
		q += strconv.Itoa(page)
	}

	return "/admin/players" + q
}

// buildStateTabs constructs the tab-strip data: "All" first, then one
// tab per onboarding state in [auth.OnboardingStateValues] order. Counts
// come from a single GROUP BY round-trip (passed in as stateCounts);
// "All" is the sum across every known bucket so the value matches the
// caller's expectation even when a future bucket is missing from the
// map.
func buildStateTabs(activeState string, stateCounts map[string]int64) []playersStateTab {
	tabs := make([]playersStateTab, 0, 1+len(auth.OnboardingStateValues()))

	total := int64(0)
	for _, s := range auth.OnboardingStateValues() {
		total += stateCounts[s]
	}

	tabs = append(tabs, playersStateTab{
		Label:    "All",
		State:    auth.OnboardingStateAll,
		Count:    total,
		IsActive: activeState == auth.OnboardingStateAll,
		URL:      playersPageURL(auth.OnboardingStateAll, 0),
	})
	for _, s := range auth.OnboardingStateValues() {
		tabs = append(tabs, playersStateTab{
			Label:    stateLabel(s),
			State:    s,
			Count:    stateCounts[s],
			IsActive: activeState == s,
			URL:      playersPageURL(s, 0),
		})
	}

	return tabs
}

// stateLabel maps a [auth.OnboardingState*] constant to the
// title-cased label rendered on the tab strip. Kept here (not in the
// template) so a future relabel ("Unverified" -> "Awaiting verification")
// touches one Go function instead of every template file.
func stateLabel(state string) string {
	switch state {
	case auth.OnboardingStateAnonymous:
		return "Anonymous"
	case auth.OnboardingStateUnverified:
		return "Unverified"
	case auth.OnboardingStateOAuth:
		return "OAuth"
	case auth.OnboardingStateVerified:
		return "Verified"
	default:
		return state
	}
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
