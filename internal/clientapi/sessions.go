package clientapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/quiz"
)

// HandleSessionCreate opens a hosted live session for a quiz. Host-authed:
// the caller must hold host/admin rights (a signed-in Player gets 403),
// and the quiz must exist and be mode='live' (MP-0 / #677). Returns 201
// with the join code on success.
//
// A non-existent quiz and a solo quiz both map to 404 so the endpoint does
// not betray which quizzes exist or their mode to a host probing ids - it
// stays a "no hostable quiz here" answer either way.
func HandleSessionCreate(logger *slog.Logger, service *livesession.Service) http.Handler {
	type createRequest struct {
		QuizID int64 `json:"quizId"`
	}
	type createResponse struct {
		JoinCode string `json:"joinCode"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		player, ok := auth.PlayerFromContext(ctx)
		if !ok {
			logger.ErrorContext(ctx, "missing player on context for session create")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}
		// Host gate (decision 4): only host/admin can open a session. A
		// signed-in Player gets a 403 - the create endpoint's existence is
		// not secret, unlike the admin surface.
		if !player.CanHost() {
			http.Error(w, "forbidden", http.StatusForbidden)

			return
		}

		req, err := handlers.DecodeJSON[createRequest](w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		sess, err := service.CreateSession(ctx, req.QuizID, player.ID)
		if err != nil {
			switch {
			case errors.Is(err, quiz.ErrQuizNotFound), errors.Is(err, livesession.ErrNotLiveQuiz):
				http.NotFound(w, r)
			default:
				writeInternalError(w, r, logger, "error creating session", err)
			}

			return
		}

		if err = handlers.EncodeJSON(w, http.StatusCreated, createResponse{JoinCode: sess.JoinCode}); err != nil {
			logger.ErrorContext(ctx, "error encoding session create response", slog.Any("err", err))
		}
	})
}

// HandleSessionJoin adds the calling player to a session anonymously under
// a display name carried in the body. A per-session display-name collision
// is resolved transparently by the service (petname fallback), so the
// response always carries the display name the player actually landed
// with. Returns 404 when the join code is unknown.
func HandleSessionJoin(logger *slog.Logger, service *livesession.Service) http.Handler {
	type joinRequest struct {
		DisplayName string `json:"displayName"`
	}
	type joinResponse struct {
		DisplayName string `json:"displayName"`
		IsReady     bool   `json:"isReady"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		player, ok := auth.PlayerFromContext(ctx)
		if !ok {
			logger.ErrorContext(ctx, "missing player on context for session join")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		req, err := handlers.DecodeJSON[joinRequest](w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}
		displayName := strings.TrimSpace(req.DisplayName)
		if displayName == "" {
			http.Error(w, "display name is required", http.StatusBadRequest)

			return
		}

		joined, err := service.Join(ctx, r.PathValue("code"), player.ID, displayName, auth.GeneratePetname)
		if err != nil {
			if errors.Is(err, livesession.ErrSessionNotFound) {
				http.NotFound(w, r)

				return
			}
			writeInternalError(w, r, logger, "error joining session", err)

			return
		}

		res := joinResponse{DisplayName: joined.DisplayName, IsReady: joined.IsReady}
		if err = handlers.EncodeJSON(w, http.StatusOK, res); err != nil {
			logger.ErrorContext(ctx, "error encoding session join response", slog.Any("err", err))
		}
	})
}

