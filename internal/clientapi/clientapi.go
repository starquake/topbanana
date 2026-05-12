// Package clientapi provides HTTP handlers for the API used by the game client.
package clientapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/quiz"
)

// HandleQuizList returns a list of quizzes.
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

		quizzes, err := quizStore.ListQuizzes(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "error retrieving quizzes from store", slog.Any("err", err))
			http.Error(w, err.Error(), http.StatusInternalServerError)

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
			logger.ErrorContext(r.Context(), "error retrieving quiz from store", slog.Any("err", err))
			http.Error(w, err.Error(), http.StatusInternalServerError)

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

// HandleQuizLeaderboard returns the top scoring players for the given quiz.
// Each player appears at most once, with their total score for the quiz; ties
// are broken by ascending username for a stable order. IsCurrentPlayer is set
// on the entry that matches the authenticated player on the request context
// so the client can highlight that row.
//
// The response also carries a currentPlayer field with the requesting
// player's rank and score, populated even when the player landed outside
// the truncated top-N. Frontend uses this to render an off-leaderboard
// "Your score" card — see #181.
func HandleQuizLeaderboard(logger *slog.Logger, service *game.Service) http.Handler {
	const leaderboardLimit = 10

	type entryResponse struct {
		PlayerID        int64  `json:"playerId"`
		Username        string `json:"username"`
		Score           int    `json:"score"`
		Rank            int    `json:"rank"`
		IsCurrentPlayer bool   `json:"isCurrentPlayer"`
	}

	type leaderboardResponse struct {
		QuizID        int64           `json:"quizId"`
		Entries       []entryResponse `json:"entries"`
		CurrentPlayer *entryResponse  `json:"currentPlayer"`
	}

	toEntryResponse := func(e game.LeaderboardEntry) entryResponse {
		return entryResponse{
			PlayerID:        e.PlayerID,
			Username:        e.Username,
			Score:           e.Score,
			Rank:            e.Rank,
			IsCurrentPlayer: e.IsCurrentPlayer,
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		quizID, ok := handlers.ParseIDFromSlugPath(w, r, logger, "slugID")
		if !ok {
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

		result, err := service.GetQuizLeaderboard(ctx, quizID, player.ID, leaderboardLimit)
		if err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) {
				http.NotFound(w, r)

				return
			}
			logger.ErrorContext(ctx, "error retrieving quiz leaderboard", slog.Any("err", err))
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		respEntries := make([]entryResponse, 0, len(result.Entries))
		for _, e := range result.Entries {
			respEntries = append(respEntries, toEntryResponse(e))
		}

		res := leaderboardResponse{QuizID: quizID, Entries: respEntries}
		if result.CurrentPlayer != nil {
			cp := toEntryResponse(*result.CurrentPlayer)
			res.CurrentPlayer = &cp
		}

		if err = handlers.EncodeJSON(w, http.StatusOK, res); err != nil {
			logger.ErrorContext(ctx, "error encoding leaderboardResponse", slog.Any("err", err))

			return
		}
	})
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
		req, err = handlers.DecodeJSON[createGameRequest](r)
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
			logger.ErrorContext(ctx, "error creating game", slog.Any("err", err))
			http.Error(w, err.Error(), http.StatusInternalServerError)

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

// HandleGameForQuiz returns the requesting player's game for the given quiz,
// if any. The frontend uses this as the resume probe before deciding whether
// to POST /api/games for a fresh attempt.
//
// Returns 200 with {"gameId":..., "completed":...} when a game exists.
// Returns 404 when the player has no game for the quiz, or when the quiz
// itself does not exist (consistent with other ErrQuizNotFound mappings).
// Returns 500 when EnsurePlayer hasn't populated the player on the context
// (a wiring bug rather than a user-facing condition).
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
			logger.ErrorContext(ctx, "error retrieving game for quiz", slog.Any("err", err))
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		res := gameForQuizResponse{GameID: g.ID, Completed: g.IsCompleted()}

		if err = handlers.EncodeJSON(w, http.StatusOK, res); err != nil {
			logger.ErrorContext(ctx, "error encoding gameForQuizResponse", slog.Any("err", err))

			return
		}
	})
}

