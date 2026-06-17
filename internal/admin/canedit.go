package admin

import (
	"net/http"

	"github.com/starquake/topbanana/internal/auth"
)

// canEditQuiz is the single source of truth for the creator-or-Admin edit rule
// (#281/#538): the session player must be present and must either be the quiz's
// creator OR an Admin (who may edit, delete, and reset scores on any quiz). A
// Host is NOT granted rights over another Host's games - own-game checks still
// go through createdByPlayerID. Both [attachCanEdit] (read paths) and
// [requireQuizOwner] (mutating paths) call this so the policy lives in one
// place.
func canEditQuiz(r *http.Request, createdByPlayerID int64) bool {
	p, ok := auth.PlayerFromContext(r.Context())
	if !ok {
		return false
	}

	return p.IsAdmin() || p.ID == createdByPlayerID
}
