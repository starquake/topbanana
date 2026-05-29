package admin

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
)

// superAdminRow is one entry in the super-admin list rendered on the
// settings page. Mirrors auth.SuperAdminEntry. PromotedAt is nil for rows
// promoted before the super_admin_since column existed; the template
// renders an em dash in that case.
type superAdminRow struct {
	ID         int64
	Username   string
	Email      string
	PromotedAt *time.Time
}

// settingsPageData backs settings.gohtml.
type settingsPageData struct {
	Title       string
	SuperAdmins []superAdminRow
	Notice      string
	Error       string
}

// HandleSettings renders GET /admin/settings (#320), the super-admin-only
// console that consolidates the #319 affordances. The route is gated by
// RequireSuperAdmin so a signed-in non-super-admin gets a 404 (the page's
// existence stays hidden) rather than a 403. Pre-loads the super-admin
// list; role changes are made from a player's detail page via the id-based
// role endpoint (#527), and the demote buttons on this page post there too.
func HandleSettings(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	store auth.SuperAdminStore,
	flash *auth.SignedFlash,
) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/settings.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entries, err := store.ListSuperAdmins(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "error listing super admins", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		rows := make([]superAdminRow, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, superAdminRow{
				ID:         e.ID,
				Username:   e.Username,
				Email:      e.Email,
				PromotedAt: e.PromotedAt,
			})
		}

		data := settingsPageData{
			Title:       "Admin Dashboard - Settings",
			SuperAdmins: rows,
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