// HandleQuestionNext returns an HTTP handler for retrieving the next question of a game based on its ID and question ID.
// It validates request parameters, fetches the game and question data from the provided stores, and encodes the response.
func HandleQuestionNext(logger *slog.Logger, service *game.Service) http.Handler {
	type optionResponse struct {
		ID   int64  `json:"id"`
		Text string `json:"text"`
	}

	type questionResponse struct {
		ID        int64            `json:"id"`
		Text      string           `json:"text"`
		ImageURL  string           `json:"imageUrl"`
		Options   []optionResponse `json:"options"`
		StartedAt time.Time        `json:"startedAt"`
		ExpiredAt time.Time        `json:"expiredAt"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		var gameID string
		if gameID = r.PathValue("gameID"); gameID == "" {
			logger.ErrorContext(r.Context(), "missing GameID in request path")
			http.Error(w, "missing GameID in request path", http.StatusBadRequest)

			return
		}

		gq, err := service.GetNextQuestion(r.Context(), gameID)
		if err != nil {
			if errors.Is(err, game.ErrGameNotFound) {
				http.Error(w, err.Error(), http.StatusNotFound)

				return
			}
			if errors.Is(err, quiz.ErrQuizNotFound) {
				http.Error(w, err.Error(), http.StatusNotFound)

				return
			}
			if errors.Is(err, game.ErrNoMoreQuestions) {
				http.Error(w, err.Error(), http.StatusNotFound)

				return
			}
			logger.ErrorContext(r.Context(), "error retrieving next question", slog.Any("err", err))
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		resOptions := make([]optionResponse, len(gq.QuizQuestion.Options))
		for i, o := range gq.QuizQuestion.Options {
			resOption := optionResponse{
				ID:   o.ID,
				Text: o.Text,
			}
			resOptions[i] = resOption
		}

		res := questionResponse{
			ID:        gq.QuizQuestion.ID,
			Text:      gq.QuizQuestion.Text,
			ImageURL:  gq.QuizQuestion.ImageURL,
			Options:   resOptions,
			StartedAt: gq.StartedAt,
			ExpiredAt: gq.ExpiredAt,
		}

		err = handlers.EncodeJSON(w, http.StatusOK, res)
		if err != nil {
			logger.ErrorContext(r.Context(), "error encoding questionResponse", slog.Any("err", err))

			return
		}
	})
}

// HandleAnswerPost handles the submission of an answer for a game question.
// It decodes the request body, extracts game and question IDs from the path,
// and uses the game service to submit the answer.
func HandleAnswerPost(logger *slog.Logger, service *game.Service) http.Handler {
	type answerRequest struct {
		OptionID int64 `json:"optionId"`
	}

	type answerResponse struct {
		Correct bool `json:"correct"`
		Score   int  `json:"score"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		var gameID string
		if gameID = r.PathValue("gameID"); gameID == "" {
			http.Error(w, "missing gameID", http.StatusBadRequest)

			return
		}

		questionID, ok := handlers.ParseIDFromPath(w, r, logger, "questionID")
		if !ok {
			return
		}

		req, err := handlers.DecodeJSON[answerRequest](r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		player, ok := auth.PlayerFromContext(r.Context())
		if !ok {
			logger.ErrorContext(r.Context(), "missing player on context for submit answer")
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		a, err := service.SubmitAnswer(r.Context(), gameID, player.ID, questionID, req.OptionID)
		if err != nil {
			if errors.Is(err, game.ErrGameNotFound) || errors.Is(err, game.ErrQuestionNotInGame) {
				http.Error(w, err.Error(), http.StatusNotFound)

				return
			}
			if errors.Is(err, game.ErrOptionNotInQuestion) {
				http.Error(w, err.Error(), http.StatusBadRequest)

				return
			}
			logger.ErrorContext(r.Context(), "error submitting answer", slog.Any("err", err))
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		score := service.CalculateScore(r.Context(), a)

		res := answerResponse{
			Correct: a.Option.Correct,
			Score:   score,
		}

		err = handlers.EncodeJSON(w, http.StatusOK, res)
		if err != nil {
			logger.ErrorContext(r.Context(), "error encoding answerResponse", slog.Any("err", err))

			return
		}
	})
}

// playerResponse is the JSON shape returned by both GET and PATCH
// /api/players/me. Shared so the two handlers cannot drift out of sync
// when a field is added — the frontend's PlayerService.getMe() and
// .claimName() decode into the same model.
type playerResponse struct {
	ID            int64  `json:"id"`
	Username      string `json:"username"`
	IsAnonymous   bool   `json:"isAnonymous"`
	HasCustomName bool   `json:"hasCustomName"`
}

// newPlayerResponse projects an auth.Player onto the wire format.
func newPlayerResponse(p *auth.Player) playerResponse {
	return playerResponse{
		ID:            p.ID,
		Username:      p.Username,
		IsAnonymous:   p.IsAnonymous(),
		HasCustomName: p.HasCustomName(),
	}
}

// HandlePlayerGetMe returns a handler for GET /api/players/me that reports
// the calling player's id, username, whether they are still anonymous
// (no password_hash set), and whether they have explicitly picked a
// display name. The frontend uses hasCustomName to gate the "claim your
// name" affordances; isAnonymous remains as a distinct, credential-level
// concept (a registered user with a password is never anonymous, but a
// claimed-but-passwordless visitor still is). The username is shown
// verbatim so a fresh petname can be displayed as-is until the player
// renames.
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

// HandlePlayerClaimName returns a handler for PATCH /api/players/me that
// renames the calling player's row in place. It targets the score-claim flow
// for anonymous visitors who want to pick a friendlier display name without
// going through the full register form: the player keeps the same row (and
// session cookie) and stays anonymous afterwards.
//
// Behaviour:
//   - 200 with the updated player JSON on success.
//   - 400 when the request body is malformed or the username is empty.
//   - 401 when EnsurePlayer has not populated a player on the context.
//     This is a wiring bug rather than a user-facing condition, but the
//     401 keeps the contract honest if the route is reused elsewhere.
//   - 409 when the username is already in use by another row, OR when the
//     player has already claimed a non-anonymous identity (password_hash
//     is set). Both are conflict states; the distinct error messages let
//     the client distinguish them.
//   - 500 on any other error.
func HandlePlayerClaimName(logger *slog.Logger, players auth.PlayerStore) http.Handler {
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

		req, err := handlers.DecodeJSON[claimNameRequest](r)
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
				http.Error(w, "username already taken", http.StatusConflict)
			case errors.Is(err, auth.ErrPlayerNotAnonymous):
				http.Error(w, "username already set for this account", http.StatusConflict)
			case errors.Is(err, auth.ErrUsernameEmpty):
				http.Error(w, "username is required", http.StatusBadRequest)
			default:
				logger.ErrorContext(ctx, "error updating player username", slog.Any("err", err))
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}

			return
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
		var err error
		var gameID string
		if gameID = r.PathValue("gameID"); gameID == "" {
			http.Error(w, "missing gameID", http.StatusBadRequest)

			return
		}
		results, err := service.GetResults(r.Context(), gameID)
		if err != nil {
			if errors.Is(err, game.ErrGameNotFound) {
				logger.ErrorContext(r.Context(), "game not found", slog.Any("err", err))
				http.Error(w, err.Error(), http.StatusNotFound)

				return
			}
			logger.ErrorContext(r.Context(), "error retrieving game results", slog.Any("err", err))
			http.Error(w, err.Error(), http.StatusInternalServerError)

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
