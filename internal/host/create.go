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

// Create handles POST /host - the host-a-session entry. With no quiz_id it opens
// an empty room (the "no game running yet" staging state where the host picks the
// first live quiz ad-hoc); with a quiz_id it opens a room with that quiz
// preselected (the "Play live" entry from the quiz admin page). Either way it
// 303-redirects the host straight to the TV lobby. The route is host-gated; when
// a quiz is supplied the service re-checks it exists and is mode='live', so a
// non-live or missing quiz round-trips back to the quiz list rather than opening
// a dead lobby. The host begins the first game from the lobby via the picker.
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	player, ok := auth.PlayerFromContext(ctx)
	if !ok {
		h.logger.ErrorContext(ctx, "missing player on context for host create")
		http.Error(w, msgInternalError, http.StatusInternalServerError)

		return
	}

	// quiz_id is optional: an empty field opens an empty room, a present one
	// preselects the first quiz. Only a present-but-malformed id is a 400.
	var quizID *int64
	if raw := r.FormValue("quiz_id"); raw != "" {
		id, idErr := handlers.IDFromString(raw)
		if idErr != nil {
			http.Error(w, "invalid quiz id", http.StatusBadRequest)

			return
		}
		quizID = &id
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
	dest := hostLobbyPathPrefix + sess.JoinCode
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
		dest := hostLobbyPathPrefix + code
		http.Redirect(w, r, dest, http.StatusSeeOther) //nolint:gosec // code is server-generated, not user input.
	case errors.Is(err, livesession.ErrSessionNotFound), errors.Is(err, livesession.ErrNotHost):
		http.NotFound(w, r)
	default:
		h.logger.ErrorContext(ctx, "error starting host session", slog.Any("err", err))
		http.Error(w, msgInternalError, http.StatusInternalServerError)
	}
}

// NextQuiz handles POST /host/{code}/next-quiz - the host control that picks a
// quiz to play in the room and starts it (#836). It is used both for the first
// game from an empty lobby and for the next game from the between-games
// intermission, calling the unified [livesession.Service.StartQuiz]. It reads
// the posted quiz_id, arms the room onto that live quiz and begins it, then
// 303-redirects the host back to the lobby the runner drives into play. A missing
// or non-live quiz bounces back to the lobby rather than surfacing a raw error; a
// room with a game already in flight (the host double-posted, or a game is
// running) is treated the same so a stale tab is harmless. A foreign or unknown
// code both 404 so the code stays opaque to a host who does not own it.
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

	err = h.service.StartQuiz(ctx, code, player.ID, quizID)
	switch {
	case err == nil,
		errors.Is(err, livesession.ErrGameInFlight),
		errors.Is(err, quiz.ErrQuizNotFound),
		errors.Is(err, livesession.ErrNotLiveQuiz):
		// code is the server-minted path value, never request input, so the
		// redirect back to the lobby is same-origin.
		dest := hostLobbyPathPrefix + code
		http.Redirect(w, r, dest, http.StatusSeeOther) //nolint:gosec // code is server-generated, not user input.
	case errors.Is(err, livesession.ErrSessionNotFound), errors.Is(err, livesession.ErrNotHost):
		http.NotFound(w, r)
	default:
		h.logger.ErrorContext(ctx, "error starting next quiz", slog.Any("err", err))
		http.Error(w, msgInternalError, http.StatusInternalServerError)
	}
}

// End handles POST /host/{code}/end - the host control that closes the room for
// good (#836). It asks the service to terminally finish the room, then
// 303-redirects the host back to the (now finished) lobby. An already-finished
// room is an idempotent success so a double-post or stale tab is harmless. A
// foreign or unknown code both 404 so the code stays opaque to a host who does
// not own it.
func (h *Handlers) End(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	player, ok := auth.PlayerFromContext(ctx)
	if !ok {
		h.logger.ErrorContext(ctx, "missing player on context for host end")
		http.Error(w, msgInternalError, http.StatusInternalServerError)

		return
	}

	code := r.PathValue("code")
	err := h.service.EndSession(ctx, code, player.ID)
	switch {
	case err == nil:
		// code is the server-minted path value, never request input, so the
		// redirect back to the lobby is same-origin.
		dest := hostLobbyPathPrefix + code
		http.Redirect(w, r, dest, http.StatusSeeOther) //nolint:gosec // code is server-generated, not user input.
	case errors.Is(err, livesession.ErrSessionNotFound), errors.Is(err, livesession.ErrNotHost):
		http.NotFound(w, r)
	default:
		h.logger.ErrorContext(ctx, "error ending host session", slog.Any("err", err))
		http.Error(w, msgInternalError, http.StatusInternalServerError)
	}
}
