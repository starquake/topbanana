// Package clientapi provides HTTP handlers for the API used by the game client.
package clientapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/httputil"
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

		err = httputil.EncodeJSON(w, http.StatusOK, res)
		if err != nil {
			logger.ErrorContext(r.Context(), "error encoding quizzesResponse", slog.Any("err", err))

			return
		}
	})
}

// HandleCreateGame creates a new game.
// It first checks if the quiz exists, then creates the game and participant, and finally starts the game.
// Returns the ID of the created game.
// Returns 201 if the game was created successfully.
// Returns 400 if the request body is invalid.
// Returns 404 if the quiz or game does not exist.
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
		req, err = httputil.DecodeJSON[createGameRequest](r)
		if err != nil {
			logger.ErrorContext(ctx, "error decoding createGameRequest", slog.Any("err", err))
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		// TODO: Replace with real PlayerID
		g, err := service.CreateGame(ctx, req.QuizID, 1)
		if err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) {
				http.NotFound(w, r)

				return
			}
			logger.ErrorContext(ctx, "error creating game", slog.Any("err", err))
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}
		res := createGameResponse{ID: g.ID}

		w.Header().Set("Location", fmt.Sprintf("/play/game/%v", g.ID))
		err = httputil.EncodeJSON(w, http.StatusCreated, res)
		if err != nil {
			logger.ErrorContext(r.Context(), "error encoding quizzesResponse", slog.Any("err", err))

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
		ID      int64            `json:"id"`
		Text    string           `json:"text"`
		Options []optionResponse `json:"options"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		var gameID string
		if gameID = r.PathValue("gameID"); gameID == "" {
			logger.ErrorContext(r.Context(), "missing GameID in request path")
			http.Error(w, "missing GameID in request path", http.StatusBadRequest)

			return
		}

		qs, err := service.GetNextQuestion(r.Context(), gameID)
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

		resOptions := make([]optionResponse, len(qs.Options))
		for i, o := range qs.Options {
			resOption := optionResponse{
				ID:   o.ID,
				Text: o.Text,
			}
			resOptions[i] = resOption
		}

		res := questionResponse{
			ID:      qs.ID,
			Text:    qs.Text,
			Options: resOptions,
		}

		err = httputil.EncodeJSON(w, http.StatusOK, res)
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

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		var gameID string
		if gameID = r.PathValue("gameID"); gameID == "" {
			http.Error(w, "missing gameID", http.StatusBadRequest)

			return
		}

		questionID, ok := httputil.ParseIDFromPath(w, r, logger, "questionID")
		if !ok {
			return
		}

		req, err := httputil.DecodeJSON[answerRequest](r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		// TODO: Replace with real PlayerID
		err = service.SubmitAnswer(r.Context(), gameID, 1, questionID, req.OptionID)
		if err != nil {
			logger.ErrorContext(r.Context(), "error submitting answer", slog.Any("err", err))
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}
