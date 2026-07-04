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

// Create handles POST /host - the host-a-session entry, with two paths split on
// quiz_id (#851):
//   - No quiz_id: open an empty staging room (the session-first / dashboard
//     "Host a session" entry), where the host picks the first live quiz ad-hoc
//     once players have joined. This path is not one-room-aware (the dashboard UI
//     already gates it).
//   - With a quiz_id ("Host live" from the quiz view): orchestrate through
//     [livesession.Service.StartHosting], which is one-room-per-host aware -
//     it opens a new armed room, arms+starts the quiz in the host's existing
//     empty/intermission room, or leaves a running game untouched.
//   - With a quiz_id and restart=true (the confirm-and-restart path #853 when a
//     game is already running): orchestrate through
//     [livesession.Service.RestartHosting], which ends the host's running session
//     and opens a fresh room hosting the picked quiz.
//
// Either way it 303-redirects the host to the big screen. The route is host-gated;
// a non-live or missing quiz round-trips back to the quiz list rather than
// opening a dead room.
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	player, ok := auth.PlayerFromContext(ctx)
	if !ok {
		h.logger.ErrorContext(ctx, "missing player on context for host create")
		http.Error(w, msgInternalError, http.StatusInternalServerError)

		return
	}

	// quiz_id is optional: an empty field opens an empty room, a present one
	// hosts that quiz via StartHosting. Only a present-but-malformed id is a 400.
	raw := r.FormValue("quiz_id")
	if raw == "" {
		h.createEmptyRoom(w, r, player.ID)

		return
	}

	id, err := handlers.IDFromString(raw)
	if err != nil {
		http.Error(w, "invalid quiz id", http.StatusBadRequest)

		return
	}
	if r.FormValue("restart") == "true" {
		h.hostLiveRestart(w, r, id, player.ID)

		return
	}
	h.hostLive(w, r, id, player.ID)
}

// createEmptyRoom opens an empty staging room (the no-quiz path) and redirects
// the host to it. Not one-room-aware on purpose: the dashboard UI gates the
// empty-room entry (#851).
func (h *Handlers) createEmptyRoom(w http.ResponseWriter, r *http.Request, playerID int64) {
	sess, err := h.service.CreateSession(r.Context(), nil, playerID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "error creating host session", slog.Any("err", err))
		http.Error(w, msgInternalError, http.StatusInternalServerError)

		return
	}
	h.redirectToLobby(w, r, sess.JoinCode)
}

// hostLive orchestrates the quiz-view "Host live" entry through StartHosting,
// which is one-room-per-host aware, then redirects the host to the resulting
// room. A missing or solo quiz bounces back to the quiz list (#851).
func (h *Handlers) hostLive(w http.ResponseWriter, r *http.Request, quizID, playerID int64) {
	sess, err := h.service.StartHosting(r.Context(), quizID, playerID)
	if err != nil {
		switch {
		case errors.Is(err, quiz.ErrQuizNotFound),
			errors.Is(err, livesession.ErrNotLiveQuiz),
			errors.Is(err, livesession.ErrQuizNotPublished):
			// A missing, solo, or unpublished-and-not-owned quiz is not hostable; bounce to the quiz list instead of a raw error.
			http.Redirect(w, r, "/admin/quizzes", http.StatusSeeOther)
		default:
			h.logger.ErrorContext(r.Context(), "error hosting live quiz", slog.Any("err", err))
			http.Error(w, msgInternalError, http.StatusInternalServerError)
		}

		return
	}
	h.redirectToLobby(w, r, sess.JoinCode)
}

// hostLiveRestart ends the host's running session and opens a new room hosting
// the picked quiz (#853): the confirm-and-restart path the host took to switch
// the live quiz. A missing or solo quiz bounces back to the quiz list and
// nothing is ended.
func (h *Handlers) hostLiveRestart(w http.ResponseWriter, r *http.Request, quizID, playerID int64) {
	sess, err := h.service.RestartHosting(r.Context(), quizID, playerID)
	if err != nil {
		switch {
		case errors.Is(err, quiz.ErrQuizNotFound),
			errors.Is(err, livesession.ErrNotLiveQuiz),
			errors.Is(err, livesession.ErrQuizNotPublished):
			http.Redirect(w, r, "/admin/quizzes", http.StatusSeeOther)
		default:
			h.logger.ErrorContext(r.Context(), "error restarting host session", slog.Any("err", err))
			http.Error(w, msgInternalError, http.StatusInternalServerError)
		}

		return
	}
	h.redirectToLobby(w, r, sess.JoinCode)
}

// redirectToLobby 303-redirects the host to the big screen for the given code.
// The code is server-minted over a fixed ambiguity-free alphabet, never request
// input, so the redirect is same-origin.
func (*Handlers) redirectToLobby(w http.ResponseWriter, r *http.Request, code string) {
	dest := hostScreenPathPrefix + code
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
		dest := hostScreenPathPrefix + code
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
		errors.Is(err, livesession.ErrNotLiveQuiz),
		errors.Is(err, livesession.ErrQuizNotPublished):
		// code is the server-minted path value, never request input, so the
		// redirect back to the lobby is same-origin.
		dest := hostScreenPathPrefix + code
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
		dest := hostScreenPathPrefix + code
		http.Redirect(w, r, dest, http.StatusSeeOther) //nolint:gosec // code is server-generated, not user input.
	case errors.Is(err, livesession.ErrSessionNotFound), errors.Is(err, livesession.ErrNotHost):
		http.NotFound(w, r)
	default:
		h.logger.ErrorContext(ctx, "error ending host session", slog.Any("err", err))
		http.Error(w, msgInternalError, http.StatusInternalServerError)
	}
}
