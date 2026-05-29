//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

// TestQuizOwnership_Integration covers #281/#538: a Host may edit or delete
// only the quizzes they created, never another Host's. The test boots two
// browser-shaped clients, makes both Hosts, has hostA create a quiz, and then
// probes hostB against every mutating admin endpoint scoped to that quiz. Each
// probe must come back 403 from the requireQuizOwner gate (an Admin would pass
// it - that path is covered in TestRoles_HostGating).
func TestQuizOwnership_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		// A throwaway first registrant consumes the first-registrant Admin
		// promotion so both hosts under test are plain registrants we can
		// demote to Host.
		"ADMIN_EMAILS": "ownership-boss@example.test",
	})
	baseURL := srv.BaseURL

	registerAdminClient(ctx, t, baseURL, srv.DBURI, "ownership-boss")
	adminA := registerAdminClient(ctx, t, baseURL, srv.DBURI, "ownership-admin-a")
	adminB := registerAdminClient(ctx, t, baseURL, srv.DBURI, "ownership-admin-b")
	makeHost(ctx, t, srv.DBURI, "ownership-admin-a")
	makeHost(ctx, t, srv.DBURI, "ownership-admin-b")

	// Host A creates a quiz; we capture its ID for the cross-host probes.
	// The create endpoint redirects to /admin/quizzes/{id}; the Location
	// header carries the id.
	quizID := createQuizAs(ctx, t, adminA, baseURL, "Ownership Quiz")

	t.Run("non-owner POST update returns 403", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, adminB, baseURL+fmt.Sprintf("/admin/quizzes/%d/edit", quizID))
		form := url.Values{"title": {"Hijacked"}, "description": {"x"}, "csrf_token": {token}}
		// The GET-for-CSRF above also surfaces the 403 inline since
		// requireQuizOwner gates the edit form — but the form still
		// renders a CSRF cookie (the renderer writes the cookie before
		// the render path early-returns). Posting from that jar
		// exercises the POST gate specifically.
		req := newFormReq(ctx, t, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID), form)
		resp, err := adminB.Do(req)
		if err != nil {
			t.Fatalf("Do err = %v, want nil", err)
		}
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusForbidden; got != want {
			t.Errorf("update status = %d, want %d", got, want)
		}
	})

	t.Run("non-owner POST delete returns 403", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, adminB, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
		form := url.Values{"csrf_token": {token}}
		req := newFormReq(ctx, t, baseURL+fmt.Sprintf("/admin/quizzes/%d/delete", quizID), form)
		resp, err := adminB.Do(req)
		if err != nil {
			t.Fatalf("Do err = %v, want nil", err)
		}
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusForbidden; got != want {
			t.Errorf("delete status = %d, want %d", got, want)
		}
	})

	t.Run("non-owner GET edit page returns 403", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, adminB, baseURL+fmt.Sprintf("/admin/quizzes/%d/edit", quizID))
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusForbidden; got != want {
			t.Errorf("edit GET status = %d, want %d", got, want)
		}
	})

	t.Run("non-owner POST question add returns 403", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, adminB, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
		form := url.Values{
			"text":              {"Q"},
			"option[0].text":    {"a"},
			"option[0].correct": {"on"},
			"option[1].text":    {"b"},
			"csrf_token":        {token},
		}
		req := newFormReq(ctx, t, baseURL+fmt.Sprintf("/admin/quizzes/%d/questions", quizID), form)
		resp, err := adminB.Do(req)
		if err != nil {
			t.Fatalf("Do err = %v, want nil", err)
		}
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusForbidden; got != want {
			t.Errorf("question add status = %d, want %d", got, want)
		}
	})

	t.Run("owner can still edit", func(t *testing.T) {
		t.Parallel()
		token := fetchCSRFToken(ctx, t, adminA, baseURL+fmt.Sprintf("/admin/quizzes/%d/edit", quizID))
		form := url.Values{
			"title":       {"Ownership Quiz (renamed)"},
			"description": {"renamed by owner"},
			"csrf_token":  {token},
		}
		req := newFormReq(ctx, t, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID), form)
		resp, err := adminA.Do(req)
		if err != nil {
			t.Fatalf("Do err = %v, want nil", err)
		}
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
			t.Errorf("owner update status = %d, want %d", got, want)
		}
	})
}

// registerAdminClient builds a cookie-jar HTTP client, registers the
// supplied username through the public /register form (which promotes
// to admin via ADMIN_EMAILS), and returns the client carrying the
// resulting session cookie. dbURI is the test server's DB URI; the
// helper stamps email_verified_at on the new row so follow-up admin
// requests pass the #111 PR3 verified-email gate. Mirrors the helper
// pattern in TestAdmin_Integration but pulled out so the cross-admin
// test can spin up two distinct sessions cheaply.
func registerAdminClient(ctx context.Context, t *testing.T, baseURL, dbURI, username string) *http.Client {
	t.Helper()
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

	token := fetchCSRFToken(ctx, t, client, baseURL+"/register")
	form := url.Values{
		"username":         {username},
		"email":            {username + "@example.test"},
		"password":         {"integration-pass-123"},
		"password_confirm": {"integration-pass-123"},
		"csrf_token":       {token},
	}
	req := newFormReq(ctx, t, baseURL+"/register", form)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("register %q err = %v, want nil", username, err)
	}
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("register %q status = %d, want %d", username, got, want)
	}

	verifyPlayerEmail(ctx, t, dbURI, username)

	return client
}

// createQuizAs posts a quiz with the given title via /admin/quizzes
// and returns the id parsed from the redirect Location header.
func createQuizAs(ctx context.Context, t *testing.T, client *http.Client, baseURL, title string) int64 {
	t.Helper()
	token := fetchCSRFToken(ctx, t, client, baseURL+"/admin/quizzes/new")
	form := url.Values{
		"title":       {title},
		"description": {"owned by test"},
		"csrf_token":  {token},
	}
	req := newFormReq(ctx, t, baseURL+"/admin/quizzes", form)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create quiz err = %v, want nil", err)
	}
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		// Best-effort body capture for the fatal message; the test is
		// failing either way so a read error here is not worth
		// surfacing separately.
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create quiz status = %d, want %d; body=%q", got, want, body)
	}
	loc := resp.Header.Get("Location")
	// Location looks like /admin/quizzes/{id}; parse the trailing id.
	const prefix = "/admin/quizzes/"
	if !strings.HasPrefix(loc, prefix) {
		t.Fatalf("create quiz Location = %q, want prefix %q", loc, prefix)
	}
	var id int64
	if _, err := fmt.Sscanf(loc[len(prefix):], "%d", &id); err != nil {
		t.Fatalf("parse quiz id from Location %q err = %v", loc, err)
	}

	return id
}

// newFormReq builds a POST application/x-www-form-urlencoded request
// with the given context. Pulled into a helper so the per-test setups
// stay readable.
func newFormReq(ctx context.Context, t *testing.T, target string, form url.Values) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return req
}
