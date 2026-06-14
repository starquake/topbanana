package clientapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/quiz"
)

// HandleSessionCreate opens a hosted room. Host-authed: the caller must hold
// host/admin rights (a signed-in Player gets 403). quizId is optional (#836): an
// omitted or null quizId opens an empty room (the "no game running yet" staging
// state where the host picks the first quiz ad-hoc), and a present quizId
// preselects that first quiz, which must exist and be mode='live' (MP-0 / #677).
// Returns 201 with the join code on success.
//
// A non-existent quiz and a solo quiz both map to 404 so the endpoint does
// not betray which quizzes exist or their mode to a host probing ids - it
// stays a "no hostable quiz here" answer either way.
func HandleSessionCreate(service *livesession.Service) http.Handler {
	type createRequest struct {
		QuizID *int64 `json:"quizId"`
	}
	type createResponse struct {
		JoinCode string `json:"joinCode"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := handlers.LoggerFromContext(ctx)

		player, ok := auth.PlayerFromContext(ctx)
		if !ok {
			logger.ErrorContext(ctx, "missing player on context for session create")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}
		logger = logger.With(slog.Int64("player", player.ID))
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

// HandleSessionJoin adds the calling player to a session. The join carries no
// name (#716): the player is already named on their players row (an anonymous
// or unnamed player claims players.display_name through the shared claim flow
// before joining; a logged-in named player keeps their account name), so the
// response echoes that current name straight off the context player. Returns
// 404 when the join code is unknown and 409 when the room is closed - a
// terminally finished room rejects joins, but a latecomer may join a live game
// at any phase (#836).
func HandleSessionJoin(service *livesession.Service) http.Handler {
	type joinResponse struct {
		DisplayName string `json:"displayName"`
		IsReady     bool   `json:"isReady"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := handlers.LoggerFromContext(ctx)

		player, ok := auth.PlayerFromContext(ctx)
		if !ok {
			logger.ErrorContext(ctx, "missing player on context for session join")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}
		logger = logger.With(slog.Int64("player", player.ID))

		joined, err := service.Join(ctx, r.PathValue("code"), player.ID)
		if err != nil {
			switch {
			case errors.Is(err, livesession.ErrSessionNotFound):
				http.NotFound(w, r)
			case errors.Is(err, livesession.ErrLobbyClosed):
				http.Error(w, "this room is closed", http.StatusConflict)
			default:
				writeInternalError(w, r, logger, "error joining session", err)
			}

			return
		}

		res := joinResponse{DisplayName: player.DisplayName, IsReady: joined.IsReady}
		if err = handlers.EncodeJSON(w, http.StatusOK, res); err != nil {
			logger.ErrorContext(ctx, "error encoding session join response", slog.Any("err", err))
		}
	})
}

