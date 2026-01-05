//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	. "github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/testutil"
)

func TestAdmin_Integration(t *testing.T) {
	t.Parallel()

	var err error

	ctx, stop := testutil.SignalCtx(t)

	stdout := testutil.NewTestWriter(t)

	dbURI, cleanup := dbtest.SetupTestDB(t)
	defer cleanup()

	getenv := func(key string) string {
		env := map[string]string{
			"HOST":   "localhost",
			"PORT":   "0", // Let the OS choose an available port
			"DB_URI": dbURI,
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
		errCh <- Run(ctx, getenv, stdout, ln)
	}()

	serverAddr := ln.Addr().String()
	err = testutil.WaitForReady(ctx, t, 10*time.Second, fmt.Sprintf("http://%s/healthz", serverAddr))
	if err != nil {
		t.Fatalf("error waiting for server to be ready: %v", err)
	}

	// Create a quiz
	quizTitle := "Integration Test Quiz"
	quizSlug := "integration-test-quiz"
	quizDesc := "A quiz created by integration test"

	quizForm := url.Values{}
	quizForm.Add("title", quizTitle)
	quizForm.Add("slug", quizSlug)
	quizForm.Add("description", quizDesc)

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

	questionForm := url.Values{}
	questionForm.Add("text", questionText)
	questionForm.Add("position", "10")
	questionForm.Add("option[0].text", questionOption1)
	questionForm.Add("option[1].text", questionOption2)
	questionForm.Add("option[1].correct", "on")
	questionForm.Add("option[2].text", questionOption3)
	questionForm.Add("option[3].text", questionOption4)

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
		body, _ := io.ReadAll(resp.Body)
		t.Logf("Response body: %s", string(body))
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
