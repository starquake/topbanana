package integration_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
)

// TestQuizVisibility_Integration pins #103: a private quiz is hidden from
// the list and its gated leaderboard is reachable only by an authed player;
// unlisted is off the list but reachable by link.
func TestQuizVisibility_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	stores := setup.Stores

	publicQz := &quiz.Quiz{
		Title:             "Public Quiz",
		Published:         true,
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
		Published:         true,
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
		Published:         true,
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

	t.Run("anonymous can reach unlisted leaderboard by direct link", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(
			ctx,
			t,
			anonClient,
			fmt.Sprintf("%s/api/quizzes/%s-%d/leaderboard", baseURL, unlistedQz.Slug, unlistedQz.ID),
		)
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("anonymous gets 404 reaching private quiz leaderboard directly", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(
			ctx, t, anonClient,
			fmt.Sprintf("%s/api/quizzes/%s-%d/leaderboard", baseURL, privateQz.Slug, privateQz.ID),
		)
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
	registerVerifyAndSignIn(ctx, t, authClient, baseURL, setup.DBURI, "visibility-resident", "visibility-pass-123")
	// Drop the redirect interceptor so subsequent GETs follow 303
	// redirects normally (the login handler 303s on success).
	authClient.CheckRedirect = nil

	t.Run("logged-in player can reach private quiz leaderboard directly", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(
			ctx, t, authClient,
			fmt.Sprintf("%s/api/quizzes/%s-%d/leaderboard", baseURL, privateQz.Slug, privateQz.ID),
		)
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}

// TestUnlistedQuizSlug_Integration pins that an unlisted quiz is reachable only
// through its own slug: a guessed slug with the right id 404s on every play
// read path and keeps the default share card, while the real link and an admin
// still get through (#1332).
func TestUnlistedQuizSlug_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	admin := registerAdminClient(ctx, t, baseURL, setup.DBURI, "slug-admin")

	unlistedQz := &quiz.Quiz{
		Title:             "Secret Slug Quiz",
		Published:         true,
		Slug:              "secret-slug",
		Description:       "Link-only.",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityUnlisted,
		Questions: []*quiz.Question{
			{Text: "Q", Position: 1, Options: []*quiz.Option{{Text: "yes", Correct: true}, {Text: "no"}}},
		},
	}
	if err := setup.Stores.Quizzes.CreateQuiz(ctx, unlistedQz); err != nil {
		t.Fatalf("CreateQuiz unlisted err = %v", err)
	}
	realSlugID := fmt.Sprintf("%s-%d", unlistedQz.Slug, unlistedQz.ID)
	guessedSlugID := fmt.Sprintf("x-%d", unlistedQz.ID)

	for _, suffix := range []string{"", "/leaderboard", "/leaderboard/stream", "/my-game"} {
		t.Run("guessed slug 404s on /api/quizzes/{slugID}"+suffix, func(t *testing.T) {
			t.Parallel()
			resp := httpGet(ctx, t, newAnonClient(t), baseURL+"/api/quizzes/"+guessedSlugID+suffix)
			defer closeBody(t, resp.Body)
			if got, want := resp.StatusCode, http.StatusNotFound; got != want {
				t.Errorf("GET %s status = %d, want %d", suffix, got, want)
			}
		})
	}

	t.Run("real slug reads metadata", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, newAnonClient(t), baseURL+"/api/quizzes/"+realSlugID)
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("admin reads metadata by a guessed slug", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, admin, baseURL+"/api/quizzes/"+guessedSlugID)
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	for name, body := range map[string]string{
		"missing slug": fmt.Sprintf(`{"quizId": %d}`, unlistedQz.ID),
		"wrong slug":   fmt.Sprintf(`{"quizId": %d, "slug": "x"}`, unlistedQz.ID),
	} {
		t.Run("create game with "+name+" 404s", func(t *testing.T) {
			t.Parallel()
			resp := httpPostJSON(ctx, t, newAnonClient(t), baseURL+"/api/games", body)
			defer closeBody(t, resp.Body)
			if got, want := resp.StatusCode, http.StatusNotFound; got != want {
				t.Errorf("status = %d, want %d", got, want)
			}
		})
	}

	t.Run("create game with the real slug succeeds", func(t *testing.T) {
		t.Parallel()
		body := fmt.Sprintf(`{"quizId": %d, "slug": %q}`, unlistedQz.ID, unlistedQz.Slug)
		resp := httpPostJSON(ctx, t, newAnonClient(t), baseURL+"/api/games", body)
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusCreated; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("share card keeps defaults for a guessed slug", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, newAnonClient(t), baseURL+"/play/"+guessedSlugID)
		defer closeBody(t, resp.Body)
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body err = %v", err)
		}
		if got, want := string(body), unlistedQz.Title; strings.Contains(got, want) {
			t.Errorf("guessed-slug share page contains quiz title %q, want the default card", want)
		}
	})

	t.Run("share card names the quiz for the real slug", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, newAnonClient(t), baseURL+"/play/"+realSlugID)
		defer closeBody(t, resp.Body)
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body err = %v", err)
		}
		if got, want := string(body), unlistedQz.Title; !strings.Contains(got, want) {
			t.Errorf("real-slug share page missing quiz title %q", want)
		}
	})
}

// TestUnpublishedQuizLeaderboard_Integration pins that the leaderboard of a
// draft or live quiz 404s to anyone but its creator or an admin, the same
// answer a missing id gets, so it cannot confirm the quiz exists (#1207).
func TestUnpublishedQuizLeaderboard_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	admin := registerAdminClient(ctx, t, baseURL, setup.DBURI, "draft-lb-admin")

	newQuiz := func(slug, mode string, published bool) *quiz.Quiz {
		qz := &quiz.Quiz{
			Title:             slug,
			Published:         published,
			Slug:              slug,
			CreatedByPlayerID: seededAdminID,
			Visibility:        quiz.VisibilityPublic,
			Mode:              mode,
			Questions: []*quiz.Question{
				{Text: "Q", Position: 1, Options: []*quiz.Option{{Text: "yes", Correct: true}, {Text: "no"}}},
			},
		}
		if err := setup.Stores.Quizzes.CreateQuiz(ctx, qz); err != nil {
			t.Fatalf("CreateQuiz %q err = %v", slug, err)
		}

		return qz
	}
	draftQz := newQuiz("draft-lb", quiz.ModeSolo, false)
	liveQz := newQuiz("live-lb", quiz.ModeLive, true)

	for _, qz := range []*quiz.Quiz{draftQz, liveQz} {
		leaderboardURL := fmt.Sprintf("%s/api/quizzes/%s-%d/leaderboard", baseURL, qz.Slug, qz.ID)

		t.Run(qz.Slug+" leaderboard 404s to a non-owner", func(t *testing.T) {
			t.Parallel()
			resp := httpGet(ctx, t, newAnonClient(t), leaderboardURL)
			defer closeBody(t, resp.Body)
			if got, want := resp.StatusCode, http.StatusNotFound; got != want {
				t.Errorf("status = %d, want %d", got, want)
			}
		})

		t.Run(qz.Slug+" leaderboard reads for an admin", func(t *testing.T) {
			t.Parallel()
			resp := httpGet(ctx, t, admin, leaderboardURL)
			defer closeBody(t, resp.Body)
			if got, want := resp.StatusCode, http.StatusOK; got != want {
				t.Errorf("status = %d, want %d", got, want)
			}
		})
	}
}
