package integration_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/csrf"
)

// csrfTokenPattern extracts the value of the hidden CSRF form field a
// renderer embeds on every form. The browser does this implicitly when
// submitting a form; the test client has to scrape it out of the GET response.
// Built from csrf.FormField so a rename of the constant fails the integration
// test loudly instead of silently producing empty matches.
var csrfTokenPattern = regexp.MustCompile(
	fmt.Sprintf(`name="%s" value="([^"]+)"`, regexp.QuoteMeta(csrf.FormField)),
)

// roundIDPattern extracts the round id from a per-round "Add question"
// link on the quiz-view page (#929). Questions are always created in a
// specific round, so the integration flow scrapes a real round id off
// the quiz view the same way a browser would follow the button.
var roundIDPattern = regexp.MustCompile(`/questions/new\?round_id=(\d+)`)

// fetchCSRFToken issues a GET to the given URL using the supplied client,
// extracts the first csrf_token form value from the response body, and
// returns it. The client's cookie jar picks up the nonce cookie as a side
// effect, so subsequent POSTs from the same client carry the matching pair.
func fetchCSRFToken(ctx context.Context, t *testing.T, client *http.Client, formURL string) string {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, formURL, nil)
	if err != nil {
		t.Fatalf("failed to create CSRF GET request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("failed to GET %s: %v", formURL, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("failed to close response body: %v", cerr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body of %s: %v", formURL, err)
	}
	m := csrfTokenPattern.FindStringSubmatch(string(body))
	if m == nil {
		t.Fatalf("no csrf_token found in response from %s; body=%q", formURL, body)
	}

	return m[1]
}

