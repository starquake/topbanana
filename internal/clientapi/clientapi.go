// Package clientapi provides HTTP handlers for the API used by the game client.
package clientapi

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/leaderboard"
	"github.com/starquake/topbanana/internal/quiz"
)

// writeInternalError records an internal failure and writes a generic
// 500 body. The wrapped error stays in the operator's logs (with
// context msg) but never reaches the client, where it would otherwise
// leak table names, SQL fragments, file paths, and other internals
// produced by sqlc / SQLite (#274).
//
// Callers pass `msg` as a short, fixed context phrase ("error
// retrieving leaderboard"), not user-controlled text. The body the
// client sees is "internal error" with the appropriate status.
func writeInternalError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, msg string, err error) {
	logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// writeClaimNameError writes a small JSON error body for the
// PATCH /api/players/me handler. The client (PlayerService.claimName)
// branches on `code` to differentiate "name already in use" from
// "this account is already non-anonymous" (#289). Falls back to the
// plain-text message on encode failure so the client at least sees a
// status + body it can render.
func writeClaimNameError(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	status int,
	code, message string,
) {
	body := struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message}
	if err := handlers.EncodeJSON(w, status, body); err != nil {
		logger.ErrorContext(r.Context(), "error encoding claimNameError", slog.Any("err", err))
		http.Error(w, message, status)
	}
}

// gameRequest extracts the gameID path parameter and the session player
// off the request. Every /api/games/{gameID}/* handler runs this gate
// once at the top of its closure so the participant check (#272) and
// the gameID validation stay in lockstep - the service refuses calls
// from a non-participant by returning ErrGameNotFound, which the
// handler maps to 404 alongside the genuine missing-game case.
//
// Writes the response and returns ok=false on any failure so the
// caller can early-return without re-handling errors.
func gameRequest(w http.ResponseWriter, r *http.Request, logger *slog.Logger) (string, int64, bool) {
	gameID := r.PathValue("gameID")
	if gameID == "" {
		// User-supplied 4xx - log at Info so the response carries the
		// signal, not an alert-triggering ERROR (#369).
		logger.InfoContext(r.Context(), "missing gameID in request path")
		http.Error(w, "missing gameID", http.StatusBadRequest)

		return "", 0, false
	}

	p, ok := auth.PlayerFromContext(r.Context())
	if !ok {
		logger.ErrorContext(r.Context(), "missing player on context for game request")
		http.Error(w, "internal error", http.StatusInternalServerError)

		return "", 0, false
	}

	return gameID, p.ID, true
}

// HandleQuizList returns a list of quizzes. Only visibility=public rows
// surface - unlisted is link-only and private is gated per-request at
// the GetQuiz path, neither of which fits a list (#103).
func HandleQuizList(logger *slog.Logger, quizStore quiz.Store) http.Handler {
	type quizResponse struct {
		ID          int64     `json:"id"`
		Title       string    `json:"title"`
		Slug        string    `json:"slug"`
		Description string    `json:"description"`
		CreatedAt   time.Time `json:"createdAt"`
	}

	type quizzesResponse []quizResponse

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		quizzes, err := quizStore.ListPublicQuizzes(r.Context())
		if err != nil {
			writeInternalError(w, r, logger, "error retrieving quizzes from store", err)

			return
		}

		res := make(quizzesResponse, 0, len(quizzes))
		for _, qz := range quizzes {
			qzr := quizResponse{
				ID:          qz.ID,
				Title:       qz.Title,
				Slug:        qz.Slug,
				Description: qz.Description,
				CreatedAt:   qz.CreatedAt,
			}
			res = append(res, qzr)
		}

		err = handlers.EncodeJSON(w, http.StatusOK, res)
		if err != nil {
			logger.ErrorContext(r.Context(), "error encoding quizzesResponse", slog.Any("err", err))

			return
		}
	})
}

// canReadQuiz applies the #103 visibility gate. Public and unlisted are
// reachable by anyone (unlisted requires guessing the slug+ID, which is
// out of scope for this ticket); private requires an authenticated
// player. Returns false when the caller is not authorised, in which
// case the response has already been written as a 404 so the gate is
// indistinguishable from a genuinely missing quiz.
func canReadQuiz(w http.ResponseWriter, r *http.Request, visibility string) bool {
	if visibility != quiz.VisibilityPrivate {
		return true
	}
	p, ok := auth.PlayerFromContext(r.Context())
	if !ok || p.IsAnonymous() {
		http.NotFound(w, r)

		return false
	}

	return true
}

// gateQuizRead reads the quiz's visibility by ID via the game service's
// quiz store proxy and applies canReadQuiz so the leaderboard,
// leaderboard-stream, my-game, and create-game handlers can reject access
// without duplicating the load + check + 404 dance. It reads only the
// visibility column, not the full questions/options tree.
func gateQuizRead(
	w http.ResponseWriter, r *http.Request,
	logger *slog.Logger, service *game.Service, quizID int64,
) bool {
	visibility, err := service.GetQuizVisibility(r.Context(), quizID)
	if err != nil {
		if errors.Is(err, quiz.ErrQuizNotFound) {
			http.NotFound(w, r)

			return false
		}
		writeInternalError(w, r, logger, "error retrieving quiz for visibility gate", err)

		return false
	}

	return canReadQuiz(w, r, visibility)
}

// decimalBase is the radix used when formatting ids as strings.
const decimalBase = 10