// HandleSessionReady sets the calling participant's ready flag. The body
// carries the desired state so the same endpoint marks ready and un-ready.
// Returns 404 for an unknown code or a non-participant (the code stays
// opaque to outsiders, mirroring the game participant gate, #272).
func HandleSessionReady(service *livesession.Service) http.Handler {
	type readyRequest struct {
		Ready bool `json:"ready"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := handlers.LoggerFromContext(ctx)

		player, ok := auth.PlayerFromContext(ctx)
		if !ok {
			logger.ErrorContext(ctx, "missing player on context for session ready")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}
		logger = logger.With(slog.Int64("player", player.ID))

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

// hostSessionAction wraps a host-gated, no-body session mutation (start now,
// arm, cancel) into an [http.Handler]. The three controls share the same shape:
// resolve the context player, run action, then map the result - nil or the
// supplied idempotent sentinel to 204, ErrSessionNotFound to 404, ErrNotHost to
// 403, anything else to a logged 500. action is the only thing that differs per
// control; idempotent is the per-control "already in that state" sentinel (an
// already-started game for start, an already-left lobby for arm/cancel) that
// maps to a 204 no-op; what names the action in the log messages.
func hostSessionAction(
	what string,
	idempotent error,
	action func(ctx context.Context, code string, playerID int64) error,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := handlers.LoggerFromContext(ctx)

		player, ok := auth.PlayerFromContext(ctx)
		if !ok {
			logger.ErrorContext(ctx, "missing player on context for session "+what)
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}
		logger = logger.With(slog.Int64("player", player.ID))

		err := action(ctx, r.PathValue("code"), player.ID)
		switch {
		case err == nil, errors.Is(err, idempotent):
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, livesession.ErrSessionNotFound):
			http.NotFound(w, r)
		case errors.Is(err, livesession.ErrNotHost):
			http.Error(w, "forbidden", http.StatusForbidden)
		default:
			writeInternalError(w, r, logger, "error on session "+what, err)
		}
	})
}

// HandleSessionStart is the host "Start now" control: it begins the game
// immediately, skipping any armed last-call countdown. Only the host may call
// it. Returns 204 on success, 403 when the caller is not the host, 404 for an
// unknown code, and 204 (idempotent no-op) when the session has already
// started.
func HandleSessionStart(service *livesession.Service) http.Handler {
	return hostSessionAction("start", livesession.ErrSessionAlreadyStarted, service.Start)
}

// HandleSessionArmStart arms the host's last-call countdown (the "Start in 60s"
// control): it stamps the absolute start deadline that every surface renders.
// Only the host may call it. Returns 204 on success, 403 when the caller is not
// the host, 404 for an unknown code, and 204 (idempotent no-op) when the
// session has already left the lobby.
func HandleSessionArmStart(service *livesession.Service) http.Handler {
	return hostSessionAction("arm-start", livesession.ErrNotInLobby,
		func(ctx context.Context, code string, playerID int64) error {
			return service.ArmStart(ctx, code, playerID, time.Now().UTC())
		})
}

// HandleSessionCancelStart cancels an armed last-call countdown (the host
// "Cancel" control), clearing the start deadline. Only the host may call it.
// Returns 204 on success, 403 when the caller is not the host, 404 for an
// unknown code, and 204 (idempotent no-op) when the session has already left
// the lobby.
func HandleSessionCancelStart(service *livesession.Service) http.Handler {
	return hostSessionAction("cancel-start", livesession.ErrNotInLobby, service.CancelStart)
}

// HandleSessionAnswer records the calling participant's pick for the session's
// current question. The answer is timestamped on the server (the request body
// carries only the chosen option) so scoring uses the server clock. Returns
// 204 on success, 404 for an unknown code or a non-participant (the code stays
// opaque to outsiders), and 409 when no question is currently open for
// answers.
func HandleSessionAnswer(service *livesession.Service) http.Handler {
	type answerRequest struct {
		OptionID int64 `json:"optionId"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := handlers.LoggerFromContext(ctx)

		player, ok := auth.PlayerFromContext(ctx)
		if !ok {
			logger.ErrorContext(ctx, "missing player on context for session answer")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}
		logger = logger.With(slog.Int64("player", player.ID))

		req, err := handlers.DecodeJSON[answerRequest](w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		err = service.SubmitAnswer(ctx, r.PathValue("code"), player.ID, req.OptionID, time.Now().UTC())
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, livesession.ErrSessionNotFound), errors.Is(err, livesession.ErrNotParticipant):
			http.NotFound(w, r)
		case errors.Is(err, livesession.ErrQuestionNotOpen):
			http.Error(w, "no question is open for answers", http.StatusConflict)
		default:
			writeInternalError(w, r, logger, "error recording session answer", err)
		}
	})
}

