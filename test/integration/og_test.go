package integration_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"regexp"
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
		Title:             "Bananas of the World",
		Slug:              "bananas-of-the-world",
		Description:       "Twenty rounds on cultivars, cuisines, and corporate history.",
		CreatedByPlayerID: seededAdminID,
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
		assertSitewideOG(ctx, t, baseURL+"/login", baseURL)
	})

	t.Run("admin quizzes page exposes sitewide OG defaults", func(t *testing.T) {
		t.Parallel()
		// /admin/quizzes is owner-gated, so registering + signing in is the
		// cheapest way to land an authenticated client on the page. The
		// first password-bearing registrant is promoted to admin.
		// registerVerifyAndSignIn sees the login 303 directly, so suppress
		// the default redirect policy on the throwaway client.
		jar, jerr := cookiejar.New(nil)
		if jerr != nil {
			t.Fatalf("cookiejar.New err = %v, want nil", jerr)
		}
		client := &http.Client{
			Jar:           jar,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}
		registerVerifyAndSignIn(ctx, t, client, baseURL, setup.DBURI, "htmx-admin", "htmx-admin-pass-123")

		resp := httpGet(ctx, t, client, baseURL+"/admin/quizzes")
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("ReadAll err = %v, want nil", err)
		}
		assertAbsoluteOGImage(t, string(body), baseURL)
	})

	t.Run("player SPA root exposes sitewide OG defaults", func(t *testing.T) {
		t.Parallel()
		assertSitewideOG(ctx, t, baseURL+"/client/", baseURL)
	})

	t.Run("play deep-link injects quiz title and description", func(t *testing.T) {
		t.Parallel()
		body := getBody(ctx, t, fmt.Sprintf("%s/play/%s-%d", baseURL, qz.Slug, qz.ID))

		wantTitle := fmt.Sprintf(`<meta property="og:title" content="%s - Top Banana!">`, qz.Title)
		if got := body; !strings.Contains(got, wantTitle) {
			t.Errorf("body missing per-quiz og:title %q", wantTitle)
		}
		wantDesc := fmt.Sprintf(`<meta property="og:description" content="%s">`, qz.Description)
		if got := body; !strings.Contains(got, wantDesc) {
			t.Errorf("body missing per-quiz og:description %q", wantDesc)
		}
		// Twitter cards mirror the og:* values so X/Twitter previews also
		// reflect the quiz, not the sitewide defaults.
		wantTwitter := fmt.Sprintf(`<meta name="twitter:title" content="%s - Top Banana!">`, qz.Title)
		if got := body; !strings.Contains(got, wantTwitter) {
			t.Errorf("body missing per-quiz twitter:title %q", wantTwitter)
		}
	})

	t.Run("play deep-link with unknown slug falls back to defaults", func(t *testing.T) {
		t.Parallel()
		// Slug-id parses fine but the row doesn't exist — the handler
		// should still serve the SPA shell with the default card so the
		// link preview is reasonable rather than a 404.
		assertSitewideOG(ctx, t, baseURL+"/play/does-not-exist-99999", baseURL)
	})

	t.Run("play deep-link emits an absolute og:image", func(t *testing.T) {
		t.Parallel()
		// The headline #294 case: WhatsApp/Slack/Discord scrapers want
		// an absolute og:image. The per-quiz path swaps the OG title
		// and description but inherits the sitewide image, so the
		// absolute-URL invariant applies here too.
		body := getBody(ctx, t, fmt.Sprintf("%s/play/%s-%d", baseURL, qz.Slug, qz.ID))
		assertAbsoluteOGImage(t, body, baseURL)
	})
}