// mediaURL returns the serving path for a question's attached media (image or
// sound), or "" when none is attached (mediaID nil), which the wire structs
// omit via omitempty.
func mediaURL(mediaID *int64) string {
	if mediaID == nil {
		return ""
	}

	return "/media/" + strconv.FormatInt(*mediaID, decimalBase)
}

// leaderboardLimit caps the number of rows the REST + SSE leaderboards
// return. The current player's standing - if they're outside the top
// N - is carried separately on currentPlayer below (#181).
const leaderboardLimit = 10

// quizLeaderboardEntryResponse is one row of the leaderboard wire shape.
// Declared at package scope so both HandleQuizLeaderboard and
// HandleQuizLeaderboardStream can build it.
//
// InProgress is true when the player is still mid-quiz: Score may be a
// running partial total (#244) or zero if the player has clicked Start
// but not yet submitted their first answer (#335).
type quizLeaderboardEntryResponse struct {
	PlayerID        int64  `json:"playerId"`
	DisplayName     string `json:"displayName"`
	Score           int    `json:"score"`
	Rank            int    `json:"rank"`
	IsCurrentPlayer bool   `json:"isCurrentPlayer"`
	InProgress      bool   `json:"inProgress"`
}

// quizLeaderboardResponse is the full leaderboard wire shape. The SSE
// endpoint sends one of these per event; the REST endpoint sends one.
type quizLeaderboardResponse struct {
	QuizID        int64                          `json:"quizId"`
	Entries       []quizLeaderboardEntryResponse `json:"entries"`
	CurrentPlayer *quizLeaderboardEntryResponse  `json:"currentPlayer"`
}

func toEntryResponse(e game.LeaderboardEntry) quizLeaderboardEntryResponse {
	return quizLeaderboardEntryResponse{
		PlayerID:        e.PlayerID,
		DisplayName:     e.DisplayName,
		Score:           e.Score,
		Rank:            e.Rank,
		IsCurrentPlayer: e.IsCurrentPlayer,
		InProgress:      e.InProgress,
	}
}

// fetchQuizLeaderboard wraps the service call and shape translation so
// the two leaderboard handlers (REST + SSE) share one code path. Pure:
// it does not touch the [http.ResponseWriter] so the SSE handler can
// call it mid-stream (after headers are committed) without risk of
// writing an HTTP error response into the event-stream body. Callers
// map the returned error to the appropriate transport-level signal.
func fetchQuizLeaderboard(
	ctx context.Context,
	service *game.Service,
	quizID, playerID int64,
) (quizLeaderboardResponse, error) {
	result, err := service.GetQuizLeaderboard(ctx, quizID, playerID, leaderboardLimit)
	if err != nil {
		return quizLeaderboardResponse{}, fmt.Errorf("fetch quiz leaderboard: %w", err)
	}

	respEntries := make([]quizLeaderboardEntryResponse, 0, len(result.Entries))
	for _, e := range result.Entries {
		respEntries = append(respEntries, toEntryResponse(e))
	}

	res := quizLeaderboardResponse{QuizID: quizID, Entries: respEntries}
	if result.CurrentPlayer != nil {
		cp := toEntryResponse(*result.CurrentPlayer)
		res.CurrentPlayer = &cp
	}

	return res, nil
}

// writeQuizLeaderboardError translates a fetchQuizLeaderboard error into
// the right HTTP error response. Only safe to call before any response
// body has been written - the SSE handler uses this for the initial
// snapshot only, and just exits the stream on subsequent errors.
func writeQuizLeaderboardError(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	err error,
) {
	if errors.Is(err, quiz.ErrQuizNotFound) {
		http.NotFound(w, r)

		return
	}
	writeInternalError(w, r, logger, "error retrieving quiz leaderboard", err)
}

// HandleQuizLeaderboard returns the top scoring players for the given quiz.
// Each player appears at most once, with their total score for the quiz; ties
// are broken by ascending displayName for a stable order. IsCurrentPlayer is set
// on the entry that matches the authenticated player on the request context
// so the client can highlight that row.
//
// The response also carries a currentPlayer field with the requesting
// player's rank and score, populated even when the player landed outside
// the truncated top-N - so callers can show an off-leaderboard standing
// without a second round-trip. See #181.
func HandleQuizLeaderboard(logger *slog.Logger, service *game.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		quizID, ok := handlers.ParseIDFromSlugPath(w, r, logger, "slugID")
		if !ok {
			return
		}

		if !gateQuizRead(w, r, logger, service, quizID) {
			return
		}

		player, ok := auth.PlayerFromContext(ctx)
		if !ok {
			// EnsurePlayer middleware should have populated this; reaching
			// here means the route was wired without it.
			logger.ErrorContext(ctx, "missing player on context for quiz leaderboard")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		res, err := fetchQuizLeaderboard(ctx, service, quizID, player.ID)
		if err != nil {
			writeQuizLeaderboardError(w, r, logger, err)

			return
		}

		if err := handlers.EncodeJSON(w, http.StatusOK, res); err != nil {
			logger.ErrorContext(ctx, "error encoding leaderboardResponse", slog.Any("err", err))

			return
		}
	})
}

// leaderboardStreamer bundles the per-request dependencies of the SSE
// leaderboard stream. Methods on this type keep helper signatures
// small instead of threading six parameters through each call.
type leaderboardStreamer struct {
	w                 http.ResponseWriter
	rc                *http.ResponseController
	logger            *slog.Logger
	service           *game.Service
	quizID            int64
	playerID          int64
	heartbeatInterval time.Duration
}