func TestAdmin_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
	})
	baseURL := srv.BaseURL

	// Start of the integration test

	// Create a quiz
	quizTitle := "Integration Test Quiz"
	quizDesc := "A quiz created by integration test"

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Register the first user (becomes admin), verify their email, and
	// sign them in so subsequent /admin/* requests succeed. The #574 hard
	// gate means register no longer hands out a session, so the sign-in
	// is an explicit verify + login step.
	registerVerifyAndSignIn(ctx, t, client, baseURL, srv.DBURI, "integration-admin", "integration-pass-123")

	// Visit the quiz-create form GET so we can pull a fresh CSRF token tied
	// to the now-authenticated session jar.
	quizCreateToken := fetchCSRFToken(ctx, t, client, baseURL+"/admin/quizzes/new")

	quizForm := url.Values{}
	quizForm.Add("title", quizTitle)
	quizForm.Add("description", quizDesc)
	quizForm.Add("csrf_token", quizCreateToken)

	var req *http.Request
	req, err = http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		baseURL+"/admin/quizzes",
		strings.NewReader(quizForm.Encode()),
	)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("failed to post quiz: %v", err)
	}
	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			t.Errorf("failed to close response body: %v", closeErr)
		}
	}()
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("expected status %d, got %d", want, got)
		body, _ := io.ReadAll(resp.Body)
		t.Logf("Response body: %s", string(body))
	}

	quizLocation := resp.Header.Get("Location")
	if got, want := quizLocation, "/admin/quizzes/"; !strings.HasPrefix(got, want) {
		t.Errorf("got Location header %q, want prefix %q", got, want)
	}

	// Verify quiz exists in the list
	req, err = http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		baseURL+"/admin/quizzes",
		strings.NewReader(quizForm.Encode()),
	)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("failed to get quiz list: %v", err)
	}
	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			t.Errorf("failed to close response body: %v", closeErr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	if got, want := string(body), quizTitle; !strings.Contains(got, want) {
		t.Errorf("string(body) = %q, should contain %q", got, want)
	}

	// Pin Tailwind classes on the quiz list. max-w-shell is a custom
	// theme token from tailwind-src.css used by the navbar shell;
	// bg-cyan-soft is the play-URL chip pill rendered per quiz card.
	// Together they prove both the navbar and the per-card reskin
	// rendered. See #213.
	if got, want := string(body), `class="max-w-shell`; !strings.Contains(got, want) {
		t.Errorf("string(body) should contain Tailwind shell class %q, got %q", want, got)
	}
	if got, want := string(body), `bg-cyan-soft`; !strings.Contains(got, want) {
		t.Errorf("string(body) should contain per-card Tailwind class %q, got %q", want, got)
	}

	// Verify quiz details
	req, err = http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		baseURL+quizLocation,
		strings.NewReader(quizForm.Encode()),
	)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("failed to get quiz details: %v", err)
	}
	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			t.Errorf("failed to close response body: %v", closeErr)
		}
	}()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	if got, want := string(body), quizTitle; !strings.Contains(got, want) {
		t.Errorf("string(body) = %q, should contain %q", got, want)
	}
	if got, want := string(body), quizDesc; !strings.Contains(got, want) {
		t.Errorf("string(body) = %q, should contain %q", got, want)
	}

	// Questions belong to a round (#929): scrape the default round's id
	// off the quiz view's per-round "Add question" link and create the
	// question against it, mirroring the button a host would click.
	roundMatch := roundIDPattern.FindStringSubmatch(string(body))
	if roundMatch == nil {
		t.Fatalf("quiz view body has no per-round Add question link, body:\n%s", string(body))
	}
	roundID := roundMatch[1]

	questionText := "What is the name of the famous plumber wearing red and blue?"
	questionOption1 := "Sonic"
	questionOption2 := "Mario"
	questionOption3 := "Tolls"
	questionOption4 := "Kirby"

	questionToken := fetchCSRFToken(
		ctx,
		t,
		client,
		baseURL+quizLocation+"/questions/new?round_id="+roundID,
	)

	questionForm := url.Values{}
	questionForm.Add("text", questionText)
	questionForm.Add("round_id", roundID)
	questionForm.Add("position", "10")
	questionForm.Add("option[0].text", questionOption1)
	questionForm.Add("option[1].text", questionOption2)
	questionForm.Add("option[1].correct", "on")
	questionForm.Add("option[2].text", questionOption3)
	questionForm.Add("option[3].text", questionOption4)
	questionForm.Add("csrf_token", questionToken)

	// Add question to quiz
	req, err = http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		baseURL+quizLocation+"/questions",
		strings.NewReader(questionForm.Encode()),
	)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("failed to post quiz: %v", err)
	}
	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			t.Errorf("failed to close response body: %v", closeErr)
		}
	}()
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("expected status %d, got %d", want, got)
		errBody, _ := io.ReadAll(resp.Body)
		t.Logf("Response body: %s", string(errBody))
	}

	questionLocation := resp.Header.Get("Location")
	if got, want := questionLocation, quizLocation; !strings.HasPrefix(got, want) {
		t.Errorf("got Location header %q, want prefix %q", got, want)
	}

	// Verify question details using quiz details
	req, err = http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		baseURL+quizLocation,
		strings.NewReader(quizForm.Encode()),
	)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("failed to get quiz details: %v", err)
	}
	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			t.Errorf("failed to close response body: %v", closeErr)
		}
	}()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	if got, want := string(body), quizTitle; !strings.Contains(got, want) {
		t.Errorf("string(body) = %q, should contain %q", got, want)
	}
	if got, want := string(body), quizDesc; !strings.Contains(got, want) {
		t.Errorf("string(body) = %q, should contain %q", got, want)
	}
	if got, want := string(body), questionText; !strings.Contains(got, want) {
		t.Errorf("string(body) = %q, should contain %q", got, want)
	}
	if got, want := string(body), questionOption1; !strings.Contains(got, want) {
		t.Errorf("string(body) = %q, should contain %q", got, want)
	}
	if got, want := string(body), questionOption2; !strings.Contains(got, want) {
		t.Errorf("string(body) = %q, should contain %q", got, want)
	}
	if got, want := string(body), questionOption3; !strings.Contains(got, want) {
		t.Errorf("string(body) = %q, should contain %q", got, want)
	}
	if got, want := string(body), questionOption4; !strings.Contains(got, want) {
		t.Errorf("string(body) = %q, should contain %q", got, want)
	}

	// #246 — options sit behind a <details class="q-spoiler"> wrapper so an
	// admin can present the quiz without exposing answers. Server-rendered
	// HTML still contains the option text (the closed-by-default state is
	// CSS-controlled), so the integration test just pins the structural
	// shape; the open/close click behaviour is covered by e2e.
	if got, want := string(body), `<details class="q-spoiler">`; !strings.Contains(got, want) {
		t.Errorf("string(body) should contain spoiler wrapper %q", want)
	}
	if got, want := string(body), `Show spoilers`; !strings.Contains(got, want) {
		t.Errorf("string(body) should contain spoiler affordance label %q", want)
	}
	if got, want := string(body), `aria-label="Toggle answer options for question`; !strings.Contains(got, want) {
		t.Errorf("string(body) should contain spoiler aria-label prefix %q", want)
	}
}
