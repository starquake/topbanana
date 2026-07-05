package host

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/quiz"
)

// quizCardData carries exactly the fields the shared quiz_card partial reads on
// its host path. It mirrors the admin QuizData shape for the card's body, but
// is defined here so the host surface does not import internal/admin.
type quizCardData struct {
	ID            int64
	Title         string
	Slug          string
	Description   string
	UpdatedAt     time.Time
	QuestionCount int
	RoundCount    int
	PlayCount     int64
	Mode          string
	ActionVariant string
	// HostHasRunningGame gates the card's action (#889): true swaps the plain
	// "Host this" form for a "Change quiz" button that opens the confirm-and-
	// restart modal, so a pick never silently no-ops over a running game (#851).
	HostHasRunningGame bool
}

// liveSessionView feeds the persistent "Session live" header indicator (#889):
// when the host already has an active room, the picker shows it so the host
// knows they are mid-session even while browsing for a quiz. QuizTitle is empty
// for a room opened with no quiz armed yet.
type liveSessionView struct {
	Active    bool
	JoinCode  string
	QuizTitle string
}

// hostPickerData feeds the host picker page: the header chrome (LiveSession,
// HostHasRunningGame) sits alongside the list content (Quizzes) rather than
// nested under a list-named struct, since the chrome is a sibling of the list.
type hostPickerData struct {
	Title              string
	LiveSession        liveSessionView
	HostHasRunningGame bool
	Quizzes            []quizCardData
}

// Picker handles GET /host/quizzes: the host picks a live quiz to run. The list
// is the runnable subset - live mode (ListLiveQuizzes) with at least one
// question - so the host never lands on a quiz that cannot start. Each card's
// only action posts quiz_id to /host, which arms the quiz in the host's room.
func (h *Handlers) Picker(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	player, ok := auth.PlayerFromContext(ctx)
	if !ok {
		h.logger.ErrorContext(ctx, "missing player on context for host quiz list")
		http.Error(w, msgInternalError, http.StatusInternalServerError)

		return
	}

	// An Admin sees every live quiz; a plain Host sees only their own (#1207).
	var (
		quizzes []*quiz.Quiz
		err     error
	)
	if player.IsAdmin() {
		quizzes, err = h.quizzes.ListLiveQuizzes(ctx)
	} else {
		quizzes, err = h.quizzes.ListLiveQuizzesForOwner(ctx, player.ID)
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "error listing live quizzes", slog.Any("err", err))
		http.Error(w, msgInternalError, http.StatusInternalServerError)

		return
	}

	counts, err := h.quizzes.QuestionCountsByQuiz(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "error loading question counts", slog.Any("err", err))
		http.Error(w, msgInternalError, http.StatusInternalServerError)

		return
	}

	roundCounts, err := h.quizzes.RoundCountsByQuiz(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "error loading round counts", slog.Any("err", err))
		http.Error(w, msgInternalError, http.StatusInternalServerError)

		return
	}

	running := h.hostHasRunningGame(ctx, player.ID)

	cards := make([]quizCardData, 0, len(quizzes))
	for _, qz := range quizzes {
		count := counts[qz.ID]
		if count == 0 {
			continue
		}
		cards = append(cards, quizCardData{
			ID:                 qz.ID,
			Title:              qz.Title,
			Slug:               qz.Slug,
			Description:        qz.Description,
			UpdatedAt:          qz.UpdatedAt,
			QuestionCount:      count,
			RoundCount:         roundCounts[qz.ID],
			PlayCount:          qz.PlayCount,
			Mode:               qz.Mode,
			ActionVariant:      "host",
			HostHasRunningGame: running,
		})
	}

	h.picker.Render(w, r, http.StatusOK, hostPickerData{
		Title:              "Host a quiz",
		LiveSession:        h.activeSessionView(ctx, player.ID, quizzes),
		HostHasRunningGame: running,
		Quizzes:            cards,
	})
}

// activeSessionView resolves the host's current room for the header indicator.
// A missing room is the common case (no indicator); a lookup error degrades to
// no indicator rather than failing the page, since the indicator is supplementary
// chrome. liveQuizzes is the already-loaded live set, so an armed quiz's title
// resolves without a second query (a hosted quiz is always live).
func (h *Handlers) activeSessionView(ctx context.Context, playerID int64, liveQuizzes []*quiz.Quiz) liveSessionView {
	sess, err := h.service.GetActiveSessionForHost(ctx, playerID)
	if err != nil {
		h.logger.ErrorContext(ctx, "error loading active host session for indicator", slog.Any("err", err))

		return liveSessionView{}
	}
	if sess == nil {
		return liveSessionView{}
	}

	view := liveSessionView{Active: true, JoinCode: sess.JoinCode}
	if sess.QuizID != nil {
		view.QuizTitle = quizTitleByID(liveQuizzes, *sess.QuizID)
	}

	return view
}

// hostHasRunningGame reports whether the host already has a game in flight, so
// the picker can swap each card's "Host this" form for the "Change quiz"
// confirm-and-restart prompt (#889). A lookup failure is logged and degraded to
// false rather than failing the page: the picker still serves, and the #851
// in-flight no-op still protects the running game server-side.
func (h *Handlers) hostHasRunningGame(ctx context.Context, playerID int64) bool {
	running, err := h.service.HostHasRunningGame(ctx, playerID)
	if err != nil {
		h.logger.ErrorContext(ctx, "error looking up running host game for picker", slog.Any("err", err))

		return false
	}

	return running
}

// quizTitleByID returns the title of the quiz with id in quizzes, or "" if it is
// not present.
func quizTitleByID(quizzes []*quiz.Quiz, id int64) string {
	for _, qz := range quizzes {
		if qz.ID == id {
			return qz.Title
		}
	}

	return ""
}