// HandleSessionLeave drops the calling participant from the session,
// stamping their roster row as left so they fall out of the live reads
// (roster, answered-order badges, standings) at once. It reads no request
// body: a player leaves via navigator.sendBeacon on tab close, whose POST
// may carry an empty or non-JSON body, so the handler must not require one.
// Returns 204 on success and 404 for an unknown code, a non-participant, or a
// repeat leave (the row is already marked left) - the code stays opaque to
// outsiders, mirroring the other session gates.
func HandleSessionLeave(service *livesession.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := handlers.LoggerFromContext(ctx)

		player, ok := auth.PlayerFromContext(ctx)
		if !ok {
			logger.ErrorContext(ctx, "missing player on context for session leave")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}
		logger = logger.With(slog.Int64("player", player.ID))

		err := service.Leave(ctx, r.PathValue("code"), player.ID)
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, livesession.ErrSessionNotFound), errors.Is(err, livesession.ErrNotParticipant):
			http.NotFound(w, r)
		default:
			writeInternalError(w, r, logger, "error leaving session", err)
		}
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
	JoinCode string                  `json:"joinCode"`
	Phase    string                  `json:"phase"`
	HostID   int64                   `json:"hostId"`
	Players  []sessionPlayerResponse `json:"players"`
	// Quiz is the room's current quiz, omitted for an empty room with no quiz
	// picked yet (#836): the "no game running yet" staging state carries no quiz
	// metadata, so the field is absent rather than a zero-valued quiz.
	Quiz      *sessionQuizResponse `json:"quiz,omitempty"`
	ServerNow time.Time            `json:"serverNow"`
	// StartAt is the absolute deadline of the host's armed last-call countdown
	// (#735), as an ISO timestamp; omitted when no countdown is armed. Every
	// surface renders the same "Starting in M:SS" off startAt minus serverNow,
	// so a skewed device clock cannot desync the countdown.
	StartAt *time.Time `json:"startAt,omitempty"`
	// Question is the live question (round_intro carries no question yet; the
	// question and reveal phases do). Options never carry a correct flag
	// before reveal - correctOptionIds below is populated only at reveal.
	Question *sessionQuestionResponse `json:"question,omitempty"`
	// Standings is the per-player ranking the bar graph (MP-9) consumes: the
	// round delta + running total in the round_results phase, and the
	// cumulative final standings in the finished phase. Null in every other
	// phase. Ordered best-first, rank stamped 1-indexed.
	Standings []sessionStandingResponse `json:"standings,omitempty"`
	// Round describes the round the session is about to play (#748): its title,
	// summary, and 1-indexed position, so the between-rounds screen names the
	// round and words its heading correctly on the first round. Present only in
	// the round_intro phase; omitted otherwise.
	Round *sessionRoundResponse `json:"round,omitempty"`
}

// sessionRoundResponse is the round shown on the round_intro screen (#748).
// number is 1-indexed and total is the round count, so a surface knows
// number == 1 means the first round (no previous round) and words the
// heading accordingly. summary is empty when the round has none.
type sessionRoundResponse struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Number  int    `json:"number"`
	Total   int    `json:"total"`
}

// sessionStandingResponse is one player's place in the between-rounds /
// final ranking. roundScore is the player's points in the round that just
// finished; in the finished phase it carries the last round's score so the bar
// graph can animate that final contribution (0 for a player absent from the
// last round). totalScore is their cumulative session score; rank is 1-indexed.
type sessionStandingResponse struct {
	PlayerID    int64  `json:"playerId"`
	DisplayName string `json:"displayName"`
	RoundScore  int    `json:"roundScore"`
	TotalScore  int    `json:"totalScore"`
	Rank        int    `json:"rank"`
}

// sessionOptionResponse is one answer option. correct is surfaced ONLY in the
// reveal phase via the question's correctOptionIds; before reveal a surface
// sees option text and id only, so it cannot leak the answer.
type sessionOptionResponse struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
}

// sessionAnswerResponse is one recorded pick. playerId + answered order drive
// the answered badges; correct and score are populated ONLY at reveal (nil
// before), so a pre-reveal read never tells a client which pick was right.
type sessionAnswerResponse struct {
	PlayerID int64 `json:"playerId"`
	Correct  *bool `json:"correct,omitempty"`
	Score    *int  `json:"score,omitempty"`
}

// sessionQuestionResponse is the live question view. startedAt / expiresAt are
// the server answer window so a client drives its countdown off expiresAt
// minus serverNow. answeredPlayerIds is the pick order without correctness.
// correctOptionIds is empty until reveal.
type sessionQuestionResponse struct {
	ID                int64                   `json:"id"`
	RoundID           int64                   `json:"roundId"`
	Text              string                  `json:"text"`
	ImageURL          string                  `json:"imageUrl,omitempty"`
	Options           []sessionOptionResponse `json:"options"`
	StartedAt         *time.Time              `json:"startedAt,omitempty"`
	ExpiresAt         *time.Time              `json:"expiresAt,omitempty"`
	AnsweredPlayerIDs []int64                 `json:"answeredPlayerIds"`
	Answers           []sessionAnswerResponse `json:"answers"`
	CorrectOptionIDs  []int64                 `json:"correctOptionIds"`
}

// HandleSessionState returns the authoritative lobby state. Participant-
// gated: only a roster player or the host may read it, so a stranger with
// the code cannot enumerate the room. Returns 404 for an unknown code or a
// non-participant.
func HandleSessionState(service *livesession.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := handlers.LoggerFromContext(ctx)

		player, ok := auth.PlayerFromContext(ctx)
		if !ok {
			logger.ErrorContext(ctx, "missing player on context for session state")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}
		logger = logger.With(slog.Int64("player", player.ID))

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
		JoinCode:  state.Session.JoinCode,
		Phase:     string(state.Session.Phase),
		HostID:    state.Session.HostPlayerID,
		Players:   players,
		Quiz:      newSessionQuizResponse(state),
		ServerNow: time.Now().UTC(),
		StartAt:   state.Session.StartAt,
		Question:  newSessionQuestionResponse(state),
		Standings: newSessionStandingsResponse(state),
		Round:     newSessionRoundResponse(state),
	}
}

