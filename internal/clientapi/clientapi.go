// Package clientapi provides HTTP handlers for the API used by the game client.
package clientapi

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

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
// the gameID validation stay in lockstep — the service refuses calls
// from a non-participant by returning ErrGameNotFound, which the
// handler maps to 404 alongside the genuine missing-game case.
//
// Writes the response and returns ok=false on any failure so the
// caller can early-return without re-handling errors.
func gameRequest(w http.ResponseWriter, r *http.Request, logger *slog.Logger) (string, int64, bool) {
	gameID := r.PathValue("gameID")
	if gameID == "" {
		// User-supplied 4xx — log at Info so the response carries the
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
// surface — unlisted is link-only and private is gated per-request at
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

		var res quizzesResponse = make([]quizResponse, 0, len(quizzes))
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
func canReadQuiz(w http.ResponseWriter, r *http.Request, qz *quiz.Quiz) bool {
	if qz.Visibility != quiz.VisibilityPrivate {
		return true
	}
	p, ok := auth.PlayerFromContext(r.Context())
	if !ok || p.IsAnonymous() {
		http.NotFound(w, r)

		return false
	}

	return true
}

// gateQuizRead loads the quiz by ID via the game service's quiz store
// proxy and applies canReadQuiz so the leaderboard, leaderboard-stream,
// my-game, and create-game handlers can reject access without
// duplicating the load + check + 404 dance.
func gateQuizRead(
	w http.ResponseWriter, r *http.Request,
	logger *slog.Logger, service *game.Service, quizID int64,
) bool {
	qz, err := service.GetQuiz(r.Context(), quizID)
	if err != nil {
		if errors.Is(err, quiz.ErrQuizNotFound) {
			http.NotFound(w, r)

			return false
		}
		writeInternalError(w, r, logger, "error retrieving quiz for visibility gate", err)

		return false
	}

	return canReadQuiz(w, r, qz)
}

// HandleQuizGet returns a single quiz with its questions and options.
func HandleQuizGet(logger *slog.Logger, quizStore quiz.Store) http.Handler {
	type optionResponse struct {
		ID   int64  `json:"id"`
		Text string `json:"text"`
	}

	type questionResponse struct {
		ID       int64            `json:"id"`
		Text     string           `json:"text"`
		ImageURL string           `json:"imageUrl"`
		Position int              `json:"position"`
		Options  []optionResponse `json:"options"`
	}

	type quizResponse struct {
		ID          int64              `json:"id"`
		Title       string             `json:"title"`
		Slug        string             `json:"slug"`
		Description string             `json:"description"`
		CreatedAt   time.Time          `json:"createdAt"`
		Questions   []questionResponse `json:"questions"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromSlugPath(w, r, logger, "slugID")
		if !ok {
			return
		}

		qz, err := quizStore.GetQuiz(r.Context(), quizID)
		if err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) {
				http.NotFound(w, r)

				return
			}
			writeInternalError(w, r, logger, "error retrieving quiz from store", err)

			return
		}
		if !canReadQuiz(w, r, qz) {
			return
		}

		questions := make([]questionResponse, 0, len(qz.Questions))
		for _, qs := range qz.Questions {
			opts := make([]optionResponse, 0, len(qs.Options))
			for _, o := range qs.Options {
				opts = append(opts, optionResponse{ID: o.ID, Text: o.Text})
			}
			questions = append(questions, questionResponse{
				ID:       qs.ID,
				Text:     qs.Text,
				ImageURL: qs.ImageURL,
				Position: qs.Position,
				Options:  opts,
			})
		}

		res := quizResponse{
			ID:          qz.ID,
			Title:       qz.Title,
			Slug:        qz.Slug,
			Description: qz.Description,
			CreatedAt:   qz.CreatedAt,
			Questions:   questions,
		}

		err = handlers.EncodeJSON(w, http.StatusOK, res)
		if err != nil {
			logger.ErrorContext(r.Context(), "error encoding quizResponse", slog.Any("err", err))

			return
		}
	})
}

// leaderboardLimit caps the number of rows the REST + SSE leaderboards
// return. The current player's standing — if they're outside the top
// N — is carried separately on currentPlayer below (#181).
const leaderboardLimit = 10

// quizLeaderboardEntryResponse is one row of the leaderboard wire shape.
// Declared at package scope so both HandleQuizLeaderboard and
// HandleQuizLeaderboardStream can build it.
//
// InProgress is true when the player is still mid-quiz: Score may be a
// running partial total (#244) or zero if the player has clicked Start
// but not yet submitted their first answer (#335). Picked as the wire
// name instead of Completed so the client only has to branch on a
// positive signal to render the badge.
type quizLeaderboardEntryResponse struct {
	PlayerID        int64  `json:"playerId"`
	Username        string `json:"username"`
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
		Username:        e.Username,
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
// body has been written — the SSE handler uses this for the initial
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
// are broken by ascending username for a stable order. IsCurrentPlayer is set
// on the entry that matches the authenticated player on the request context
// so the client can highlight that row.
//
// The response also carries a currentPlayer field with the requesting
// player's rank and score, populated even when the player landed outside
// the truncated top-N — so callers can show an off-leaderboard standing
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
	w        http.ResponseWriter
	rc       *http.ResponseController
	logger   *slog.Logger
	service  *game.Service
	quizID   int64
	playerID int64
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
// frame is `:\n\n` — comment lines start with a colon and the spec
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

// leaderboardHeartbeatInterval is how often the SSE handler emits a
// no-op comment frame to keep the connection alive when the hub is
// quiet. The HTTP server's WriteTimeout no longer kills the response
// (the handler clears its own write deadline), so this only exists as
// insurance against intermediate proxy / NAT / mobile-carrier idle
// timeouts that aren't visible during local-dev testing — nginx
// defaults to 60s, HAProxy ~50s, mobile NATs sometimes 30s. 25s lands
// comfortably inside all of those without the keep-alive cost of a
// 10s tick.
const leaderboardHeartbeatInterval = 25 * time.Second

// HandleQuizLeaderboardStream pushes leaderboard snapshots over SSE.
// On every hub tick the handler re-fetches and emits one full snapshot,
// not a delta - so a slow client coalesces multiple commits into one
// repaint via the per-subscriber buffer of 1.
func HandleQuizLeaderboardStream(
	logger *slog.Logger, service *game.Service, hub *leaderboard.Hub,
) http.Handler {
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
		// HTTP status codes — we log and end the stream there.
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
		// — every heartbeat after 10s fails and the loop exits, which
		// shows up as a 10.003s stream that EventSource has to
		// reconnect. Zero disables the per-request deadline. The
		// underlying TCP connection stays governed by OS keepalives
		// and the request context (cancelled on client disconnect).
		if err := rc.SetWriteDeadline(time.Time{}); err != nil {
			logger.WarnContext(ctx, "could not clear SSE write deadline", slog.Any("err", err))
		}

		streamer := &leaderboardStreamer{
			w:        w,
			rc:       rc,
			logger:   logger,
			service:  service,
			quizID:   quizID,
			playerID: player.ID,
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
// exits — the client will reconnect via EventSource and re-run the
// initial-snapshot path, which can surface the error cleanly.
//
// The heartbeat ticker emits a no-op SSE comment frame every
// leaderboardHeartbeatInterval to keep the connection warm; without
// it Firefox closes idle streams after ~30s with NS_ERROR_PARTIAL_TRANSFER.
func (s *leaderboardStreamer) run(ctx context.Context, events <-chan struct{}) {
	heartbeat := time.NewTicker(leaderboardHeartbeatInterval)
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
			logger.ErrorContext(r.Context(), "error encoding quizzesResponse", slog.Any("err", err))

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

// shuffleOptionsSeed derives a deterministic uint64 seed from a game ID
// and question ID. The shuffle of the option buttons (#297) is stable
// per (game, question) so a player who reloads mid-question sees the
// same order they did before — preventing both confusion and a
// deliberate "re-roll the layout" by refreshing. Different games on
// the same question see different orders because the gameID dominates
// the hash, so position-memorisation across players doesn't help
// either. FNV-64a is fast, deterministic, and well-distributed enough
// for a 4-element shuffle; no cryptographic strength is needed because
// the order is observable anyway once the question is rendered.
func shuffleOptionsSeed(gameID string, questionID int64) uint64 {
	h := fnv.New64a()
	// hash.Hash.Write never returns an error.
	_, _ = h.Write([]byte(gameID))
	_, _ = h.Write([]byte{'/'})
	// binary.Write into a hash.Hash never errors either; fixed byte
	// order keeps the seed identical across hosts, and the value is
	// treated as opaque bits for seeding so sign is irrelevant.
	_ = binary.Write(h, binary.LittleEndian, questionID)

	return h.Sum64()
}

// shuffleByGame shuffles n items in place using a PCG RNG seeded by
// [shuffleOptionsSeed]. Two seed words derived from one hash give the
// PCG enough entropy for the small permutation space (4!=24 here)
// without pulling in a SHA family hash for what is essentially a UI
// concern. swap mirrors the signature [rand.Rand.Shuffle] expects.
func shuffleByGame(gameID string, questionID int64, n int, swap func(i, j int)) {
	seed := shuffleOptionsSeed(gameID, questionID)
	// G404: deterministic-by-design — we need the same (gameID,
	// questionID) to always yield the same permutation across reloads
	// and process restarts. crypto/rand cannot do that because it
	// doesn't accept a seed. No secret protection is at stake; the
	// player sees the resulting order anyway.
	rng := rand.New(rand.NewPCG(seed, ^seed)) //nolint:gosec // deterministic shuffle, not a security boundary
	rng.Shuffle(n, swap)
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
	Type      string               `json:"type"`
	ID        int64                `json:"id"`
	Text      string               `json:"text"`
	ImageURL  string               `json:"imageUrl"`
	Options   []nextOptionResponse `json:"options"`
	StartedAt time.Time            `json:"startedAt"`
	ExpiredAt time.Time            `json:"expiredAt"`
	ServerNow time.Time            `json:"serverNow"`
	Position  int                  `json:"position"`
	Total     int                  `json:"total"`
}

// nextBreakResponse is the wire shape for the `type=break` /next
// variant. Breaks have no countdown so StartedAt/ExpiredAt are dropped
// entirely; Score carries the player's running total for the break
// screen, Total keeps the HUD chip rendering across the break (#167).
type nextBreakResponse struct {
	Type      string    `json:"type"`
	ID        int64     `json:"id"`
	Text      string    `json:"text"`
	Score     int       `json:"score"`
	ServerNow time.Time `json:"serverNow"`
	Total     int       `json:"total"`
}

// HandleQuestionNext returns the next item in the play sequence as a
// tagged union (`type: "question"` | `"break"`). Total counts quiz
// questions, not items, so a break does not bump the HUD position
// chip. StartedAt/ExpiredAt are omitted on breaks; Score is omitted on
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

		if item.Type == game.ItemTypeBreak {
			writeBreakItem(w, r, logger, item)

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

// writeBreakItem encodes a break-variant /next response. Split out of
// HandleQuestionNext to keep that handler under the function-length
// limit; the break path has no shuffle / timing arithmetic so the
// helper is a thin field projection.
func writeBreakItem(w http.ResponseWriter, r *http.Request, logger *slog.Logger, item *game.Item) {
	res := nextBreakResponse{
		Type:      string(game.ItemTypeBreak),
		ID:        item.Break.ID,
		Text:      item.Break.Text,
		Score:     item.Score,
		ServerNow: time.Now().UTC(),
		Total:     item.Total,
	}
	if err := handlers.EncodeJSON(w, http.StatusOK, res); err != nil {
		logger.ErrorContext(r.Context(), "error encoding break item", slog.Any("err", err))
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
	shuffleByGame(gameID, gq.QuestionID, len(resOptions), func(i, j int) {
		resOptions[i], resOptions[j] = resOptions[j], resOptions[i]
	})

	res := nextQuestionResponse{
		Type:      string(game.ItemTypeQuestion),
		ID:        gq.QuizQuestion.ID,
		Text:      gq.QuizQuestion.Text,
		ImageURL:  gq.QuizQuestion.ImageURL,
		Options:   resOptions,
		StartedAt: gq.StartedAt,
		ExpiredAt: gq.ExpiredAt,
		ServerNow: time.Now().UTC(),
		Position:  gq.Position,
		Total:     gq.Total,
	}

	if err := handlers.EncodeJSON(w, http.StatusOK, res); err != nil {
		logger.ErrorContext(r.Context(), "error encoding question item", slog.Any("err", err))
	}
}

// HandleBreakSeen records a break acknowledgement. Idempotent: second
// call returns 204 because the store INSERTs ON CONFLICT DO NOTHING.
// Game-not-found, not-a-participant, and break-not-in-quiz all return
// 404 to keep ids opaque to outsiders.
func HandleBreakSeen(logger *slog.Logger, service *game.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gameID, playerID, ok := gameRequest(w, r, logger)
		if !ok {
			return
		}

		breakID, ok := handlers.ParseIDFromPath(w, r, logger, "breakID")
		if !ok {
			return
		}

		if err := service.MarkBreakSeen(r.Context(), gameID, playerID, breakID); err != nil {
			switch {
			case errors.Is(err, game.ErrGameNotFound), errors.Is(err, quiz.ErrBreakNotFound):
				http.NotFound(w, r)
			default:
				writeInternalError(w, r, logger, "error marking break seen", err)
			}

			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}

// correctOptionIDsFromAnswer extracts the IDs of every option flagged
// correct on the question the player just answered. SubmitAnswer
// populates Answer.Question.QuizQuestion with the full option set so
// this read is local — no extra store round-trip. Returns nil when the
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
//   - ErrGameNotFound / ErrQuestionNotInGame → 404
//   - ErrOptionNotInQuestion → 400
//   - ErrAnswerAlreadyRecorded → 409 (double-tap / retry; #353)
//   - anything else → 500 via writeInternalError
func writeSubmitAnswerError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, game.ErrGameNotFound), errors.Is(err, game.ErrQuestionNotInGame):
		http.NotFound(w, r)
	case errors.Is(err, game.ErrOptionNotInQuestion):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, game.ErrAnswerAlreadyRecorded):
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

// playerResponse is the JSON shape for GET and PATCH /api/players/me.
// isAuthenticated is distinct from !isAnonymous: an OAuth-only player
// has no password_hash (isAnonymous == true) but IS known via their
// linked identity (isAuthenticated == true). The client gates the
// claim-name modal on isAuthenticated.
type playerResponse struct {
	ID              int64  `json:"id"`
	Username        string `json:"username"`
	IsAnonymous     bool   `json:"isAnonymous"`
	HasCustomName   bool   `json:"hasCustomName"`
	IsAuthenticated bool   `json:"isAuthenticated"`
}

// newPlayerResponse projects an auth.Player onto the wire format.
func newPlayerResponse(p *auth.Player) playerResponse {
	return playerResponse{
		ID:              p.ID,
		Username:        p.Username,
		IsAnonymous:     p.IsAnonymous(),
		HasCustomName:   p.HasCustomName(),
		IsAuthenticated: p.IsAuthenticated(),
	}
}

// HandlePlayerGetMe returns a handler for GET /api/players/me that reports
// the calling player's id, username, whether they are still anonymous
// (no password_hash set), and whether they have explicitly picked a
// display name. hasCustomName and isAnonymous are deliberately
// independent concepts: a registered user with a password is never
// anonymous, but a claimed-but-passwordless visitor still is — callers
// that care about "did this player choose this name" should look at
// hasCustomName, not isAnonymous. The username is shown verbatim so a
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
// username-taken and already-claimed-via-register; the distinct
// messages let the client tell them apart.
func HandlePlayerClaimName(
	logger *slog.Logger, players auth.PlayerStore, gameService *game.Service,
) http.Handler {
	type claimNameRequest struct {
		Username string `json:"username"`
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
		if strings.TrimSpace(req.Username) == "" {
			http.Error(w, "username is required", http.StatusBadRequest)

			return
		}

		updated, err := players.UpdatePlayerUsername(ctx, current.ID, req.Username)
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrUsernameTaken):
				writeClaimNameError(w, r, logger,
					http.StatusConflict, "username_taken", "username already taken")
			case errors.Is(err, auth.ErrPlayerNotAnonymous):
				// #289: distinct code so the JS can tell "name in use
				// by someone else" from "this account already has a
				// claimed name". The latter is a state-drift signal —
				// the client should re-fetch /me and dismiss the
				// modal, not show "name is taken".
				writeClaimNameError(w, r, logger,
					http.StatusConflict, "already_claimed", "username already set for this account")
			case errors.Is(err, auth.ErrUsernameEmpty):
				writeClaimNameError(w, r, logger,
					http.StatusBadRequest, "username_required", "username is required")
			default:
				writeInternalError(w, r, logger, "error updating player username", err)
			}

			return
		}

		// Republish leaderboard ticks on every quiz the renamed player
		// appears on so other clients' SSE streams pick up the new
		// display name without waiting for the next answer-submit
		// publish. Best-effort: a failure here logs but does not fail
		// the HTTP response — the rename itself already succeeded.
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
				// User-supplied bad ID — Info, not Error (#369).
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
		var winner string
		if results.Winner != 0 {
			winner = strconv.FormatInt(results.Winner, 10)
		}
		res := resultsResponse{
			GameID:       gameID,
			Winner:       winner,
			PlayerScores: psr,
		}

		err = handlers.EncodeJSON(w, http.StatusOK, res)
		if err != nil {
			logger.ErrorContext(r.Context(), "error encoding questionResponse", slog.Any("err", err))

			return
		}
	})
}