// HandleSessionReady sets the calling participant's ready flag. The body
// carries the desired state so the same endpoint marks ready and un-ready.
// Returns 404 for an unknown code or a non-participant (the code stays
// opaque to outsiders, mirroring the game participant gate, #272).
func HandleSessionReady(logger *slog.Logger, service *livesession.Service) http.Handler {
	type readyRequest struct {
		Ready bool `json:"ready"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		player, ok := auth.PlayerFromContext(ctx)
		if !ok {
			logger.ErrorContext(ctx, "missing player on context for session ready")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		req, err := handlers.DecodeJSON[readyRequest](w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		if err = service.SetReady(ctx, r.PathValue("code"), player.ID, req.Ready); err != nil {
			if errors.Is(err, livesession.ErrSessionNotFound) || errors.Is(err, livesession.ErrNotParticipant) {
				http.NotFound(w, r)

				return
			}
			writeInternalError(w, r, logger, "error setting session ready", err)

			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}

// sessionPlayerResponse is one roster row in the lobby state. playerId is
// the underlying players.id so a surface can correlate the host (hostId
// below) and highlight the viewer's own row; displayName + isReady drive
// the lobby list.
type sessionPlayerResponse struct {
	PlayerID    int64  `json:"playerId"`
	DisplayName string `json:"displayName"`
	IsReady     bool   `json:"isReady"`
}

// sessionQuizResponse is the quiz metadata the lobby renders. Deliberately
// minimal - title + question count - so the lobby never leaks question or
// option text before the game starts (the no-spoiler guarantee).
type sessionQuizResponse struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	QuestionCount int    `json:"questionCount"`
}

// sessionStateResponse is the FROZEN wire contract for
// GET /api/sessions/{code}/state (MP-1 / #678). Later phases (MP-2..MP-5)
// build their surfaces on this shape, so fields are added, never renamed
// or removed:
//
//   - joinCode: the room code, echoed so a surface can render it from the
//     state alone.
//   - phase: the server-authoritative phase; "lobby" in MP-1.
//   - hostId: players.id of the host, so a surface can mark the host row
//     and gate host-only controls.
//   - players: the lobby roster in join order, each with playerId +
//     displayName + isReady.
//   - quiz: minimal quiz metadata (id, title, questionCount); no question
//     or option text.
//   - serverNow: the server clock at response time, so later phases can
//     drive client-local countdowns off server timestamps (the same
//     technique the solo client uses) without depending on the client's
//     wall clock.
type sessionStateResponse struct {
	JoinCode  string                  `json:"joinCode"`
	Phase     string                  `json:"phase"`
	HostID    int64                   `json:"hostId"`
	Players   []sessionPlayerResponse `json:"players"`
	Quiz      sessionQuizResponse     `json:"quiz"`
	ServerNow time.Time               `json:"serverNow"`
}

// HandleSessionState returns the authoritative lobby state. Participant-
// gated: only a roster player or the host may read it, so a stranger with
// the code cannot enumerate the room. Returns 404 for an unknown code or a
// non-participant.
func HandleSessionState(logger *slog.Logger, service *livesession.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		player, ok := auth.PlayerFromContext(ctx)
		if !ok {
			logger.ErrorContext(ctx, "missing player on context for session state")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		state, err := service.GetLobbyState(ctx, r.PathValue("code"), player.ID)
		if err != nil {
			if errors.Is(err, livesession.ErrSessionNotFound) || errors.Is(err, livesession.ErrNotParticipant) {
				http.NotFound(w, r)

				return
			}
			writeInternalError(w, r, logger, "error retrieving session state", err)

			return
		}

		if err = handlers.EncodeJSON(w, http.StatusOK, newSessionStateResponse(state)); err != nil {
			logger.ErrorContext(ctx, "error encoding session state response", slog.Any("err", err))
		}
	})
}

// newSessionStateResponse projects the domain lobby state onto the frozen
// wire shape.
func newSessionStateResponse(state *livesession.LobbyState) sessionStateResponse {
	players := make([]sessionPlayerResponse, 0, len(state.Session.Players))
	for _, p := range state.Session.Players {
		players = append(players, sessionPlayerResponse{
			PlayerID:    p.PlayerID,
			DisplayName: p.DisplayName,
			IsReady:     p.IsReady,
		})
	}

	return sessionStateResponse{
		JoinCode: state.Session.JoinCode,
		Phase:    string(state.Session.Phase),
		HostID:   state.Session.HostPlayerID,
		Players:  players,
		Quiz: sessionQuizResponse{
			ID:            state.Quiz.ID,
			Title:         state.Quiz.Title,
			QuestionCount: len(state.Quiz.Questions),
		},
		ServerNow: time.Now().UTC(),
	}
}

// sessionEventResponse is the wire shape of one SSE tick on the session
// event channel (MP-2 / #679). It deliberately carries NO game data - only
// a monotonic version and the phase. A tick (or a reconnect, which resends
// the current version) means "session state moved; re-GET
// /api/sessions/{code}/state". The full authoritative read is the state
// endpoint; this channel is a pure side-channel that never carries roster,
// quiz, or player data, so it cannot drift out of sync with the DTO.
type sessionEventResponse struct {
	Version uint64 `json:"version"`
	Phase   string `json:"phase"`
}

// sessionEventHeartbeatInterval is how often the session SSE handler emits
// a no-op comment frame to keep the connection alive when the session is
// quiet. Same value and rationale as the leaderboard stream
// (leaderboardHeartbeatInterval): 25s lands inside common proxy / NAT /
// mobile-carrier idle timeouts without the keep-alive cost of a faster
// tick.
const sessionEventHeartbeatInterval = 25 * time.Second

// sessionEventStreamer bundles the per-request dependencies of the session
// SSE stream. Mirrors leaderboardStreamer so the two share the same flush /
// heartbeat / write-deadline handling.
type sessionEventStreamer struct {
	w      http.ResponseWriter
	rc     *http.ResponseController
	logger *slog.Logger
}

// writeTick writes one tick as a single SSE `data:` frame and flushes.
// Returns false on any write/flush/encode failure (client disconnected,
// broken pipe) so the caller can exit the stream loop cleanly.
func (s *sessionEventStreamer) writeTick(ctx context.Context, tick livesession.Tick) bool {
	payload, err := json.Marshal(sessionEventResponse{Version: tick.Version, Phase: string(tick.Phase)})
	if err != nil {
		s.logger.ErrorContext(ctx, "error marshalling session event", slog.Any("err", err))

		return false
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", payload); err != nil {
		return false
	}
	if err := s.rc.Flush(); err != nil {
		return false
	}

	return true
}

// writeHeartbeat writes a single SSE comment frame (`:\n\n`) and flushes.
// EventSource ignores comment lines, so this never fires a client-side
// onmessage; its only job is keeping the connection warm so an idle stream
// is not torn down by an intermediate proxy. Returns false on write/flush
// failure so the caller can exit cleanly.
func (s *sessionEventStreamer) writeHeartbeat() bool {
	if _, err := fmt.Fprint(s.w, ":\n\n"); err != nil {
		return false
	}
	if err := s.rc.Flush(); err != nil {
		return false
	}

	return true
}

// run drains the hub channel and writes one SSE frame per tick until the
// client disconnects or the channel closes. The heartbeat ticker emits a
// no-op comment frame every sessionEventHeartbeatInterval to keep an idle
// connection warm.
func (s *sessionEventStreamer) run(ctx context.Context, events <-chan livesession.Tick) {
	heartbeat := time.NewTicker(sessionEventHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case tick, ok := <-events:
			if !ok {
				return
			}
			if !s.writeTick(ctx, tick) {
				return
			}
		case <-heartbeat.C:
			if !s.writeHeartbeat() {
				return
			}
		}
	}
}

// HandleSessionEvents streams session ticks over SSE (MP-2 / #679).
// Participant-gated exactly like GET /state: only the host or a roster
// player may subscribe, so a stranger with the code gets a 404 (the
// subscription must not leak that the session exists). The stream carries
// no game data - each tick is {version, phase}, a signal to re-GET
// /api/sessions/{code}/state. A reconnect re-runs the gate and resends the
// current version, which doubles as the resync path.
func HandleSessionEvents(logger *slog.Logger, service *livesession.Service, hub *livesession.Hub) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		player, ok := auth.PlayerFromContext(ctx)
		if !ok {
			logger.ErrorContext(ctx, "missing player on context for session events")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		// Gate BEFORE any header write so a non-participant / unknown code
		// can still be surfaced as a proper HTTP 404 rather than a half-open
		// text/event-stream.
		code, phase, err := service.AuthorizeView(ctx, r.PathValue("code"), player.ID)
		if err != nil {
			if errors.Is(err, livesession.ErrSessionNotFound) || errors.Is(err, livesession.ErrNotParticipant) {
				http.NotFound(w, r)

				return
			}
			writeInternalError(w, r, logger, "error authorizing session events", err)

			return
		}

		// Subscribe under the canonical code; the returned version seeds the
		// initial frame so a fresh subscriber learns where it stands without
		// racing a publish.
		events, version, unsubscribe := hub.Subscribe(code)
		defer unsubscribe()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		rc := http.NewResponseController(w)
		// Clear the per-request write deadline so the HTTP server's
		// WriteTimeout does not kill this long-lived response on the first
		// write past the deadline (same fix as the leaderboard stream).
		if derr := rc.SetWriteDeadline(time.Time{}); derr != nil {
			logger.WarnContext(ctx, "could not clear SSE write deadline", slog.Any("err", derr))
		}

		streamer := &sessionEventStreamer{w: w, rc: rc, logger: logger}

		if !streamer.writeTick(ctx, livesession.Tick{Version: version, Phase: phase}) {
			return
		}

		streamer.run(ctx, events)
	})
}
