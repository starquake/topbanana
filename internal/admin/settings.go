package admin

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
)

// adminRow is one entry in the Admins list rendered on the settings page.
// Mirrors auth.AdminEntry. PromotedAt is nil for rows whose role predates the
// role_changed_at column; the template renders an em dash in that case.
type adminRow struct {
	ID          int64
	DisplayName string
	Email       string
	PromotedAt  *time.Time
}

// settingsPageData backs settings.gohtml. Admins carries the current top-tier
// Admins listed on the settings page.
type settingsPageData struct {
	Title  string
	Admins []adminRow
	Notice string
	Error  string
}

// HandleSettings renders GET /admin/settings (#320/#538), the Admin-only
// console. The route is gated by RequireAdmin so a signed-in non-Admin (Player
// or Host) gets a 404 (the page's existence stays hidden) rather than a 403.
// Pre-loads the Admins list; role changes are made from a player's detail page
// via the id-based role endpoint (#538), and the demote buttons on this page
// post there too.
func HandleSettings(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	store auth.AdminListStore,
	flash *auth.SignedFlash,
) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/settings.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entries, err := store.ListAdmins(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "error listing admins", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		rows := make([]adminRow, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, adminRow{
				ID:          e.ID,
				DisplayName: e.DisplayName,
				Email:       e.Email,
				PromotedAt:  e.RoleChangedAt,
			})
		}

		data := settingsPageData{
			Title:  "Admin Dashboard - Settings",
			Admins: rows,
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