// writeEvent writes the given leaderboard response as a single SSE
// `data:` frame and flushes. Returns false on any write/flush failure
// (client disconnected, broken pipe, encoding error) so the caller can
// exit the stream loop cleanly.
func (s *leaderboardStreamer) writeEvent(ctx context.Context, res quizLeaderboardResponse) bool {
	payload, err := json.Marshal(res)
	if err != nil {
		s.logger.ErrorContext(ctx, "error marshalling leaderboard event", slog.Any("err", err))

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

// writeHeartbeat writes a single SSE comment frame and flushes. The
// frame is `:\n\n` - comment lines start with a colon and the spec
// requires EventSource implementations to ignore them, so this never
// fires a client-side `onmessage`. Its only job is to keep the TCP
// connection warm so Firefox / intermediate proxies don't tear down
// an idle SSE stream as NS_ERROR_PARTIAL_TRANSFER (#244 follow-up).
// Returns false on write/flush failure so the caller can exit cleanly.
func (s *leaderboardStreamer) writeHeartbeat() bool {
	if _, err := fmt.Fprint(s.w, ":\n\n"); err != nil {
		return false
	}
	if err := s.rc.Flush(); err != nil {
		return false
	}

	return true
}

// DefaultLeaderboardHeartbeatInterval is how often the SSE handler emits
// a no-op comment frame to keep the connection alive when the hub is
// quiet. The HTTP server's WriteTimeout no longer kills the response
// (the handler clears its own write deadline), so this only exists as
// insurance against intermediate proxy / NAT / mobile-carrier idle
// timeouts that aren't visible during local-dev testing - nginx
// defaults to 60s, HAProxy ~50s, mobile NATs sometimes 30s. 25s lands
// comfortably inside all of those without the keep-alive cost of a
// 10s tick. Production wiring (internal/server/routes.go) passes this
// value; the heartbeat regression tests pass a shorter interval.
const DefaultLeaderboardHeartbeatInterval = 25 * time.Second

// clampHeartbeat falls back to fallback when d is non-positive, so a
// caller that builds the wiring struct without the field does not panic
// at [time.NewTicker] inside the SSE handler.
func clampHeartbeat(d, fallback time.Duration) time.Duration {
	if d <= 0 {
		return fallback
	}

	return d
}

// HandleQuizLeaderboardStream pushes leaderboard snapshots over SSE.
// On every hub tick the handler re-fetches and emits one full snapshot,
// not a delta - so a slow client coalesces multiple commits into one
// repaint via the per-subscriber buffer of 1.
//
// heartbeatInterval is the gap between no-op SSE comment frames written
// on an otherwise idle stream; production passes
// [DefaultLeaderboardHeartbeatInterval] and the heartbeat regression
// test shrinks it so the assertion runs in milliseconds, not seconds.
// A zero or negative value falls back to the default so a caller that
// constructs the wiring struct without the field does not panic at
// [time.NewTicker].
func HandleQuizLeaderboardStream(
	logger *slog.Logger, service *game.Service, hub *leaderboard.Hub,
	heartbeatInterval time.Duration,
) http.Handler {
	heartbeatInterval = clampHeartbeat(heartbeatInterval, DefaultLeaderboardHeartbeatInterval)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		quizID, ok := handlers.ParseIDFromSlugPath(w, r, logger, "slugID")
		if !ok {
			return
		}

		if !gateQuizRead(w, r, logger, service, quizID) {
			return
		}

		player, ok := auth.PlayerFromContext(ctx)
		if !ok {
			logger.ErrorContext(ctx, "missing player on context for leaderboard stream")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		// Subscribe BEFORE the initial snapshot so we never miss a publish
		// that lands between fetch and subscribe.
		events, unsubscribe := hub.Subscribe(quizID)
		defer unsubscribe()

		// Initial fetch BEFORE any header write so an error (ErrQuizNotFound,
		// store hiccup) can still be surfaced as a proper HTTP status.
		// Subsequent fetch errors inside the loop happen after the response
		// is committed as text/event-stream, so they cannot be reported as
		// HTTP status codes - we log and end the stream there.
		res, err := fetchQuizLeaderboard(ctx, service, quizID, player.ID)
		if err != nil {
			writeQuizLeaderboardError(w, r, logger, err)

			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Connection", "keep-alive")
		// Disable proxy-level buffering (nginx etc.) so the client sees
		// events promptly rather than in large flushes.
		w.Header().Set("X-Accel-Buffering", "no")

		rc := http.NewResponseController(w)
		// The HTTP server's WriteTimeout (10s) would otherwise kill
		// this long-lived response on the first write past the deadline
		// - every heartbeat after 10s fails and the loop exits, which
		// shows up as a 10.003s stream that EventSource has to
		// reconnect. Zero disables the per-request deadline. The
		// underlying TCP connection stays governed by OS keepalives
		// and the request context (cancelled on client disconnect).
		if err := rc.SetWriteDeadline(time.Time{}); err != nil {
			logger.WarnContext(ctx, "could not clear SSE write deadline", slog.Any("err", err))
		}

		streamer := &leaderboardStreamer{
			w:                 w,
			rc:                rc,
			logger:            logger,
			service:           service,
			quizID:            quizID,
			playerID:          player.ID,
			heartbeatInterval: heartbeatInterval,
		}

		if !streamer.writeEvent(ctx, res) {
			return
		}

		streamer.run(ctx, events)
	})
}

// run drains the hub channel and writes one SSE frame per tick until
// the client disconnects or the channel closes. Refresh errors after
// the initial snapshot cannot be reported as HTTP status (the response
// is already committed as text/event-stream), so the loop logs and
// exits - the client will reconnect via EventSource and re-run the
// initial-snapshot path, which can surface the error cleanly.
//
// The heartbeat ticker emits a no-op SSE comment frame every
// s.heartbeatInterval to keep the connection warm; without it Firefox
// closes idle streams after ~30s with NS_ERROR_PARTIAL_TRANSFER.
func (s *leaderboardStreamer) run(ctx context.Context, events <-chan struct{}) {
	heartbeat := time.NewTicker(s.heartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-events:
			if !ok {
				return
			}
			res, err := fetchQuizLeaderboard(ctx, s.service, s.quizID, s.playerID)
			if err != nil {
				s.logger.ErrorContext(ctx, "error refreshing leaderboard for SSE", slog.Any("err", err))

				return
			}
			if !s.writeEvent(ctx, res) {
				return
			}
		case <-heartbeat.C:
			if !s.writeHeartbeat() {
				return
			}
		}
	}
}

// HandleCreateGame creates a new game.
// It first checks if the quiz exists, then creates the game and participant, and finally starts the game.
// Returns the ID of the created game.
// Returns 201 if the game was created successfully.
// Returns 400 if the request body is invalid.
// Returns 404 if the quiz does not exist.
// Returns 409 if the player already has a game for the quiz (in-progress or
// completed); the client should call GET /api/quizzes/{slugID}/my-game to
// resolve.
// Returns 500 if an error occurs.
func HandleCreateGame(logger *slog.Logger, service *game.Service) http.Handler {
	type createGameRequest struct {
		QuizID int64 `json:"quizId"`
	}

	type createGameResponse struct {
		ID string `json:"id"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var err error
		var req createGameRequest
		req, err = handlers.DecodeJSON[createGameRequest](w, r)
		if err != nil {
			logger.ErrorContext(ctx, "error decoding createGameRequest", slog.Any("err", err))
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		player, ok := auth.PlayerFromContext(ctx)
		if !ok {
			// EnsurePlayer middleware should have populated this; reaching
			// here means the route was wired without it.
			logger.ErrorContext(ctx, "missing player on context for create game")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		if !gateQuizRead(w, r, logger, service, req.QuizID) {
			return
		}

		g, err := service.CreateGame(ctx, req.QuizID, player.ID)
		if err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) {
				http.NotFound(w, r)

				return
			}
			if errors.Is(err, game.ErrGameAlreadyExists) {
				http.Error(w, err.Error(), http.StatusConflict)

				return
			}
			writeInternalError(w, r, logger, "error creating game", err)

			return
		}
		res := createGameResponse{ID: g.ID}

		w.Header().Set("Location", fmt.Sprintf("/play/game/%v", g.ID))
		err = handlers.EncodeJSON(w, http.StatusCreated, res)
		if err != nil {
			logger.ErrorContext(r.Context(), "error encoding createGameResponse", slog.Any("err", err))

			return
		}
	})
}

// HandleGameForQuiz is the resume probe: callers POST /api/games or
// continue an existing game based on the response. `completed` is true
// only when every question has been issued AND none is in its answer
// window, so a reload on the final question resumes there instead of
// jumping to the post-game leaderboard (#310).
func HandleGameForQuiz(logger *slog.Logger, service *game.Service) http.Handler {
	type gameForQuizResponse struct {
		GameID    string `json:"gameId"`
		Completed bool   `json:"completed"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		quizID, ok := handlers.ParseIDFromSlugPath(w, r, logger, "slugID")
		if !ok {
			return
		}

		if !gateQuizRead(w, r, logger, service, quizID) {
			return
		}

		player, ok := auth.PlayerFromContext(ctx)
		if !ok {
			logger.ErrorContext(ctx, "missing player on context for game-for-quiz")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		g, err := service.GetGameForPlayerOnQuiz(ctx, player.ID, quizID)
		if err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) || errors.Is(err, game.ErrGameNotFound) {
				http.NotFound(w, r)

				return
			}
			writeInternalError(w, r, logger, "error retrieving game for quiz", err)

			return
		}

		res := gameForQuizResponse{GameID: g.ID, Completed: g.IsCompleted() && !g.HasOpenQuestion()}

		if err = handlers.EncodeJSON(w, http.StatusOK, res); err != nil {
			logger.ErrorContext(ctx, "error encoding gameForQuizResponse", slog.Any("err", err))

			return
		}
	})
}

