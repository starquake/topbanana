package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
)

// TestQuizMeta_Integration pins the deep-link metadata endpoint (#1214):
// GET /api/quizzes/{slugID} returns metadata only for a solo, published,
// visibility-permitted quiz. A draft, a live quiz, and a private quiz seen
// by an anonymous caller all 404 opaquely, matching applyQuizOG's withholding
// (#1192/#677/#103). A missing id 404s too.
func TestQuizMeta_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	stores := setup.Stores

	newQuiz := func(title, slug, desc, visibility, mode string, published bool) *quiz.Quiz {
		return &quiz.Quiz{
			Title:             title,
			Published:         published,
			Slug:              slug,
			Description:       desc,
			CreatedByPlayerID: seededAdminID,
			Visibility:        visibility,
			Mode:              mode,
			Questions: []*quiz.Question{
				{Text: "Q", Position: 1, Options: []*quiz.Option{{Text: "yes", Correct: true}, {Text: "no"}}},
			},
		}
	}

	publicQz := newQuiz("Public Meta", "public-meta", "Visible everywhere.", quiz.VisibilityPublic, quiz.ModeSolo, true)
	unlistedQz := newQuiz("Unlisted Meta", "unlisted-meta", "Link-only.", quiz.VisibilityUnlisted, quiz.ModeSolo, true)
	privateQz := newQuiz("Private Meta", "private-meta", "Members only.", quiz.VisibilityPrivate, quiz.ModeSolo, true)
	liveQz := newQuiz("Live Meta", "live-meta", "Hosted only.", quiz.VisibilityPublic, quiz.ModeLive, true)
	draftQz := newQuiz("Draft Meta", "draft-meta", "Not yet published.", quiz.VisibilityPublic, quiz.ModeSolo, false)
	for _, qz := range []*quiz.Quiz{publicQz, unlistedQz, privateQz, liveQz, draftQz} {
		if err := stores.Quizzes.CreateQuiz(ctx, qz); err != nil {
			t.Fatalf("CreateQuiz %q err = %v", qz.Title, err)
		}
	}

	type metaResponse struct {
		ID          int64  `json:"id"`
		Title       string `json:"title"`
		Slug        string `json:"slug"`
		Description string `json:"description"`
		Mode        string `json:"mode"`
	}

	metaURL := func(qz *quiz.Quiz) string {
		return fmt.Sprintf("%s/api/quizzes/%s-%d", baseURL, qz.Slug, qz.ID)
	}

	decodeMeta := func(t *testing.T, resp *http.Response) metaResponse {
		t.Helper()
		var meta metaResponse
		if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
			t.Fatalf("decode: %v", err)
		}

		return meta
	}

	anonJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v", err)
	}
	anonClient := &http.Client{Jar: anonJar}

	t.Run("anonymous can read public quiz metadata", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, anonClient, metaURL(publicQz))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		meta := decodeMeta(t, resp)
		if got, want := meta.ID, publicQz.ID; got != want {
			t.Errorf("id = %d, want %d", got, want)
		}
		if got, want := meta.Title, "Public Meta"; got != want {
			t.Errorf("title = %q, want %q", got, want)
		}
		if got, want := meta.Slug, "public-meta"; got != want {
			t.Errorf("slug = %q, want %q", got, want)
		}
		if got, want := meta.Mode, quiz.ModeSolo; got != want {
			t.Errorf("mode = %q, want %q", got, want)
		}
	})

	t.Run("anonymous can read unlisted quiz metadata by link", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, anonClient, metaURL(unlistedQz))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got, want := decodeMeta(t, resp).Title, "Unlisted Meta"; got != want {
			t.Errorf("title = %q, want %q", got, want)
		}
	})

	t.Run("live quiz metadata returns 404 (hosted-only, not solo-playable)", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, anonClient, metaURL(liveQz))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("draft quiz metadata returns 404 (not published)", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, anonClient, metaURL(draftQz))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("anonymous gets 404 reading private quiz metadata", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, anonClient, metaURL(privateQz))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("missing id returns 404", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, anonClient, baseURL+"/api/quizzes/nope-999999")
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

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
	registerVerifyAndSignIn(ctx, t, authClient, baseURL, setup.DBURI, "meta-resident", "meta-pass-123456")
	authClient.CheckRedirect = nil

	t.Run("authenticated player can read private quiz metadata", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, authClient, metaURL(privateQz))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got, want := decodeMeta(t, resp).Title, "Private Meta"; got != want {
			t.Errorf("title = %q, want %q", got, want)
		}
	})
}
