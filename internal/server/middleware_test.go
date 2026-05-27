package server_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/server"
)

func TestLogRequests_LogsMethodPathStatus(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	handler := ExportLogRequests(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test-path", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	log := buf.String()
	for _, want := range []string{"method=GET", "path=/test-path", "status=201", "duration="} {
		if !strings.Contains(log, want) {
			t.Errorf("log output %q missing %q", log, want)
		}
	}
}

func TestLogRequests_DefaultsTo200WhenWriteHeaderNotCalled(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	handler := ExportLogRequests(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/ping", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := buf.String(), "status=200"; !strings.Contains(got, want) {
		t.Errorf("log output %q missing %q", got, want)
	}
}

func TestLogRequests_LogsErrorStatus(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	handler := ExportLogRequests(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/bad", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := buf.String(), "status=400"; !strings.Contains(got, want) {
		t.Errorf("log output %q missing %q", got, want)
	}
}

func TestRecoverPanic_LogsAndReturns500(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	handler := ExportRecoverPanic(logger, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("kaboom - synthetic test panic")
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/boom", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusInternalServerError; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	log := buf.String()
	for _, want := range []string{"handler panic recovered", "kaboom", "path=/boom", "method=GET", "stack="} {
		if !strings.Contains(log, want) {
			t.Errorf("log output missing %q\nfull log:\n%s", want, log)
		}
	}
	if got, want := log, "level=ERROR"; !strings.Contains(got, want) {
		t.Errorf("log level missing %q\nfull log:\n%s", want, log)
	}
}

func TestRecoverPanic_IgnoresErrAbortHandler(t *testing.T) {
	t.Parallel()

	// http.ErrAbortHandler is the documented sentinel for an
	// intentional abort. recoverPanic must NOT promote it to a 500
	// or log at Error - that would turn a feature into noise.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	handler := ExportRecoverPanic(logger, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/abort", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d (ErrAbortHandler should not write 500)", got, want)
	}
	if got, want := buf.String(), "level=ERROR"; strings.Contains(got, want) {
		t.Errorf("log contains %q, should not (ErrAbortHandler is informational)\nfull log:\n%s", want, got)
	}
	if got, want := buf.String(), "handler aborted"; !strings.Contains(got, want) {
		t.Errorf("log output missing %q\nfull log:\n%s", want, got)
	}
}

func TestLogRequests_UnwrapAllowsFlush(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	handler := ExportLogRequests(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rc := http.NewResponseController(w)
		if err := rc.Flush(); err != nil {
			t.Errorf("Flush() via Unwrap error: %v", err)
		}
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/flush", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !rec.Flushed {
		t.Error("expected recorder to have been flushed via Unwrap")
	}
}
