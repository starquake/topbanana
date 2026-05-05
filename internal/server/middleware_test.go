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
