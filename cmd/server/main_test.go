package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/testutil"
)

func TestRun_CreateQuiz(t *testing.T) {
	t.Parallel()

	ctx, stop := testutil.SignalCtx(t)

	var err error
	var tmpDB *os.File
	// Setup temporary database for the test
	tmpDB, err = os.CreateTemp(t.TempDir(), "topbanana-test-*.sqlite")
	if err != nil {
		t.Fatalf("failed to create temp db: %v", err)
	}
	tmpDBPath := tmpDB.Name()
	err = tmpDB.Close()
	if err != nil {
		t.Fatalf("failed to close temp db: %v", err)
	}
	defer func() {
		removeErr := os.Remove(tmpDBPath)
		if removeErr != nil {
			t.Errorf("failed to remove temp db: %s", removeErr)
		}
	}()

	getenv := func(key string) string {
		env := map[string]string{
			"PORT":   "0", // Let the OS choose an available port
			"DB_URI": "file:" + tmpDBPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)",
		}

		return env[key]
	}

	pr, pw := io.Pipe()
	defer func() {
		closeErr := pr.Close()
		if closeErr != nil {
			t.Errorf("failed to close pipe reader: %v", closeErr)
		}
	}()
	defer func() {
		closeErr := pw.Close()
		if closeErr != nil {
			t.Errorf("failed to close pipe writer: %v", closeErr)
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Run(ctx, getenv, pw)
	}()

	serverAddr := testutil.ServerAddress(t, pr)
	err = testutil.WaitForReady(ctx, t, 10*time.Second, fmt.Sprintf("http://%s/healthz", serverAddr))
	if err != nil {
		t.Fatalf("error waiting for server to be ready: %v", err)
	}

	// Create a quiz
	quizTitle := "Integration Test Quiz"
	quizSlug := "integration-test-quiz"
	quizDesc := "A quiz created by integration test"

	form := url.Values{}
	form.Add("title", quizTitle)
	form.Add("slug", quizSlug)
	form.Add("description", quizDesc)

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	var req *http.Request
	req, err = http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("http://%s/admin/quizzes", serverAddr),
		strings.NewReader(form.Encode()),
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

	location := resp.Header.Get("Location")
	if got, want := location, "/admin/quizzes/"; !strings.HasPrefix(got, want) {
		t.Errorf("got Location header %q, want prefix %q", got, want)
	}

	// Verify quiz exists in the list
	req, err = http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("http://%s/admin/quizzes", serverAddr),
		strings.NewReader(form.Encode()),
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
		fmt.Sprintf("http://%s%s", serverAddr, location),
		strings.NewReader(form.Encode()),
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

	// Shutdown server
	stop()
	select {
	case err = <-errCh:
		if err != nil {
			t.Errorf("run() returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("server failed to shutdown in time")
	}
}
