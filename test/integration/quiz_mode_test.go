package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
)

// TestQuizModeGating_Integration pins the MP-0 solo gate end-to-end
// (#677): a live quiz is absent from the public/solo browse list, is
// rejected by the solo play data endpoint, and is rejected by the
// game-create path - all with a 404 so a live quiz is indistinguishable
// from a missing one. A solo quiz stays listed and playable.
func TestQuizModeGating_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	stores := setup.Stores

	soloQz := &quiz.Quiz{
		Title:             "Solo Mode Quiz",
		Slug:              "solo-mode-quiz",
		Description:       "Self-paced.",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeSolo,
		Questions: []*quiz.Question{
			{Text: "Q", Position: 1, Options: []*quiz.Option{{Text: "yes", Correct: true}, {Text: "no"}}},
		},
	}
	if err := stores.Quizzes.CreateQuiz(ctx, soloQz); err != nil {
		t.Fatalf("CreateQuiz solo err = %v", err)
	}

	liveQz := &quiz.Quiz{
		Title:             "Live Mode Quiz",
		Slug:              "live-mode-quiz",
		Description:       "Hosted only.",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeLive,
		Questions: []*quiz.Question{
			{Text: "Q", Position: 1, Options: []*quiz.Option{{Text: "yes", Correct: true}, {Text: "no"}}},
		},
	}
	if err := stores.Quizzes.CreateQuiz(ctx, liveQz); err != nil {
		t.Fatalf("CreateQuiz live err = %v", err)
	}

	anonJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v", err)
	}
	anonClient := &http.Client{Jar: anonJar}

	t.Run("solo browse list omits the live quiz", func(t *testing.T) {
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
		if !seen["Solo Mode Quiz"] {
			t.Error("solo browse list missing the solo quiz")
		}
		if seen["Live Mode Quiz"] {
			t.Error("solo browse list surfaced the live quiz")
		}
	})

	t.Run("solo play endpoint 404s the live quiz", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, anonClient, fmt.Sprintf("%s/api/quizzes/%s-%d", baseURL, liveQz.Slug, liveQz.ID))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("solo play endpoint still serves the solo quiz", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, anonClient, fmt.Sprintf("%s/api/quizzes/%s-%d", baseURL, soloQz.Slug, soloQz.ID))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("game-create 404s the live quiz", func(t *testing.T) {
		t.Parallel()
		body := fmt.Sprintf(`{"quizId": %d}`, liveQz.ID)
		resp := httpPostJSON(ctx, t, anonClient, baseURL+"/api/games", body)
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}

// TestQuizModeForm_Integration pins the admin form round-trip for the
// play mode (MP-0 / #677): creating a quiz with mode=live persists as
// live, and editing it back to solo persists as solo. The form is the
// only surface that lets a host pick the mode.
func TestQuizModeForm_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	stores := setup.Stores

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	registerVerifyAndSignIn(ctx, t, client, baseURL, setup.DBURI, "mode-form-admin", "mode-form-pass-123")

	// Create a live quiz through the form.
	createToken := fetchCSRFToken(ctx, t, client, baseURL+"/admin/quizzes/new")
	createForm := url.Values{}
	createForm.Add("title", "Mode Form Quiz")
	createForm.Add("description", "Created via the admin form.")
	createForm.Add("time_limit_seconds", "10")
	createForm.Add("visibility", quiz.VisibilityPublic)
	createForm.Add("mode", quiz.ModeLive)
	createForm.Add("csrf_token", createToken)

	location := postQuizForm(ctx, t, client, baseURL+"/admin/quizzes", createForm)
	quizID := quizIDFromLocation(t, location)

	qz, err := stores.Quizzes.GetQuiz(ctx, quizID)
	if err != nil {
		t.Fatalf("GetQuiz after create err = %v", err)
	}
	if got, want := qz.Mode, quiz.ModeLive; got != want {
		t.Fatalf("created quiz mode = %q, want %q", got, want)
	}

	// Edit it back to solo through the form.
	editURL := fmt.Sprintf("%s/admin/quizzes/%d/edit", baseURL, quizID)
	editToken := fetchCSRFToken(ctx, t, client, editURL)
	editForm := url.Values{}
	editForm.Add("title", "Mode Form Quiz")
	editForm.Add("description", "Created via the admin form.")
	editForm.Add("time_limit_seconds", "10")
	editForm.Add("visibility", quiz.VisibilityPublic)
	editForm.Add("mode", quiz.ModeSolo)
	editForm.Add("csrf_token", editToken)

	postQuizForm(ctx, t, client, fmt.Sprintf("%s/admin/quizzes/%d", baseURL, quizID), editForm)

	qz, err = stores.Quizzes.GetQuiz(ctx, quizID)
	if err != nil {
		t.Fatalf("GetQuiz after edit err = %v", err)
	}
	if got, want := qz.Mode, quiz.ModeSolo; got != want {
		t.Errorf("edited quiz mode = %q, want %q", got, want)
	}
}

// postQuizForm submits a urlencoded quiz form, asserts the 303 redirect,
// and returns the Location header so the caller can pull the quiz id.
func postQuizForm(
	ctx context.Context, t *testing.T, client *http.Client, postURL string, form url.Values,
) string {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request err = %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post quiz form err = %v", err)
	}
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("post %s status = %d, want %d", postURL, got, want)
	}

	return resp.Header.Get("Location")
}

// quizIDFromLocation parses the trailing quiz id off an
// /admin/quizzes/{id} redirect Location.
func quizIDFromLocation(t *testing.T, location string) int64 {
	t.Helper()
	const prefix = "/admin/quizzes/"
	if !strings.HasPrefix(location, prefix) {
		t.Fatalf("Location = %q, want prefix %q", location, prefix)
	}
	var quizID int64
	if _, err := fmt.Sscanf(strings.TrimPrefix(location, prefix), "%d", &quizID); err != nil {
		t.Fatalf("parse quiz id from %q err = %v", location, err)
	}

	return quizID
}