// newSessionQuizResponse projects the room's quiz onto the wire shape, or nil for
// an empty room with no quiz picked yet (#836), so the field is omitted from the
// JSON rather than dereferencing a nil quiz.
func newSessionQuizResponse(state *livesession.LobbyState) *sessionQuizResponse {
	if state.Quiz == nil {
		return nil
	}

	return &sessionQuizResponse{
		ID:            state.Quiz.ID,
		Title:         state.Quiz.Title,
		QuestionCount: len(state.Quiz.Questions),
	}
}

// newSessionRoundResponse projects the round_intro round onto the wire shape.
// Returns nil outside the round_intro phase (and when the round id resolved to
// no round), so the field is omitted from the JSON.
func newSessionRoundResponse(state *livesession.LobbyState) *sessionRoundResponse {
	if state.CurrentRound == nil {
		return nil
	}

	return &sessionRoundResponse{
		Title:   state.CurrentRound.Title,
		Summary: state.CurrentRound.Summary,
		Number:  state.CurrentRound.Number,
		Total:   state.CurrentRound.Total,
	}
}

// newSessionStandingsResponse projects the domain standings onto the wire
// shape. Returns nil outside the phases that carry standings (round_results
// and finished), so the field is omitted from the JSON.
func newSessionStandingsResponse(state *livesession.LobbyState) []sessionStandingResponse {
	if len(state.Standings) == 0 {
		return nil
	}
	standings := make([]sessionStandingResponse, 0, len(state.Standings))
	for _, st := range state.Standings {
		standings = append(standings, sessionStandingResponse{
			PlayerID:    st.PlayerID,
			DisplayName: st.DisplayName,
			RoundScore:  st.RoundScore,
			TotalScore:  st.TotalScore,
			Rank:        st.Rank,
		})
	}

	return standings
}