// nextOptionResponse is one option on the `type=question` /next
// variant. Hoisted to package scope so the handler can stay under the
// revive function-length budget (#167 slice 2).
type nextOptionResponse struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
}

// nextQuestionResponse is the wire shape for the `type=question`
// /next variant. Position/Total drive the HUD chip (#253); ServerNow
// drives the client clock-offset correction (#180).
type nextQuestionResponse struct {
	Type        string               `json:"type"`
	ID          int64                `json:"id"`
	Text        string               `json:"text"`
	ImageURL    string               `json:"imageUrl,omitempty"`
	AudioURL    string               `json:"audioUrl,omitempty"`
	AudioRepeat bool                 `json:"audioRepeat,omitempty"`
	Options     []nextOptionResponse `json:"options"`
	StartedAt   time.Time            `json:"startedAt"`
	ExpiredAt   time.Time            `json:"expiredAt"`
	ServerNow   time.Time            `json:"serverNow"`
	Position    int                  `json:"position"`
	Total       int                  `json:"total"`
	// RoundNumber/RoundTotal place the question's round within the quiz,
	// and RoundPosition/RoundQuestions place the question within that
	// round, for the gameplay header's "Round N of M" heading and its
	// per-round "Q n / m" chip.
	RoundNumber    int `json:"roundNumber"`
	RoundTotal     int `json:"roundTotal"`
	RoundPosition  int `json:"roundPosition"`
	RoundQuestions int `json:"roundQuestions"`
}

