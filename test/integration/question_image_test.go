package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// quizGetImageRes decodes only the per-question image field off the solo
// data endpoint (GET /api/quizzes/{slugID}).
type quizGetImageRes struct {
	Questions []questionImageRes `json:"questions"`
}

// questionImageRes is one question's text + image field, shared by the solo
// data and /next decode targets.
type questionImageRes struct {
	Text     string `json:"text"`
	ImageURL string `json:"imageUrl"`
}

// createGameRes decodes the POST /api/games response.
type createGameRes struct {
	ID string `json:"id"`
}

// sessionImageRes decodes only the current question's image field off the live
// session state DTO (GET /api/sessions/{code}/state).
type sessionImageRes struct {
	Question questionImageRes `json:"question"`
}

// attachMediaToQuestion seeds a media row for the question's quiz and points the
// question at it via MediaID, returning the new media id. The row's files are
// not written: the play endpoints only project /media/<id> from the id, so the
// wire-field assertions never fetch the bytes.
func attachMediaToQuestion(
	ctx context.Context, t *testing.T, stores *store.Stores, quizID, questionID int64,
) int64 {
	t.Helper()
	row, err := stores.Media.CreateMedia(ctx, &media.Media{
		QuizID:            quizID,
		Type:              media.TypeImage,
		MIME:              "image/jpeg",
		Width:             200,
		Height:            120,
		SizeBytes:         1234,
		SHA256:            fmt.Sprintf("sha-%d", questionID),
		CreatedByPlayerID: seededAdminID,
	})
	if err != nil {
		t.Fatalf("CreateMedia err = %v, want nil", err)
	}
	// CreateMedia inserts not-ready (#992); a question only attaches a library
	// image, which is ready, so flip it to match production.
	if err = stores.Media.MarkMediaReady(ctx, row.ID); err != nil {
		t.Fatalf("MarkMediaReady err = %v, want nil", err)
	}

	qs, err := stores.Quizzes.GetQuestion(ctx, questionID)
	if err != nil {
		t.Fatalf("GetQuestion err = %v, want nil", err)
	}
	qs.MediaID = &row.ID
	if err := stores.Quizzes.UpdateQuestion(ctx, qs); err != nil {
		t.Fatalf("UpdateQuestion err = %v, want nil", err)
	}

	return row.ID
}

// createSoloGame posts POST /api/games for the quiz on the client and returns
// the decoded response, closing the body before returning.
func createSoloGame(
	ctx context.Context, t *testing.T, client *http.Client, baseURL string, quizID int64,
) createGameRes {
	t.Helper()
	resp := httpPostJSON(ctx, t, client, baseURL+"/api/games", fmt.Sprintf(`{"quizId": %d}`, quizID))
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusCreated; got != want {
		t.Fatalf("create game status = %d, want %d", got, want)
	}
	var created createGameRes
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create game: %v", err)
	}

	return created
}

// TestQuestionImage_SoloWire pins that the solo play endpoints surface a
// question's attached image as imageUrl = /media/<id> and omit the field when
// the question has none. Covers both the bulk data endpoint
// (GET /api/quizzes/{slugID}) and the per-question /next endpoint.
func TestQuestionImage_SoloWire(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	stores := setup.Stores

	qz := &quiz.Quiz{
		Title:             "Image Solo Quiz",
		Slug:              "image-solo-quiz",
		Description:       "solo image fixture",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeSolo,
		Questions: []*quiz.Question{
			{Text: "With image", Position: 1, Options: []*quiz.Option{{Text: "A", Correct: true}, {Text: "B"}}},
			{Text: "No image", Position: 2, Options: []*quiz.Option{{Text: "C", Correct: true}, {Text: "D"}}},
		},
	}
	if err := stores.Quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	mediaID := attachMediaToQuestion(ctx, t, stores, qz.ID, qz.Questions[0].ID)
	wantURL := fmt.Sprintf("/media/%d", mediaID)
	slugID := fmt.Sprintf("%s-%d", qz.Slug, qz.ID)

	t.Run("data endpoint carries imageUrl", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, newAnonClient(t), fmt.Sprintf("%s/api/quizzes/%s", baseURL, slugID))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("quiz get status = %d, want %d", got, want)
		}
		var body quizGetImageRes
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode quiz get: %v", err)
		}
		if got, want := len(body.Questions), 2; got != want {
			t.Fatalf("questions = %d, want %d", got, want)
		}
		if got, want := body.Questions[0].ImageURL, wantURL; got != want {
			t.Errorf("question[0].imageUrl = %q, want %q", got, want)
		}
		if got := body.Questions[1].ImageURL; got != "" {
			t.Errorf("question[1].imageUrl = %q, want empty (no image attached)", got)
		}
	})

	t.Run("next endpoint carries imageUrl", func(t *testing.T) {
		t.Parallel()
		player := newAnonClient(t)

		created := createSoloGame(ctx, t, player, baseURL, qz.ID)

		next := httpGet(ctx, t, player, fmt.Sprintf("%s/api/games/%s/questions/next", baseURL, created.ID))
		defer closeBody(t, next.Body)
		if got, want := next.StatusCode, http.StatusOK; got != want {
			t.Fatalf("next status = %d, want %d", got, want)
		}
		var q questionImageRes
		if err := json.NewDecoder(next.Body).Decode(&q); err != nil {
			t.Fatalf("decode next: %v", err)
		}
		// The first issued question is the one carrying the image.
		if got, want := q.ImageURL, wantURL; got != want {
			t.Errorf("next imageUrl = %q, want %q", got, want)
		}
	})
}

// TestQuestionImage_LiveWire pins that the live session state DTO surfaces the
// current question's attached image as imageUrl = /media/<id> once the runner
// has issued the question.
func TestQuestionImage_LiveWire(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegrationWithEnv(t, map[string]string{
		"SESSION_RUNNER_BEAT": "250ms",
		"REVEAL_DELAY":        "500ms",
	})
	baseURL := setup.BaseURL

	qz := seedRunnerLiveQuiz(ctx, t, setup.Stores.Quizzes, "image-live-quiz")
	mediaID := attachMediaToQuestion(ctx, t, setup.Stores, qz.ID, qz.Questions[0].ID)
	wantURL := fmt.Sprintf("/media/%d", mediaID)

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "image-live-host", "image-live-pass-123")
	code := createSession(ctx, t, host, baseURL, qz.ID)

	player := newAnonClient(t)
	joinSession(ctx, t, player, baseURL, code, "Imager")

	startSession(ctx, t, host, baseURL, code)

	state := waitForPhase(ctx, t, player, baseURL, code, "question")
	if state.Question == nil {
		t.Fatal("question phase has no question in state")
	}

	// The runner-aware decode target omits imageUrl, so read the field
	// directly off the raw state for this assertion.
	resp := httpGet(ctx, t, player, fmt.Sprintf("%s/api/sessions/%s/state", baseURL, code))
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("state status = %d, want %d", got, want)
	}
	var raw sessionImageRes
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if got, want := raw.Question.ImageURL, wantURL; got != want {
		t.Errorf("session question imageUrl = %q, want %q", got, want)
	}
}
