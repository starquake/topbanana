//go:build integration

package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

// TestAdminImport_Integration covers the JSON import flow added in #231:
// an admin pastes a quiz JSON document into /admin/quizzes/import, the
// server creates the quiz tree atomically, and the redirect lands on the
// new quiz's view page. The test also checks the rendered import form
// carries the example block — that's the whole point of the page.
func TestAdminImport_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New err = %v, want nil", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	registerAdminViaHTTP(ctx, t, client, srv.BaseURL)

	// Fetching the import form should both seed the CSRF nonce on the jar
	// AND render the example JSON block — without the example the page is
	// useless to the LLM round-trip workflow this feature exists for.
	importURL := srv.BaseURL + "/admin/quizzes/import"
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, importURL, nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	getResp, err := client.Do(getReq)
	if err != nil {
		t.Fatalf("GET import client.Do err = %v, want nil", err)
	}
	formBody, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if cerr := getResp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}
	if got, want := getResp.StatusCode, http.StatusOK; got != want {
		t.Errorf("GET import status = %d, want %d", got, want)
	}
	if got := string(formBody); !strings.Contains(got, "European Capitals") {
		t.Errorf("import form body got %q, should contain the example title %q", got, "European Capitals")
	}

	// CSRF token is the same scrape the regular admin tests use.
	csrfToken := fetchCSRFToken(ctx, t, client, importURL)

	// Post a minimal but valid quiz. The slug is intentionally omitted to
	// exercise the auto-derive-from-title path.
	const quizJSON = `{
  "title": "Import Round-Trip",
  "description": "Round-trip integration test for the JSON importer.",
  "questions": [
    {
      "text": "What is the capital of the Czech Republic?",
      "options": [
        { "text": "Prague",   "correct": true  },
        { "text": "Warsaw",   "correct": false },
        { "text": "Budapest", "correct": false },
        { "text": "Vienna",   "correct": false }
      ]
    },
    {
      "text": "Lisbon is the capital of which country?",
      "options": [
        { "text": "Spain",    "correct": false },
        { "text": "Portugal", "correct": true  }
      ]
    }
  ]
}`

	form := url.Values{}
	form.Add("json", quizJSON)
	form.Add("csrf_token", csrfToken)

	postReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, importURL, strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postResp, err := client.Do(postReq)
	if err != nil {
		t.Fatalf("POST import client.Do err = %v, want nil", err)
	}
	if cerr := postResp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}

	if got, want := postResp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("POST import status = %d, want %d", got, want)
	}
	location := postResp.Header.Get("Location")
	if !strings.HasPrefix(location, "/admin/quizzes/") {
		t.Fatalf("POST import Location = %q, want prefix /admin/quizzes/", location)
	}

	// Follow the redirect and verify both question texts render on the
	// resulting quiz view — confirming the tree (quiz + questions +
	// options) was persisted, not just the quiz row.
	viewURL := srv.BaseURL + location
	viewReq, err := http.NewRequestWithContext(ctx, http.MethodGet, viewURL, nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	viewResp, err := client.Do(viewReq)
	if err != nil {
		t.Fatalf("GET quiz view client.Do err = %v, want nil", err)
	}
	viewBody, err := io.ReadAll(viewResp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if cerr := viewResp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}
	if got, want := viewResp.StatusCode, http.StatusOK; got != want {
		t.Errorf("GET quiz view status = %d, want %d", got, want)
	}
	body := string(viewBody)
	for _, want := range []string{
		"Import Round-Trip",
		"What is the capital of the Czech Republic?",
		"Lisbon is the capital of which country?",
		"Prague",
		"Portugal",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("quiz view body got %q, should contain %q", body, want)
		}
	}

	// Negative paths. Each subtest posts a different malformed JSON and
	// asserts the corresponding error branch: invalid JSON syntax,
	// unknown fields (rejected by DisallowUnknownFields), and
	// well-formed JSON that fails the domain Valid() check.
	t.Run("syntactically broken JSON", func(t *testing.T) {
		t.Parallel()
		// html/template HTML-escapes the textarea content, so the literal
		// `"title": "x"` we POSTed appears in the response as
		// `&#34;title&#34;: &#34;x&#34;` — pin that exact form to catch
		// both "form re-rendered" and "user's text survived the round-trip".
		postImportRejection(ctx, t, client, importURL,
			`{"title": "x", "questions": [`,
			http.StatusBadRequest,
			[]string{"invalid JSON", `&#34;title&#34;: &#34;x&#34;`},
		)
	})

	t.Run("unknown JSON field rejected", func(t *testing.T) {
		t.Parallel()
		// `slug` was deliberately removed from the wire shape; the
		// DisallowUnknownFields decoder must reject it so a caller that
		// thinks it can override the server-derived slug finds out at
		// import time, not at quiz-load time.
		postImportRejection(ctx, t, client, importURL,
			`{"title": "x", "slug": "x", "description": "y", "questions": []}`,
			http.StatusBadRequest,
			[]string{"invalid JSON", `&#34;slug&#34;`},
		)
	})

	t.Run("domain validation failure", func(t *testing.T) {
		t.Parallel()
		// Empty title trips quiz.Quiz.Valid (Title is required). The
		// "validation errors:" prefix is what the handler prepends to
		// any map of field problems — pin it so a future refactor of
		// the error rendering doesn't silently swallow this branch.
		postImportRejection(
			ctx,
			t,
			client,
			importURL,
			`{"title": "", "description": "ok", "questions": [{"text": "Q", "options": [{"text": "A", "correct": true}]}]}`,
			http.StatusBadRequest,
			[]string{"validation errors", "Title is required"},
		)
	})

	t.Run("imported quiz carries the payload's timeLimitSeconds", func(t *testing.T) {
		t.Parallel()
		// #99: the payload exposes timeLimitSeconds at both the quiz
		// and per-question level. The quiz value lands on Quiz.TimeLimitSeconds
		// (visible on the edit form as value="15") and the question
		// override drops the inherit-the-default behaviour for that
		// row (visible on the question's edit form). Assert both by
		// scraping the edit-form HTML, which is the same path the
		// admin would use to verify the value stuck.
		csrf := fetchCSRFToken(ctx, t, client, importURL)
		const tlJSON = `{
  "title": "Time-Limit Import",
  "description": "Round-trip the per-quiz + per-question time limits.",
  "timeLimitSeconds": 15,
  "questions": [
    {
      "text": "Override question.",
      "timeLimitSeconds": 5,
      "options": [
        { "text": "A", "correct": true  },
        { "text": "B", "correct": false }
      ]
    }
  ]
}`
		tlForm := url.Values{}
		tlForm.Add("json", tlJSON)
		tlForm.Add("csrf_token", csrf)
		req, err := http.NewRequestWithContext(
			ctx, http.MethodPost, importURL, strings.NewReader(tlForm.Encode()),
		)
		if err != nil {
			t.Fatalf("NewRequest err = %v, want nil", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST import client.Do err = %v, want nil", err)
		}
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("Body.Close err = %v, want nil", cerr)
		}
		if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
			t.Fatalf("POST import status = %d, want %d", got, want)
		}
		quizPath := resp.Header.Get("Location")
		if !strings.HasPrefix(quizPath, "/admin/quizzes/") {
			t.Fatalf("Location = %q, want prefix /admin/quizzes/", quizPath)
		}

		// Edit form for the quiz must reflect the imported 15s default.
		// Use the authenticated client (the bare getBody helper has no
		// session cookie, so it would 303 to /login).
		editReq, err := http.NewRequestWithContext(
			ctx, http.MethodGet, srv.BaseURL+quizPath+"/edit", nil,
		)
		if err != nil {
			t.Fatalf("NewRequest err = %v, want nil", err)
		}
		editResp, err := client.Do(editReq)
		if err != nil {
			t.Fatalf("GET edit client.Do err = %v, want nil", err)
		}
		editBytes, err := io.ReadAll(editResp.Body)
		if err != nil {
			t.Fatalf("ReadAll err = %v, want nil", err)
		}
		if cerr := editResp.Body.Close(); cerr != nil {
			t.Errorf("Body.Close err = %v, want nil", cerr)
		}
		if got, want := editResp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("GET edit status = %d, want %d", got, want)
		}
		if want := `value="15"`; !strings.Contains(string(editBytes), want) {
			t.Errorf("quiz edit form missing %q (the imported default time limit)", want)
		}
	})

	t.Run("duplicate title returns 409", func(t *testing.T) {
		t.Parallel()
		// The first import above succeeded with title "Import Round-Trip"
		// and slug "import-round-trip". A second import with the same
		// title derives the same slug, which now collides with the
		// existing row. The fix (#293) maps the SQLite UNIQUE failure
		// to ErrSlugTaken and re-renders the import form at 409 with
		// the JSON intact so the admin can rename and resubmit without
		// re-pasting.
		const dupJSON = `{
  "title": "Import Round-Trip",
  "description": "Same title as the earlier successful import.",
  "questions": [
    {
      "text": "Anything?",
      "options": [
        { "text": "A", "correct": true  },
        { "text": "B", "correct": false }
      ]
    }
  ]
}`
		postImportRejection(
			ctx,
			t,
			client,
			importURL,
			dupJSON,
			http.StatusConflict,
			[]string{"already exists", "Import Round-Trip"},
		)
	})
}

// postImportRejection posts the given JSON to the import endpoint and
// asserts the response status equals wantStatus and the form re-rendered
// with each substring in wantSubstrings present in the body. Factored
// out to keep the negative-path subtests small. wantStatus is the
// expected HTTP status: 400 for validation errors, 409 for slug
// collisions (#293).
func postImportRejection(
	ctx context.Context, t *testing.T, client *http.Client,
	importURL, jsonBody string, wantStatus int, wantSubstrings []string,
) {
	t.Helper()

	csrfToken := fetchCSRFToken(ctx, t, client, importURL)
	form := url.Values{}
	form.Add("json", jsonBody)
	form.Add("csrf_token", csrfToken)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, importURL, strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("Body.Close err = %v, want nil", cerr)
	}

	if got, want := resp.StatusCode, wantStatus; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	rendered := string(body)
	for _, want := range wantSubstrings {
		if !strings.Contains(rendered, want) {
			t.Errorf("body got %q, should contain %q", rendered, want)
		}
	}
}