// nextRoundIntroResponse is the wire shape for the intro phase of the
// `type=round_boundary` /next variant (#548). It is emitted before a
// round's first question and carries the round title + summary so the
// client can show what is coming. StartedAt/ExpiredAt bound a countdown
// one quiz-default answer duration long so the client auto-advances the
// card when it expires; Total keeps the HUD chip rendering across the
// boundary. No score is carried at the intro.
type nextRoundIntroResponse struct {
	Type      string    `json:"type"`
	Phase     string    `json:"phase"`
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	StartedAt time.Time `json:"startedAt"`
	ExpiredAt time.Time `json:"expiredAt"`
	ServerNow time.Time `json:"serverNow"`
	Total     int       `json:"total"`
}

// nextRoundResultsResponse is the wire shape for the results phase of
// the `type=round_boundary` /next variant (#548). It is emitted after a
// round's questions and carries the player's own recap for the round:
// Score is the running game total, RoundScore the points earned for
// this round, and RoundCorrect of RoundQuestions the questions answered
// correctly in this round. There is deliberately no cross-player
// leaderboard here - the recap is self-referential only.
type nextRoundResultsResponse struct {
	Type           string    `json:"type"`
	Phase          string    `json:"phase"`
	ID             int64     `json:"id"`
	Title          string    `json:"title"`
	Score          int       `json:"score"`
	RoundScore     int       `json:"roundScore"`
	RoundCorrect   int       `json:"roundCorrect"`
	RoundQuestions int       `json:"roundQuestions"`
	StartedAt      time.Time `json:"startedAt"`
	ExpiredAt      time.Time `json:"expiredAt"`
	ServerNow      time.Time `json:"serverNow"`
	Total          int       `json:"total"`
}

// HandleQuestionNext returns the next item in the play sequence as a
// tagged union (`type: "question"` | `"round_boundary"`). Total counts
// quiz questions, not items, so a round boundary does not bump the HUD
// position chip. Both variants carry StartedAt/ExpiredAt (the boundary
// window drives the auto-advance countdown); Score is omitted on
// questions.
func HandleQuestionNext(logger *slog.Logger, service *game.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gameID, playerID, ok := gameRequest(w, r, logger)
		if !ok {
			return
		}

		item, err := service.GetNext(r.Context(), gameID, playerID)
		if err != nil {
			writeGetNextError(w, r, logger, err)

			return
		}

		if item.Type == game.ItemTypeRoundBoundary {
			writeRoundBoundaryItem(w, r, logger, item)

			return
		}
		writeQuestionItem(w, r, logger, gameID, item.Question)
	})
}

// writeGetNextError translates the sentinels returned by
// [game.Service.GetNext] into the right HTTP status so HandleQuestionNext
// stays under revive's function-length limit. Errors that should be
// observable to the player (missing game, exhausted quiz) map to 404;
// anything else surfaces as a generic 500 with the wrapped error in
// the operator log (#274).
func writeGetNextError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, game.ErrGameNotFound),
		errors.Is(err, quiz.ErrQuizNotFound),
		errors.Is(err, game.ErrNoMoreQuestions):
		http.NotFound(w, r)
	default:
		writeInternalError(w, r, logger, "error retrieving next item", err)
	}
}

// writeRoundBoundaryItem encodes a round-boundary-variant /next
// response. Split out of HandleQuestionNext to keep that handler under
// the function-length limit; the round-boundary path has no shuffle so
// the helper is a thin field projection (the auto-advance window is
// computed in the service alongside the question window). The intro and
// results phases (#548) project different fields, so each gets its own
// response struct.
func writeRoundBoundaryItem(w http.ResponseWriter, r *http.Request, logger *slog.Logger, item *game.Item) {
	var res any
	if item.Phase == game.RoundPhaseResults {
		res = nextRoundResultsResponse{
			Type:           string(game.ItemTypeRoundBoundary),
			Phase:          string(game.RoundPhaseResults),
			ID:             item.Round.ID,
			Title:          item.Round.Title,
			Score:          item.Score,
			RoundScore:     item.RoundScore,
			RoundCorrect:   item.RoundCorrect,
			RoundQuestions: item.RoundQuestions,
			StartedAt:      item.StartedAt,
			ExpiredAt:      item.ExpiredAt,
			ServerNow:      time.Now().UTC(),
			Total:          item.Total,
		}
	} else {
		res = nextRoundIntroResponse{
			Type:      string(game.ItemTypeRoundBoundary),
			Phase:     string(game.RoundPhaseIntro),
			ID:        item.Round.ID,
			Title:     item.Round.Title,
			Summary:   item.Round.Summary,
			StartedAt: item.StartedAt,
			ExpiredAt: item.ExpiredAt,
			ServerNow: time.Now().UTC(),
			Total:     item.Total,
		}
	}
	if err := handlers.EncodeJSON(w, http.StatusOK, res); err != nil {
		logger.ErrorContext(r.Context(), "error encoding round boundary item", slog.Any("err", err))
	}
}