// TestOGMetadata_LiveQuiz pins the MP-1 share-card tightening (#678): a
// /play/{slug} link for a mode='live' quiz must fall back to the sitewide
// default OG card so the title/description are not leaked into link
// previews before the hosted game runs. A mode='solo' quiz still surfaces
// its own title/description.
func TestOGMetadata_LiveQuiz(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	liveQz := &quiz.Quiz{
		Title:             "Secret Live Quiz",
		Slug:              "secret-live-quiz",
		Description:       "Spoilers that must not leak into a link preview.",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeLive,
	}
	if err := setup.Stores.Quizzes.CreateQuiz(ctx, liveQz); err != nil {
		t.Fatalf("CreateQuiz live err = %v, want nil", err)
	}

	soloQz := &quiz.Quiz{
		Title:             "Open Solo Quiz",
		Slug:              "open-solo-quiz",
		Description:       "Self-paced; safe to preview.",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeSolo,
	}
	if err := setup.Stores.Quizzes.CreateQuiz(ctx, soloQz); err != nil {
		t.Fatalf("CreateQuiz solo err = %v, want nil", err)
	}

	t.Run("live quiz play link serves the default OG card", func(t *testing.T) {
		t.Parallel()
		// Same default-card assertion as the unknown-slug case: the live
		// quiz's title/description must be absent and the sitewide
		// defaults present.
		assertSitewideOG(ctx, t, fmt.Sprintf("%s/play/%s-%d", baseURL, liveQz.Slug, liveQz.ID), baseURL)

		body := getBody(ctx, t, fmt.Sprintf("%s/play/%s-%d", baseURL, liveQz.Slug, liveQz.ID))
		if strings.Contains(body, liveQz.Title) {
			t.Errorf("live quiz play card leaked title %q", liveQz.Title)
		}
		if strings.Contains(body, liveQz.Description) {
			t.Errorf("live quiz play card leaked description %q", liveQz.Description)
		}
	})

	t.Run("solo quiz play link still injects its title and description", func(t *testing.T) {
		t.Parallel()
		body := getBody(ctx, t, fmt.Sprintf("%s/play/%s-%d", baseURL, soloQz.Slug, soloQz.ID))

		wantTitle := fmt.Sprintf(`<meta property="og:title" content="%s - Top Banana!">`, soloQz.Title)
		if !strings.Contains(body, wantTitle) {
			t.Errorf("solo quiz play card missing og:title %q", wantTitle)
		}
		wantDesc := fmt.Sprintf(`<meta property="og:description" content="%s">`, soloQz.Description)
		if !strings.Contains(body, wantDesc) {
			t.Errorf("solo quiz play card missing og:description %q", wantDesc)
		}
	})
}

// TestOGMetadata_PrivateQuiz pins the #783 share-card tightening: a
// /play/{slug} link for a private quiz must fall back to the sitewide
// default OG card so an anonymous link-preview scraper cannot read the
// quiz's title/description. The share card is unauthenticated with no
// viewer context, so a private quiz never gets a rich card. A public quiz
// still surfaces its own title/description.
func TestOGMetadata_PrivateQuiz(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	privateQz := &quiz.Quiz{
		Title:             "Confidential Private Quiz",
		Slug:              "confidential-private-quiz",
		Description:       "Title and description that must stay behind sign-in.",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPrivate,
		Mode:              quiz.ModeSolo,
	}
	if err := setup.Stores.Quizzes.CreateQuiz(ctx, privateQz); err != nil {
		t.Fatalf("CreateQuiz private err = %v, want nil", err)
	}

	publicQz := &quiz.Quiz{
		Title:             "Open Public Quiz",
		Slug:              "open-public-quiz",
		Description:       "Listed and safe to preview.",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeSolo,
	}
	if err := setup.Stores.Quizzes.CreateQuiz(ctx, publicQz); err != nil {
		t.Fatalf("CreateQuiz public err = %v, want nil", err)
	}

	t.Run("private quiz play link serves the default OG card", func(t *testing.T) {
		t.Parallel()
		assertSitewideOG(ctx, t, fmt.Sprintf("%s/play/%s-%d", baseURL, privateQz.Slug, privateQz.ID), baseURL)

		body := getBody(ctx, t, fmt.Sprintf("%s/play/%s-%d", baseURL, privateQz.Slug, privateQz.ID))
		if strings.Contains(body, privateQz.Title) {
			t.Errorf("private quiz play card leaked title %q", privateQz.Title)
		}
		if strings.Contains(body, privateQz.Description) {
			t.Errorf("private quiz play card leaked description %q", privateQz.Description)
		}
	})

	t.Run("public quiz play link still injects its title and description", func(t *testing.T) {
		t.Parallel()
		body := getBody(ctx, t, fmt.Sprintf("%s/play/%s-%d", baseURL, publicQz.Slug, publicQz.ID))

		wantTitle := fmt.Sprintf(`<meta property="og:title" content="%s - Top Banana!">`, publicQz.Title)
		if !strings.Contains(body, wantTitle) {
			t.Errorf("public quiz play card missing og:title %q", wantTitle)
		}
		wantDesc := fmt.Sprintf(`<meta property="og:description" content="%s">`, publicQz.Description)
		if !strings.Contains(body, wantDesc) {
			t.Errorf("public quiz play card missing og:description %q", wantDesc)
		}
	})
}

