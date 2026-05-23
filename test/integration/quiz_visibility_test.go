//go:build integration

package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
)

// TestQuizVisibility_Integration pins #103 end-to-end: a private quiz
// disappears from /api/quizzes for everyone, is reachable on its
// direct endpoints only by an authenticated player, and rejects a
// start-game POST from an anonymous visitor. Unlisted is not on the
// public list but stays reachable on its direct endpoints.
func TestQuizVisibility_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	stores := setup.Stores

	publicQz := &quiz.Quiz{
		Title:             "Public Quiz",
		Slug:              "public-quiz",
		Description:       "Visible everywhere.",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Questions: []*quiz.Question{
			{Text: "Q", Position: 1, Options: []*quiz.Option{{Text: "yes", Correct: true}, {Text: "no"}}},
		},
	}
	if err := stores.Quizzes.CreateQuiz(ctx, publicQz); err != nil {
		t.Fatalf("CreateQuiz public err = %v", err)
	}

	unlistedQz := &quiz.Quiz{
		Title:             "Unlisted Quiz",
		Slug:              "unlisted-quiz",
		Description:       "Link-only.",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityUnlisted,
		Questions: []*quiz.Question{
			{Text: "Q", Position: 1, Options: []*quiz.Option{{Text: "yes", Correct: true}, {Text: "no"}}},
		},
	}
	if err := stores.Quizzes.CreateQuiz(ctx, unlistedQz); err != nil {
		t.Fatalf("CreateQuiz unlisted err = %v", err)
	}

	privateQz := &quiz.Quiz{
		Title:             "Private Quiz",
		Slug:              "private-quiz",
		Description:       "Members only.",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPrivate,
		Questions: []*quiz.Question{
			{Text: "Q", Position: 1, Options: []*quiz.Option{{Text: "yes", Correct: true}, {Text: "no"}}},
		},
	}
	if err := stores.Quizzes.CreateQuiz(ctx, privateQz); err != nil {
		t.Fatalf("CreateQuiz private err = %v", err)
	}

	// Anonymous client. EnsurePlayer mints a session row on first
	// /api/players/me round-trip; reusing a jar across the subtests
	// keeps the same auto-petname player so the visibility gate sees a
	// consistent caller.
	anonJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v", err)
	}
	anonClient := &http.Client{Jar: anonJar}

	t.Run("public list omits unlisted and private quizzes", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, anonClient, baseURL+"/api/quizzes")
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		var quizzes []struct {
			Title string `json:"title"`
		}
		if derr := json.NewDecoder(resp.Body).Decode(&quizzes); derr != nil {
			t.Fatalf("decode: %v", derr)
		}
		seen := map[string]bool{}
		for _, q := range quizzes {
			seen[q.Title] = true
		}
		if !seen["Public Quiz"] {
			t.Error("public list missing the public quiz")
		}
		if seen["Unlisted Quiz"] {
			t.Error("public list surfaced the unlisted quiz")
		}
		if seen["Private Quiz"] {
			t.Error("public list surfaced the private quiz")
		}
	})

	t.Run("anonymous can fetch unlisted by direct link", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(
			ctx,
			t,
			anonClient,
			fmt.Sprintf("%s/api/quizzes/%s-%d", baseURL, unlistedQz.Slug, unlistedQz.ID),
		)
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("anonymous gets 404 fetching private quiz directly", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, anonClient, fmt.Sprintf("%s/api/quizzes/%s-%d", baseURL, privateQz.Slug, privateQz.ID))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("anonymous gets 404 starting a game on a private quiz", func(t *testing.T) {
		t.Parallel()
		body := fmt.Sprintf(`{"quizId": %d}`, privateQz.ID)
		resp := httpPostJSON(ctx, t, anonClient, baseURL+"/api/games", body)
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	// Logged-in player can reach the private quiz. Register first so
	// the player has a credentialled (non-anonymous) session.
	authJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v", err)
	}
	authClient := &http.Client{
		Jar: authJar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	registerPlayer(ctx, t, authClient, baseURL, "visibility-resident", "visibility-pass-123")
	// Drop the redirect interceptor so subsequent GETs follow 303
	// redirects normally (the registration handler 303s on success).
	authClient.CheckRedirect = nil

	t.Run("logged-in player can fetch private quiz directly", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, authClient, fmt.Sprintf("%s/api/quizzes/%s-%d", baseURL, privateQz.Slug, privateQz.ID))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		var body struct {
			Title string `json:"title"`
		}
		if derr := json.NewDecoder(resp.Body).Decode(&body); derr != nil {
			t.Fatalf("decode: %v", derr)
		}
		if got, want := body.Title, "Private Quiz"; !strings.Contains(got, want) {
			t.Errorf("title = %q, should contain %q", got, want)
		}
	})
}