// writeQuestionItem encodes a question-variant /next response. The
// per-game stable shuffle of the option buttons (#297) is applied
// here so a reload returns the same layout for the same (game,
// question) pair; two players answering the same question in
// different games see different orders.
func writeQuestionItem(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger, gameID string, gq *game.Question,
) {
	resOptions := make([]nextOptionResponse, len(gq.QuizQuestion.Options))
	for i, o := range gq.QuizQuestion.Options {
		resOptions[i] = nextOptionResponse{ID: o.ID, Text: o.Text}
	}
	shuffleBySeed(gameID, gq.QuestionID, len(resOptions), func(i, j int) {
		resOptions[i], resOptions[j] = resOptions[j], resOptions[i]
	})

	res := nextQuestionResponse{
		Type:           string(game.ItemTypeQuestion),
		ID:             gq.QuizQuestion.ID,
		Text:           gq.QuizQuestion.Text,
		ImageURL:       mediaURL(gq.QuizQuestion.ImageMediaID),
		AudioURL:       mediaURL(gq.QuizQuestion.AudioMediaID),
		AudioRepeat:    gq.QuizQuestion.AudioRepeat,
		Options:        resOptions,
		StartedAt:      gq.StartedAt,
		ExpiredAt:      gq.ExpiredAt,
		ServerNow:      time.Now().UTC(),
		Position:       gq.Position,
		Total:          gq.Total,
		RoundNumber:    gq.RoundNumber,
		RoundTotal:     gq.RoundTotal,
		RoundPosition:  gq.RoundPosition,
		RoundQuestions: gq.RoundQuestions,
	}

	if err := handlers.EncodeJSON(w, http.StatusOK, res); err != nil {
		logger.ErrorContext(r.Context(), "error encoding question item", slog.Any("err", err))
	}
}

// audioClipResponse is one entry in the audio-manifest wire shape: the
// metadata a play surface needs to preload one question's audio at game
// start, without any question text, options, or answer key (so the manifest
// never leaks gameplay content). questionId correlates the clip with the
// question when it is later played; audioUrl is the /media path; audioRepeat
// mirrors the question's AudioRepeat flag. Shared by both manifest endpoints
// (solo and host) so they emit an identical shape (#1088).
type audioClipResponse struct {
	QuestionID  int64  `json:"questionId"`
	AudioURL    string `json:"audioUrl"`
	AudioRepeat bool   `json:"audioRepeat"`
}

// audioManifestResponse is the audio-manifest wire shape: only the
// audio-bearing questions of the quiz, ordered by play position. clips is
// always a non-nil slice so an audio-free quiz serializes as an empty array,
// not null.
type audioManifestResponse struct {
	Clips []audioClipResponse `json:"clips"`
}

// newAudioManifestResponse projects a quiz's questions onto the manifest wire
// shape, keeping only the questions with audio attached and preserving their
// input order (the quiz's position order). Used by both manifest endpoints so
// the solo and host surfaces emit the same shape (#1088).
func newAudioManifestResponse(questions []*quiz.Question) audioManifestResponse {
	clips := make([]audioClipResponse, 0, len(questions))
	for _, q := range questions {
		if q.AudioMediaID == nil {
			continue
		}
		clips = append(clips, audioClipResponse{
			QuestionID:  q.ID,
			AudioURL:    mediaURL(q.AudioMediaID),
			AudioRepeat: q.AudioRepeat,
		})
	}

	return audioManifestResponse{Clips: clips}
}

// HandleGameAudio returns the audio-preload manifest for a solo game's quiz:
// the audio-bearing questions in play order, each with its question id, media
// URL, and repeat flag (#1088). Authorized exactly like
// [HandleQuestionNext] - the participant gate means a non-participant gets a
// 404, indistinguishable from a missing game. Carries no question text,
// options, or answer key, so a player cannot read it to pre-learn the quiz.
func HandleGameAudio(logger *slog.Logger, service *game.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gameID, playerID, ok := gameRequest(w, r, logger)
		if !ok {
			return
		}

		questions, err := service.GetAudioManifest(r.Context(), gameID, playerID)
		if err != nil {
			writeGetNextError(w, r, logger, err)

			return
		}

		if err = handlers.EncodeJSON(w, http.StatusOK, newAudioManifestResponse(questions)); err != nil {
			logger.ErrorContext(r.Context(), "error encoding audio manifest", slog.Any("err", err))
		}
	})
}