// assertSitewideOG fetches the URL and verifies the sitewide Open Graph
// defaults are present in the response body. baseURL is the absolute
// scheme://host the server is listening on; the og:image assertion uses
// it to confirm the rendered URL is absolute and matches the host the
// request landed on (#294).
//
// The og:description substring deliberately ends at "see " so the assertion
// passes both the static gohtml layouts (literal apostrophe in "who's") and
// the SPA's html/template-rendered version (where the apostrophe is encoded
// to &#39; by attribute escaping). Both decode identically for scrapers.
func assertSitewideOG(ctx context.Context, t *testing.T, url, baseURL string) {
	t.Helper()
	body := getBody(ctx, t, url)

	wantSubstrings := []string{
		`<meta property="og:site_name" content="Top Banana!">`,
		`<meta property="og:title" content="Be the Top Banana!">`,
		`<meta property="og:description" content="Make a quiz, share the link, see `,
		`<meta name="twitter:card" content="summary_large_image">`,
	}
	for _, want := range wantSubstrings {
		if got := body; !strings.Contains(got, want) {
			t.Errorf("body missing %q", want)
		}
	}
	assertAbsoluteOGImage(t, body, baseURL)
}

// absoluteOGImagePattern matches `^https?://.+/assets/og-image\.png$` so
// the og:image / twitter:image meta tags must carry a fully-qualified
// URL. Defined as a package-level regexp so the assertion can run inside
// parallel subtests without re-compiling on each call.
var absoluteOGImagePattern = regexp.MustCompile(`^https?://.+/assets/og-image\.png$`)

// ogImageMetaPattern extracts the attribute and content of a meta tag
// pointing at the OG/Twitter card image. Capture group 1 is the
// attribute (og:image or twitter:image) so the assertion below can name
// which tag failed; capture group 2 is the URL string.
var ogImageMetaPattern = regexp.MustCompile(
	`<meta (?:property|name)="((?:og|twitter):image)" content="([^"]+)">`,
)

// assertAbsoluteOGImage fails the test if any og:image / twitter:image
// meta tag in body carries a non-absolute URL, or if the URL points at a
// different host than the request landed on. The ticket (#294) AC is
// "matches `^https?://.+/assets/og-image\.png$`"; the host-equality
// check is a stronger property that catches a misconfigured BaseURL
// helper too.
func assertAbsoluteOGImage(t *testing.T, body, baseURL string) {
	t.Helper()

	matches := ogImageMetaPattern.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatal("body has no og:image / twitter:image meta tag")
	}
	wantPrefix := baseURL + "/assets/og-image.png"
	for _, m := range matches {
		attr, url := m[1], m[2]
		if !absoluteOGImagePattern.MatchString(url) {
			t.Errorf("%s URL = %q, want match %s", attr, url, absoluteOGImagePattern)
		}
		if got, want := url, wantPrefix; got != want {
			t.Errorf("%s URL = %q, want %q", attr, got, want)
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