// newSessionQuestionResponse projects the live question view onto the wire
// shape, enforcing the no-spoiler guarantee: correctness (per-option and
// per-answer) is included only when state.Revealed is true.
func newSessionQuestionResponse(state *livesession.LobbyState) *sessionQuestionResponse {
	if state.CurrentQuestion == nil {
		return nil
	}
	q := state.CurrentQuestion

	options := make([]sessionOptionResponse, 0, len(q.Options))
	for _, o := range q.Options {
		options = append(options, sessionOptionResponse{ID: o.ID, Text: o.Text})
	}

	// The roster already excludes players who have left, so a left player's
	// pick drops out of the answered-order badges here (MP-10) without
	// touching the store read that also backs scoring at close.
	live := make(map[int64]struct{}, len(state.Session.Players))
	for _, p := range state.Session.Players {
		live[p.PlayerID] = struct{}{}
	}

	answeredIDs := make([]int64, 0, len(state.Answers))
	answers := make([]sessionAnswerResponse, 0, len(state.Answers))
	for _, a := range state.Answers {
		if _, ok := live[a.PlayerID]; !ok {
			continue
		}
		answeredIDs = append(answeredIDs, a.PlayerID)
		ans := sessionAnswerResponse{PlayerID: a.PlayerID}
		if state.Revealed {
			correct := a.Correct
			ans.Correct = &correct
			ans.Score = a.Score
		}
		answers = append(answers, ans)
	}

	correctIDs := []int64{}
	if state.Revealed {
		for _, o := range q.Options {
			if o.Correct {
				correctIDs = append(correctIDs, o.ID)
			}
		}
	}

	resp := &sessionQuestionResponse{
		ID:                q.ID,
		RoundID:           q.RoundID,
		Text:              q.Text,
		ImageURL:          mediaURL(q.MediaID),
		Options:           options,
		AnsweredPlayerIDs: answeredIDs,
		Answers:           answers,
		CorrectOptionIDs:  correctIDs,
	}
	if state.Session.QuestionStartedAt != nil {
		resp.StartedAt = state.Session.QuestionStartedAt
	}
	if state.Session.QuestionExpiresAt != nil {
		resp.ExpiresAt = state.Session.QuestionExpiresAt
	}

	return resp
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

// DefaultSessionEventHeartbeatInterval is how often the session SSE handler
// emits a no-op comment frame to keep the connection alive when the session
// is quiet. Same value and rationale as the leaderboard stream
// ([DefaultLeaderboardHeartbeatInterval]): 25s lands inside common proxy /
// NAT / mobile-carrier idle timeouts without the keep-alive cost of a
// faster tick. Production wiring (internal/server/routes.go) passes this
// value; the heartbeat regression test passes a shorter interval.
const DefaultSessionEventHeartbeatInterval = 25 * time.Second

// sessionEventStreamer bundles the per-request dependencies of the session
// SSE stream. Mirrors leaderboardStreamer so the two share the same flush /
// heartbeat / write-deadline handling.
type sessionEventStreamer struct {
	w                 http.ResponseWriter
	rc                *http.ResponseController
	logger            *slog.Logger
	heartbeatInterval time.Duration
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
// no-op comment frame every s.heartbeatInterval to keep an idle connection
// warm.
func (s *sessionEventStreamer) run(ctx context.Context, events <-chan livesession.Tick) {
	heartbeat := time.NewTicker(s.heartbeatInterval)
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

// beatPresence bumps a presence heartbeat while the SSE connection is held:
// once immediately, then every livesession.HeartbeatInterval until ctx is
// cancelled (the client disconnects). touch is the heartbeat the caller chose -
// the host's host_last_seen_at (which the runner's idle-close sweep reads) or a
// roster player's own last_seen_at (which the active-player count reads). Runs
// in its own goroutine so it does not block the stream loop; touch failures are
// logged at debug since a transient miss is recovered by the next beat and the
// presence simply ages out if the beats stop. Stopping on ctx cancel is what
// lets a dropped player or host go stale so the runner reacts (stops waiting on
// a dropped player; idle-closes a room once the host is away AND no players
// remain).
func beatPresence(ctx context.Context, logger *slog.Logger, touch func(context.Context) error) {
	beat := func() {
		if err := touch(ctx); err != nil {
			logger.DebugContext(ctx, "session heartbeat touch failed", slog.Any("err", err))
		}
	}
	beat()

	ticker := time.NewTicker(livesession.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			beat()
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
//
// heartbeatInterval is the gap between no-op SSE comment frames written on
// an otherwise idle stream; production passes
// [DefaultSessionEventHeartbeatInterval] and the heartbeat regression test
// shrinks it so the assertion runs in milliseconds, not seconds. A zero
// or negative value falls back to the default so a caller that constructs
// the wiring struct without the field does not panic at [time.NewTicker].
func HandleSessionEvents(
	service *livesession.Service, hub *livesession.Hub, heartbeatInterval time.Duration,
) http.Handler {
	heartbeatInterval = clampHeartbeat(heartbeatInterval, DefaultSessionEventHeartbeatInterval)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := handlers.LoggerFromContext(ctx)

		player, ok := auth.PlayerFromContext(ctx)
		if !ok {
			logger.ErrorContext(ctx, "missing player on context for session events")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}
		logger = logger.With(slog.Int64("player", player.ID))

		// Gate BEFORE any header write so a non-participant / unknown code
		// can still be surfaced as a proper HTTP 404 rather than a half-open
		// text/event-stream.
		view, err := service.AuthorizeView(ctx, r.PathValue("code"), player.ID)
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
		events, version, unsubscribe := hub.Subscribe(view.Code)
		defer unsubscribe()

		// The held connection is the presence heartbeat: bump now and on a
		// ticker while it is open, so a disconnect (ctx cancelled) lets the
		// presence go stale. The host beats host_last_seen_at (the runner's
		// idle-close sweep reads it); a roster player beats their own
		// last_seen_at (the runner's active-player count reads it).
		touch := func(ctx context.Context) error {
			return service.TouchLastSeen(ctx, view.Code, player.ID)
		}
		if view.IsHost {
			touch = func(ctx context.Context) error {
				return service.TouchHostLastSeen(ctx, view.Code)
			}
		}
		go beatPresence(ctx, logger, touch)

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

		streamer := &sessionEventStreamer{w: w, rc: rc, logger: logger, heartbeatInterval: heartbeatInterval}

		if !streamer.writeTick(ctx, livesession.Tick{Version: version, Phase: view.Phase}) {
			return
		}

		streamer.run(ctx, events)
	})
}