// HandleRoundSeen records acknowledgement of one round boundary phase
// (intro or results) carried in the {phase} path value (#548).
// Idempotent: second call returns 204 because the store INSERTs ON
// CONFLICT DO NOTHING. Game-not-found, not-a-participant, and
// round-not-in-quiz all return 404 to keep ids opaque to outsiders; an
// unknown phase returns 400.
func HandleRoundSeen(logger *slog.Logger, service *game.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gameID, playerID, ok := gameRequest(w, r, logger)
		if !ok {
			return
		}

		roundID, ok := handlers.ParseIDFromPath(w, r, logger, "roundID")
		if !ok {
			return
		}

		phase := game.RoundPhase(r.PathValue("phase"))

		if err := service.MarkRoundSeen(r.Context(), gameID, playerID, roundID, phase); err != nil {
			switch {
			case errors.Is(err, game.ErrInvalidRoundPhase):
				http.Error(w, err.Error(), http.StatusBadRequest)
			case errors.Is(err, game.ErrGameNotFound), errors.Is(err, quiz.ErrRoundNotFound):
				http.NotFound(w, r)
			default:
				writeInternalError(w, r, logger, "error marking round seen", err)
			}

			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}

// correctOptionIDsFromAnswer extracts the IDs of every option flagged
// correct on the question the player just answered. SubmitAnswer
// populates Answer.Question.QuizQuestion with the full option set so
// this read is local - no extra store round-trip. Returns nil when the
// quiz question was not populated (defensive; shouldn't happen in the
// production code path).
func correctOptionIDsFromAnswer(a *game.Answer) []int64 {
	if a.Question == nil || a.Question.QuizQuestion == nil {
		return nil
	}
	var ids []int64
	for _, o := range a.Question.QuizQuestion.Options {
		if o.Correct {
			ids = append(ids, o.ID)
		}
	}

	return ids
}

// writeSubmitAnswerError maps the sentinels returned by
// [game.Service.SubmitAnswer] to the right HTTP status. Pulled out of
// HandleAnswerPost so the handler stays under revive's
// function-length limit.
//   - ErrGameNotFound / ErrQuestionNotInGame -> 404
//   - ErrOptionNotInQuestion -> 400
//   - ErrAnswerAlreadyRecorded -> 409 (double-tap / retry; #353)
//   - ErrAnswerWindowClosed -> 409 (answer arrived too late; #1163)
//   - anything else -> 500 via writeInternalError
func writeSubmitAnswerError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, game.ErrGameNotFound), errors.Is(err, game.ErrQuestionNotInGame):
		http.NotFound(w, r)
	case errors.Is(err, game.ErrOptionNotInQuestion):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, game.ErrAnswerAlreadyRecorded), errors.Is(err, game.ErrAnswerWindowClosed):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		writeInternalError(w, r, logger, "error submitting answer", err)
	}
}

