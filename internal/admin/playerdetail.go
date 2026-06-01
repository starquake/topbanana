package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/handlers"
)

// PlayerDetailFlashCookieName is the per-target one-shot banner cookie
// for /admin/players/{id}. Path scopes the cookie to /admin so the
// player detail PRG hops and a same-path back-arrow both see it; the
// browser drops it on the next read.
const (
	PlayerDetailFlashCookieName = "topbanana_admin_player_flash"
	PlayerDetailFlashCookiePath = "/admin"
)

// recentFinishedGamesLimit caps the "Last finished games" excerpt on
// the per-player detail view. The ticket asks for five; the constant
// lives here so the SQL caller, the template's heading, and any future
// tests share one value.
const recentFinishedGamesLimit = 5

// adminAuditLimit caps the "Recent admin actions" excerpt. Same
// rationale as recentFinishedGamesLimit.
const adminAuditLimit = 20

// AdminResendVerificationCooldown is the per-target cool-down between
// consecutive admin-initiated resend-verification clicks. A stuck
// operator pounding the button should not turn the page into an
// outbound mail floodgate (#321 / #450); 60s mirrors the public
// verify-resend window.
const AdminResendVerificationCooldown = 60 * time.Second

// playerDetailData backs the playerdetail.gohtml template.
type playerDetailData struct {
	Title          string
	Player         playerDetailRow
	RecentGames    []playerDetailGame
	AuditEntries   []playerDetailAudit
	CanVerify      bool
	CanResend      bool
	Notice         string
	Error          string
	EmailFormValue string
}

type playerDetailRow struct {
	ID                int64
	DisplayName       string
	Email             string
	Role              string
	OnboardingState   string
	OAuthProvider     string
	HasOAuth          bool
	HasPassword       bool
	IsAdmin           bool
	IsHost            bool
	CreatedAt         time.Time
	EmailVerifiedAt   *time.Time
	EmailVerifiedText string
}

type playerDetailGame struct {
	GameID    string
	QuizID    int64
	QuizTitle string
	CreatedAt time.Time
}

type playerDetailAudit struct {
	ID               int64
	ActorPlayerID    int64
	ActorDisplayName string
	Action           string
	ActionLabel      string
	Detail           string
	CreatedAt        time.Time
}

// HandlePlayerDetail renders GET /admin/players/{playerID}. Reads from
// the AdminPlayerStore for the row + recent games + audit trail and
// from the PlayerLister for finish stats; mounts the action buttons
// gated by onboarding state.
func HandlePlayerDetail(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	store auth.AdminPlayerStore,
	flash *auth.SignedFlash,
) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/playerdetail.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		playerID, ok := handlers.ParseIDFromPath(w, r, logger, "playerID")
		if !ok {
			return
		}

		data, ok := loadPlayerDetail(w, r, logger, csrfMgr, store, playerID)
		if !ok {
			return
		}
		if flash != nil {
			if fr := flash.Read(w, r); fr.OK {
				data.Notice = fr.Notice
				data.Error = fr.Err
			}
		}
		render.Render(w, r, http.StatusOK, data)
	})
}

