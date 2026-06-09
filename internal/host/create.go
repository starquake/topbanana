package host

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/quiz"
)

// Create handles POST /host - the "Play live" entry from the quiz admin
// page. It opens a hosted session for the posted quiz and 303-redirects the
// host straight to the TV lobby. The route is host-gated; the service
// re-checks that the quiz exists and is mode='live', so a non-live or
// missing quiz round-trips back to the quiz list rather than opening a dead
// lobby.
//
// This is the only session-creating path the host surface wires today; the
// host begins the game from the lobby via [Handlers.Start].
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	player, ok := auth.PlayerFromContext(ctx)
	if !ok {
		h.logger.ErrorContext(ctx, "missing player on context for host create")
		http.Error(w, msgInternalError, http.StatusInternalServerError)

		return
	}

	quizID, err := handlers.IDFromString(r.FormValue("quiz_id"))
	if err != nil {
		http.Error(w, "invalid quiz id", http.StatusBadRequest)

		return
	}

	sess, err := h.service.CreateSession(ctx, quizID, player.ID)
	if err != nil {
		switch {
		case errors.Is(err, quiz.ErrQuizNotFound), errors.Is(err, livesession.ErrNotLiveQuiz):
			// A missing or solo quiz is not hostable; bounce back to the
			// quiz list rather than surfacing a raw error.
			http.Redirect(w, r, "/admin/quizzes", http.StatusSeeOther)
		default:
			h.logger.ErrorContext(ctx, "error creating host session", slog.Any("err", err))
			http.Error(w, msgInternalError, http.StatusInternalServerError)
		}

		return
	}

	// sess.JoinCode is server-minted over a fixed ambiguity-free alphabet,
	// not request input, so this is a same-origin redirect to the new lobby.
	dest := "/host/" + sess.JoinCode
	http.Redirect(w, r, dest, http.StatusSeeOther) //nolint:gosec // code is server-generated, not user input.
}

// Start handles POST /host/{code}/start - the host control that begins the
// game. It calls the shared live-session service, which marks the session
// started and hands it to the runner that drives the lobby -> in-game
// transition (MP-5 / #682). An already-started session is treated as success
// so a double click or a stale tab is idempotent, then the host is
// 303-redirected back to the lobby, which the runner advances into play. A
// foreign or unknown code both 404 so the code stays opaque to a host who
// does not own it.
func (h *Handlers) Start(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	player, ok := auth.PlayerFromContext(ctx)
	if !ok {
		h.logger.ErrorContext(ctx, "missing player on context for host start")
		http.Error(w, msgInternalError, http.StatusInternalServerError)

		return
	}

	code := r.PathValue("code")
	err := h.service.Start(ctx, code, player.ID)
	switch {
	case err == nil, errors.Is(err, livesession.ErrSessionAlreadyStarted):
		// code is the server-minted path value, never request input, so the
		// redirect back to the lobby is same-origin.
		dest := "/host/" + code
		http.Redirect(w, r, dest, http.StatusSeeOther) //nolint:gosec // code is server-generated, not user input.
	case errors.Is(err, livesession.ErrSessionNotFound), errors.Is(err, livesession.ErrNotHost):
		http.NotFound(w, r)
	default:
		h.logger.ErrorContext(ctx, "error starting host session", slog.Any("err", err))
		http.Error(w, msgInternalError, http.StatusInternalServerError)
	}
}

// NextQuiz handles POST /host/{code}/next-quiz - the host control that arms the
// room's next game from the between-games intermission (#836). It reads the
// posted quiz_id and asks the service to re-arm the room onto that live quiz and
// begin it, then 303-redirects the host back to the lobby the runner drives into
// play. A missing or non-live quiz bounces back to the lobby rather than
// surfacing a raw error; a room not in intermission (the host double-posted or
// a game is already in flight) is treated the same so a stale tab is harmless.
// A foreign or unknown code both 404 so the code stays opaque to a host who does
// not own it.
func (h *Handlers) NextQuiz(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	player, ok := auth.PlayerFromContext(ctx)
	if !ok {
		h.logger.ErrorContext(ctx, "missing player on context for host next quiz")
		http.Error(w, msgInternalError, http.StatusInternalServerError)

		return
	}

	code := r.PathValue("code")
	quizID, err := handlers.IDFromString(r.FormValue("quiz_id"))
	if err != nil {
		http.Error(w, "invalid quiz id", http.StatusBadRequest)

		return
	}

	err = h.service.StartNextQuiz(ctx, code, player.ID, quizID)
	switch {
	case err == nil,
		errors.Is(err, livesession.ErrNotIntermission),
		errors.Is(err, quiz.ErrQuizNotFound),
		errors.Is(err, livesession.ErrNotLiveQuiz):
		// code is the server-minted path value, never request input, so the
		// redirect back to the lobby is same-origin.
		dest := "/host/" + code
		http.Redirect(w, r, dest, http.StatusSeeOther) //nolint:gosec // code is server-generated, not user input.
	case errors.Is(err, livesession.ErrSessionNotFound), errors.Is(err, livesession.ErrNotHost):
		http.NotFound(w, r)
	default:
		h.logger.ErrorContext(ctx, "error starting next quiz", slog.Any("err", err))
		http.Error(w, msgInternalError, http.StatusInternalServerError)
	}
}