// HandleAnswerPost handles the submission of an answer for a game question.
// It decodes the request body, extracts game and question IDs from the path,
// and uses the game service to submit the answer.
func HandleAnswerPost(logger *slog.Logger, service *game.Service) http.Handler {
	// TappedAt is what the client claims as the moment of the tap; the
	// service clamps it to [question.StartedAt, time.Now()] so an
	// honest player on a slow link doesn't get scored late by accident
	// (#237). Missing/zero falls back to the server's now on the
	// service side.
	type answerRequest struct {
		OptionID int64     `json:"optionId"`
		TappedAt time.Time `json:"tappedAt"`
	}

	// CorrectOptionIDs always carries the question's correct option set
	// so the client can light up the right answer after a wrong pick
	// (#233) without branching on Correct.
	type answerResponse struct {
		Correct          bool    `json:"correct"`
		Score            int     `json:"score"`
		CorrectOptionIDs []int64 `json:"correctOptionIds"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gameID, playerID, ok := gameRequest(w, r, logger)
		if !ok {
			return
		}

		questionID, ok := handlers.ParseIDFromPath(w, r, logger, "questionID")
		if !ok {
			return
		}

		req, err := handlers.DecodeJSON[answerRequest](w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		a, err := service.SubmitAnswer(r.Context(), gameID, playerID, questionID, req.OptionID, req.TappedAt)
		if err != nil {
			writeSubmitAnswerError(w, r, logger, err)

			return
		}

		score := service.CalculateScore(r.Context(), a)

		res := answerResponse{
			Correct:          a.Option.Correct,
			Score:            score,
			CorrectOptionIDs: correctOptionIDsFromAnswer(a),
		}

		err = handlers.EncodeJSON(w, http.StatusOK, res)
		if err != nil {
			logger.ErrorContext(r.Context(), "error encoding answerResponse", slog.Any("err", err))

			return
		}
	})
}

// playerResponse is the JSON shape for GET and PATCH /api/players/me. The three
// flags are independent: isAnonymous (credential-less guest), isAuthenticated
// (signed-in account), and hasCustomName (picked their own name) can mix, e.g. a
// renamed guest is hasCustomName and still isAnonymous.
type playerResponse struct {
	ID              int64  `json:"id"`
	DisplayName     string `json:"displayName"`
	IsAnonymous     bool   `json:"isAnonymous"`
	HasCustomName   bool   `json:"hasCustomName"`
	IsAuthenticated bool   `json:"isAuthenticated"`
}

// newPlayerResponse projects an auth.Player onto the wire format.
func newPlayerResponse(p *auth.Player) playerResponse {
	return playerResponse{
		ID:              p.ID,
		DisplayName:     p.DisplayName,
		IsAnonymous:     p.IsAnonymous(),
		HasCustomName:   p.HasCustomName(),
		IsAuthenticated: p.IsAuthenticated(),
	}
}

// HandlePlayerGetMe returns a handler for GET /api/players/me that reports
// the calling player's id, displayName, whether they are still anonymous
// (no password_hash set), and whether they have explicitly picked a
// display name. hasCustomName and isAnonymous are deliberately
// independent concepts: a registered user with a password is never
// anonymous, but a claimed-but-passwordless visitor still is - callers
// that care about "did this player choose this name" should look at
// hasCustomName, not isAnonymous. The displayName is shown verbatim so a
// fresh petname can be displayed as-is until the player renames.
func HandlePlayerGetMe(logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		current, ok := auth.PlayerFromContext(ctx)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)

			return
		}

		if err := handlers.EncodeJSON(w, http.StatusOK, newPlayerResponse(current)); err != nil {
			logger.ErrorContext(ctx, "error encoding playerResponse", slog.Any("err", err))

			return
		}
	})
}

// HandlePlayerClaimName is the score-claim rename for anonymous
// visitors: the player keeps the same row and session cookie and stays
// anonymous after picking a display name. 409 covers both
// displayName-taken and already-claimed-via-register; the distinct
// messages let the client tell them apart.
func HandlePlayerClaimName(
	logger *slog.Logger, players auth.PlayerStore, gameService *game.Service,
) http.Handler {
	type claimNameRequest struct {
		DisplayName string `json:"displayName"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		current, ok := auth.PlayerFromContext(ctx)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)

			return
		}

		req, err := handlers.DecodeJSON[claimNameRequest](w, r)
		if err != nil {
			logger.ErrorContext(ctx, "error decoding claimNameRequest", slog.Any("err", err))
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}
		trimmed := strings.TrimSpace(req.DisplayName)
		if trimmed == "" {
			http.Error(w, "display name is required", http.StatusBadRequest)

			return
		}
		if utf8.RuneCountInString(trimmed) > auth.MaxDisplayNameLength {
			writeClaimNameError(w, r, logger,
				http.StatusBadRequest, "display_name_too_long",
				fmt.Sprintf("display name must be at most %d characters", auth.MaxDisplayNameLength))

			return
		}

		updated, err := players.UpdatePlayerDisplayName(ctx, current.ID, req.DisplayName)
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrDisplayNameTaken):
				writeClaimNameError(w, r, logger,
					http.StatusConflict, "display_name_taken", "display name already taken")
			case errors.Is(err, auth.ErrPlayerNotAnonymous):
				// #289: distinct code so the JS can tell "name in use
				// by someone else" from "this account already has a
				// claimed name". The latter is a state-drift signal -
				// the client should re-fetch /me and dismiss the
				// modal, not show "name is taken".
				writeClaimNameError(w, r, logger,
					http.StatusConflict, "already_claimed", "display name already set for this account")
			case errors.Is(err, auth.ErrDisplayNameEmpty):
				writeClaimNameError(w, r, logger,
					http.StatusBadRequest, "display_name_required", "display name is required")
			default:
				writeInternalError(w, r, logger, "error updating player displayName", err)
			}

			return
		}

		// Republish leaderboard ticks on every quiz the renamed player
		// appears on so other clients' SSE streams pick up the new
		// display name without waiting for the next answer-submit
		// publish. Best-effort: a failure here logs but does not fail
		// the HTTP response - the rename itself already succeeded.
		if perr := gameService.PublishLeaderboardForPlayer(ctx, current.ID); perr != nil {
			logger.ErrorContext(ctx, "error publishing leaderboard for renamed player",
				slog.Int64("playerId", current.ID), slog.Any("err", perr))
		}

		if err = handlers.EncodeJSON(w, http.StatusOK, newPlayerResponse(updated)); err != nil {
			logger.ErrorContext(ctx, "error encoding playerResponse", slog.Any("err", err))

			return
		}
	})
}

// HandleGameResults returns the results of a game based on its ID.
func HandleGameResults(logger *slog.Logger, service *game.Service) http.Handler {
	type playerScoreResponse struct {
		PlayerID int64 `json:"playerId"`
		Score    int   `json:"score"`
	}

	type resultsResponse struct {
		GameID string `json:"gameId"`
		Winner string `json:"winner"`

		PlayerScores []playerScoreResponse `json:"playerScores"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gameID, playerID, ok := gameRequest(w, r, logger)
		if !ok {
			return
		}

		results, err := service.GetResults(r.Context(), gameID, playerID)
		if err != nil {
			if errors.Is(err, game.ErrGameNotFound) {
				// User-supplied bad ID - Info, not Error (#369).
				logger.InfoContext(r.Context(), "game not found", slog.Any("err", err))
				http.NotFound(w, r)

				return
			}
			writeInternalError(w, r, logger, "error retrieving game results", err)

			return
		}

		psr := make([]playerScoreResponse, 0, len(results.PlayerScores))
		for psKey, psVal := range results.PlayerScores {
			psr = append(psr, playerScoreResponse{
				PlayerID: psKey,
				Score:    psVal,
			})
		}
		// Map iteration is randomized; sort for a deterministic wire order
		// (score desc, then player id asc).
		slices.SortFunc(psr, func(a, b playerScoreResponse) int {
			if c := cmp.Compare(b.Score, a.Score); c != 0 {
				return c
			}

			return cmp.Compare(a.PlayerID, b.PlayerID)
		})
		var winner string
		if results.Winner != 0 {
			winner = strconv.FormatInt(results.Winner, decimalBase)
		}
		res := resultsResponse{
			GameID:       gameID,
			Winner:       winner,
			PlayerScores: psr,
		}

		err = handlers.EncodeJSON(w, http.StatusOK, res)
		if err != nil {
			logger.ErrorContext(r.Context(), "error encoding resultsResponse", slog.Any("err", err))

			return
		}
	})
}
