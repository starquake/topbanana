package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
)

// settingsRedirectURL is the PRG target the settings form posts redirect
// back to.
const settingsRedirectURL = "/admin/settings"

// superAdminRow is one entry in the super-admin list rendered on the
// settings page. Mirrors auth.SuperAdminEntry; the schema carries no
// promoted-on timestamp (the row is a flat boolean) so the list shows
// username + email only.
type superAdminRow struct {
	ID       int64
	Username string
	Email    string
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
// list; the forms on the page post to the existing #319/#450 endpoints
// plus the promote-by-username handler below.
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
			rows = append(rows, superAdminRow{ID: e.ID, Username: e.Username, Email: e.Email})
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

// HandleSettingsPromoteSuper handles POST /admin/settings/promote (#320).
// Super-admin only (gated by RequireSuperAdmin at the route). The settings
// form is username-based, but the row-button promote endpoint is id-based;
// this handler resolves the typed username to a player, flips
// is_super_admin, and writes the same audit row as the id-based promote so
// the two paths leave an identical trail. CSRF-protected; on success or
// not-found it flashes and redirects back to /admin/settings.
func HandleSettingsPromoteSuper(
	logger *slog.Logger,
	store auth.SuperAdminStore,
	flash *auth.SignedFlash,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, ok := requireAdminActor(w, r)
		if !ok {
			return
		}

		username := strings.TrimSpace(r.PostFormValue("username"))
		if username == "" {
			flash.SetError(w, "Enter a username to promote.", 0)
			redirectToSettings(w, r)

			return
		}

		player, err := store.GetPlayerByUsername(r.Context(), username)
		if err != nil {
			if errors.Is(err, auth.ErrPlayerNotFound) {
				flash.SetError(w, "No player found with that username.", 0)
				redirectToSettings(w, r)

				return
			}
			logger.ErrorContext(r.Context(), "error looking up player to promote", slog.Any("err", err))
			flash.SetError(w, "Could not promote that player. Try again.", 0)
			redirectToSettings(w, r)

			return
		}

		if err := store.SetPlayerSuperAdmin(r.Context(), player.ID, true); err != nil {
			if errors.Is(err, auth.ErrPlayerNotFound) {
				flash.SetError(w, "No player found with that username.", 0)
				redirectToSettings(w, r)

				return
			}
			logger.ErrorContext(r.Context(), "error promoting player to super admin", slog.Any("err", err))
			flash.SetError(w, "Could not promote that player. Try again.", 0)
			redirectToSettings(w, r)

			return
		}

		writeAudit(r.Context(), logger, store, actor.ID, player.ID, auth.AdminActionPromoteSuper, nil)
		flash.SetNotice(w, "Player promoted to super admin.")
		redirectToSettings(w, r)
	})
}

// redirectToSettings issues a 303 back to the settings page after a form
// post (PRG). The target is a fixed string literal so there is no
// open-redirect surface to guard.
func redirectToSettings(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, settingsRedirectURL, http.StatusSeeOther)
}
