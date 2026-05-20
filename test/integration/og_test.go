//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
)

// TestOGMetadata_Integration covers ticket #258 — every shareable page
// surfaces an Open Graph card so chat-app link previews show a meaningful
// title, description, and the banana image. The per-quiz path on
// /play/{slugID} is the headline case: og:title and og:description swap
// in the quiz's own values so a shared quiz link previews the quiz, not
// generic site copy.
func TestOGMetadata_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	// Seed one quiz so we can drive both the per-quiz path and the
	// unknown-slug fallback. Description is non-trivial — proving it
	// reaches og:description rather than the default.
	qz := &quiz.Quiz{
		Title:       "Bananas of the World",
		Slug:        "bananas-of-the-world",
		Description: "Twenty rounds on cultivars, cuisines, and corporate history.",
	}
	if err := setup.Stores.Quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	t.Run("og-image asset is served as image/png", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, &http.Client{}, baseURL+"/assets/og-image.png")
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		if got, want := resp.Header.Get("Content-Type"), "image/png"; !strings.HasPrefix(got, want) {
			t.Errorf("Content-Type = %q, want prefix %q", got, want)
		}
	})

	t.Run("auth login page exposes sitewide OG defaults", func(t *testing.T) {
		t.Parallel()
		assertSitewideOG(ctx, t, baseURL+"/login")
	})

	t.Run("player SPA root exposes sitewide OG defaults", func(t *testing.T) {
		t.Parallel()
		assertSitewideOG(ctx, t, baseURL+"/client/")
	})

	t.Run("play deep-link injects quiz title and description", func(t *testing.T) {
		t.Parallel()
		body := getBody(ctx, t, fmt.Sprintf("%s/play/%s-%d", baseURL, qz.Slug, qz.ID))

		wantTitle := fmt.Sprintf(`<meta property="og:title" content="%s — Top Banana!">`, qz.Title)
		if got := body; !strings.Contains(got, wantTitle) {
			t.Errorf("body missing per-quiz og:title %q", wantTitle)
		}
		wantDesc := fmt.Sprintf(`<meta property="og:description" content="%s">`, qz.Description)
		if got := body; !strings.Contains(got, wantDesc) {
			t.Errorf("body missing per-quiz og:description %q", wantDesc)
		}
		// Twitter cards mirror the og:* values so X/Twitter previews also
		// reflect the quiz, not the sitewide defaults.
		wantTwitter := fmt.Sprintf(`<meta name="twitter:title" content="%s — Top Banana!">`, qz.Title)
		if got := body; !strings.Contains(got, wantTwitter) {
			t.Errorf("body missing per-quiz twitter:title %q", wantTwitter)
		}
	})

	t.Run("play deep-link with unknown slug falls back to defaults", func(t *testing.T) {
		t.Parallel()
		// Slug-id parses fine but the row doesn't exist — the handler
		// should still serve the SPA shell with the default card so the
		// link preview is reasonable rather than a 404.
		assertSitewideOG(ctx, t, baseURL+"/play/does-not-exist-99999")
	})
}

// assertSitewideOG fetches the URL and verifies the sitewide Open Graph
// defaults are present in the response body.
//
// The og:description substring deliberately ends at "see " so the assertion
// passes both the static gohtml layouts (literal apostrophe in "who's") and
// the SPA's html/template-rendered version (where the apostrophe is encoded
// to &#39; by attribute escaping). Both decode identically for scrapers.
func assertSitewideOG(ctx context.Context, t *testing.T, url string) {
	t.Helper()
	body := getBody(ctx, t, url)

	wantSubstrings := []string{
		`<meta property="og:site_name" content="Top Banana!">`,
		`<meta property="og:title" content="Be the Top Banana!">`,
		`<meta property="og:description" content="Make a quiz, share the link, see `,
		`<meta property="og:image" content="/assets/og-image.png">`,
		`<meta name="twitter:card" content="summary_large_image">`,
	}
	for _, want := range wantSubstrings {
		if got := body; !strings.Contains(got, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func getBody(ctx context.Context, t *testing.T, url string) string {
	t.Helper()
	resp := httpGet(ctx, t, &http.Client{}, url)
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d (url=%s)", got, want, url)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}

	return string(body)
}

func closeBody(t *testing.T, body io.Closer) {
	t.Helper()
	if cerr := body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}
}
