//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/testutil"
)

// csrfTokenPattern extracts the value of the hidden CSRF form field a
// renderer embeds on every form. The browser does this implicitly when
// submitting a form; the test client has to scrape it out of the GET response.
// Built from csrf.FormField so a rename of the constant fails the integration
// test loudly instead of silently producing empty matches.
var csrfTokenPattern = regexp.MustCompile(
	fmt.Sprintf(`name="%s" value="([^"]+)"`, regexp.QuoteMeta(csrf.FormField)),
)

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

	var err error

	ctx, stop := testutil.SignalCtx(t)

	stdout := testutil.NewTestWriter(t)

	dbURI, cleanup := dbtest.SetupTestDB(t)
	defer cleanup()

	getenv := func(key string) string {
		env := map[string]string{
			"HOST":                 "localhost",
			"PORT":                 "0", // Let the OS choose an available port
			"DB_URI":               dbURI,
			"REGISTRATION_ENABLED": "true",
		}

		return env[key]
	}

	listenConfig := &net.ListenConfig{}
	var ln net.Listener
	ln, err = listenConfig.Listen(ctx, "tcp", net.JoinHostPort(getenv("HOST"), getenv("PORT")))
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(ctx, getenv, stdout, ln)
	}()

	serverAddr := ln.Addr().String()
	err = testutil.WaitForReady(ctx, t, 10*time.Second, fmt.Sprintf("http://%s/healthz", serverAddr))
	if err != nil {
		t.Fatalf("error waiting for server to be ready: %v", err)
	}

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

	// Register the first user (becomes admin) so subsequent /admin/* requests succeed.
	// Fetching the GET form first sets the CSRF nonce cookie on the jar and
	// returns the hidden token, both of which the POST then carries.
	registerToken := fetchCSRFToken(ctx, t, client, fmt.Sprintf("http://%s/register", serverAddr))

	registerForm := url.Values{}
	registerForm.Add("username", "integration-admin")
	registerForm.Add("password", "integration-pass-123")
	registerForm.Add("csrf_token", registerToken)

	registerReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("http://%s/register", serverAddr),
		strings.NewReader(registerForm.Encode()),
	)
	if err != nil {
		t.Fatalf("failed to create register request: %v", err)
	}
	registerReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	registerResp, err := client.Do(registerReq)
	if err != nil {
		t.Fatalf("failed to register: %v", err)
	}
	if got, want := registerResp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("register status = %d, want %d", got, want)
	}
	if cerr := registerResp.Body.Close(); cerr != nil {
		t.Errorf("failed to close register body: %v", cerr)
	}

	// Visit the quiz-create form GET so we can pull a fresh CSRF token tied
	// to the now-authenticated session jar.
	quizCreateToken := fetchCSRFToken(ctx, t, client, fmt.Sprintf("http://%s/admin/quizzes/new", serverAddr))

	quizForm := url.Values{}
	quizForm.Add("title", quizTitle)
	quizForm.Add("description", quizDesc)
	quizForm.Add("csrf_token", quizCreateToken)

	var req *http.Request
	req, err = http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("http://%s/admin/quizzes", serverAddr),
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
		fmt.Sprintf("http://%s/admin/quizzes", serverAddr),
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

	// Verify quiz details
	req, err = http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("http://%s%s", serverAddr, quizLocation),
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

	questionText := "What is the name of the famous plumber wearing red and blue?"
	questionOption1 := "Sonic"
	questionOption2 := "Mario"
	questionOption3 := "Tolls"
	questionOption4 := "Kirby"

	questionToken := fetchCSRFToken(
		ctx,
		t,
		client,
		fmt.Sprintf("http://%s%s/questions/new", serverAddr, quizLocation),
	)

	questionForm := url.Values{}
	questionForm.Add("text", questionText)
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
		fmt.Sprintf("http://%s%s/questions", serverAddr, quizLocation),
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
		fmt.Sprintf("http://%s%s", serverAddr, quizLocation),
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

	// Shutdown server
	stop()
	select {
	case err = <-errCh:
		// Ignore context.Canceled because we triggered it ourselves via stop()
		if err != nil && !errors.Is(err, context.Background().Err()) && !errors.Is(err, context.Canceled) {
			t.Errorf("run() returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("server failed to shutdown in time")
	}
}