// loadPlayerDetail pulls every read-side dependency for the detail
// view. Split out of [HandlePlayerDetail] so the handler closure stays
// under revive's function-length cap; a 404 or 500 is rendered
// directly and ok=false short-circuits the caller.
func loadPlayerDetail(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	store auth.AdminPlayerStore,
	playerID int64,
) (playerDetailData, bool) {
	ctx := r.Context()
	detail, err := store.GetPlayerDetail(ctx, playerID)
	if err != nil {
		if errors.Is(err, auth.ErrPlayerNotFound) {
			render404(w, r, logger, csrfMgr)

			return playerDetailData{}, false
		}
		logger.ErrorContext(ctx, "error fetching player detail", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return playerDetailData{}, false
	}

	games, err := store.ListRecentFinishedGamesForPlayer(ctx, playerID, recentFinishedGamesLimit)
	if err != nil {
		logger.ErrorContext(ctx, "error listing recent finished games", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return playerDetailData{}, false
	}

	audit, err := store.ListAdminAuditForTarget(ctx, playerID, adminAuditLimit)
	if err != nil {
		logger.ErrorContext(ctx, "error listing admin audit", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return playerDetailData{}, false
	}

	return buildPlayerDetailData(detail, games, audit), true
}

// buildPlayerDetailData merges the row + games + audit into the
// template-facing shape. Action gating ("can verify", "can resend") is
// pre-derived here so the template stays declarative.
func buildPlayerDetailData(
	detail *auth.PlayerDetail,
	games []*auth.RecentFinishedGame,
	audit []*auth.AdminAuditEntry,
) playerDetailData {
	row := playerDetailRow{
		ID:              detail.ID,
		DisplayName:     detail.DisplayName,
		Email:           detail.Email,
		Role:            detail.Role,
		OnboardingState: detail.OnboardingState,
		OAuthProvider:   detail.OAuthProvider,
		HasOAuth:        detail.HasOAuth,
		HasPassword:     detail.HasPassword,
		IsAdmin:         detail.Role == auth.RoleAdmin,
		IsHost:          detail.Role == auth.RoleHost,
		CreatedAt:       detail.CreatedAt,
		EmailVerifiedAt: detail.EmailVerifiedAt,
	}
	if detail.EmailVerifiedAt != nil {
		row.EmailVerifiedText = detail.EmailVerifiedAt.Format(time.RFC3339)
	} else {
		row.EmailVerifiedText = "not yet"
	}

	gameRows := make([]playerDetailGame, 0, len(games))
	for _, g := range games {
		gameRows = append(gameRows, playerDetailGame{
			GameID: g.GameID, QuizID: g.QuizID, QuizTitle: g.QuizTitle, CreatedAt: g.CreatedAt,
		})
	}

	auditRows := make([]playerDetailAudit, 0, len(audit))
	for _, a := range audit {
		auditRows = append(auditRows, playerDetailAudit{
			ID:               a.ID,
			ActorPlayerID:    a.ActorPlayerID,
			ActorDisplayName: a.ActorDisplayName,
			Action:           a.Action,
			ActionLabel:      adminActionLabel(a.Action),
			Detail:           decodeAuditDetail(a.Action, a.Payload),
			CreatedAt:        a.CreatedAt,
		})
	}

	return playerDetailData{
		Title:          "Admin Dashboard - Player",
		Player:         row,
		RecentGames:    gameRows,
		AuditEntries:   auditRows,
		CanVerify:      detail.OnboardingState == auth.OnboardingStateUnverified,
		CanResend:      detail.OnboardingState == auth.OnboardingStateUnverified && detail.Email != "",
		EmailFormValue: detail.Email,
	}
}

// adminActionLabel is the user-facing label for one of the
// [auth.AdminAction*] constants. Kept here (not in the template) so a
// relabel touches one Go function instead of every template fork.
func adminActionLabel(action string) string {
	switch action {
	case auth.AdminActionVerify:
		return "Marked verified"
	case auth.AdminActionEmailSet:
		return "Email set"
	case auth.AdminActionDisplayNameSet:
		return "Display name set"
	case auth.AdminActionPasswordSet:
		return "Password set"
	case auth.AdminActionCreated:
		return "Created"
	case auth.AdminActionResendVerification:
		return "Verification email resent"
	case auth.AdminActionRoleChanged:
		return "Role changed"
	case auth.AdminActionPromoteSuper:
		return "Promoted to admin"
	case auth.AdminActionDemoteSuper:
		return "Admin removed"
	case auth.AdminActionPromoteAdmin:
		return "Promoted to host"
	case auth.AdminActionDemoteAdmin:
		return "Host removed"
	default:
		return action
	}
}

// decodeAuditDetail extracts the user-readable fragment of an audit
// row's payload. Only the actions whose payloads carry a
// human-meaningful field are decoded; the rest fall through to "".
// Decode failures (corrupt JSON, missing key) also fall through to ""
// rather than surfacing a parse error - the detail line is advisory.
func decodeAuditDetail(action, payload string) string {
	if payload == "" || payload == "{}" {
		return ""
	}
	var fields map[string]string
	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		return ""
	}
	switch action {
	case auth.AdminActionEmailSet, auth.AdminActionCreated:
		return fields["new_email"]
	case auth.AdminActionDisplayNameSet:
		return fields["new_displayName"]
	case auth.AdminActionRoleChanged,
		auth.AdminActionPromoteSuper, auth.AdminActionDemoteSuper,
		auth.AdminActionPromoteAdmin, auth.AdminActionDemoteAdmin:
		from, to := fields["from"], fields["to"]
		if from == "" || to == "" {
			return ""
		}

		return from + " -> " + to
	default:
		return ""
	}
}

// playerIDRedirectBase decimalises the player id for the redirect URL.
// 10 is the only base the path expects; the named constant satisfies
// revive's add-constant rule.
const playerIDRedirectBase = 10

// playerDetailRedirectURL returns the /admin/players/{id} path; used
// by every action handler's 303 so the browser lands back on the
// detail page after a PRG hop.
func playerDetailRedirectURL(playerID int64) string {
	return "/admin/players/" + strconv.FormatInt(playerID, playerIDRedirectBase)
}

// redirectToPlayerDetail issues a 303 to playerDetailRedirectURL. The
// helper exists so the single nolint:gosec marker covers every action
// handler that PRG-redirects back to a parsed-int64 path; gosec's
// open-redirect taint analysis treats [strconv.FormatInt] output as
// user-influenced even when the integer came from
// [handlers.ParseIDFromPath] (which rejects anything that isn't a
// clean int64).
func redirectToPlayerDetail(w http.ResponseWriter, r *http.Request, playerID int64) {
	//nolint:gosec // G710: target is /admin/players/<base-10-int64>; ParseIDFromPath rejects anything that isn't a clean int64.
	http.Redirect(w, r, playerDetailRedirectURL(playerID), http.StatusSeeOther)
}

// auditPayloadJSON serialises the payload map as JSON. Returns "{}"
// for nil/empty so the column never holds a NULL or an empty string.
// [encoding/json.Marshal] can fail only on a non-marshalable Go value;
// the callers pass map[string]string so any error here is a programmer
// mistake that the caller should log and recover from.
func auditPayloadJSON(payload map[string]string) (string, error) {
	if len(payload) == 0 {
		return "{}", nil
	}
	bs, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal audit payload: %w", err)
	}

	return string(bs), nil
}

// writeAudit records one admin action against the target player. Errors
// are logged but do not bubble up: an audit failure should not block
// the action itself from succeeding, since the mutation has already
// landed in the players row.
func writeAudit(
	ctx context.Context,
	logger *slog.Logger,
	store auth.AdminPlayerStore,
	actorID, targetID int64,
	action string,
	payload map[string]string,
) {
	js, err := auditPayloadJSON(payload)
	if err != nil {
		logger.ErrorContext(ctx, "error encoding audit payload",
			slog.String("action", action), slog.Any("err", err))

		return
	}
	if err := store.InsertAdminAudit(ctx, actorID, targetID, action, js); err != nil {
		logger.ErrorContext(ctx, "error writing admin audit",
			slog.String("action", action),
			slog.Int64("actor_id", actorID),
			slog.Int64("target_id", targetID),
			slog.Any("err", err))
	}
}
