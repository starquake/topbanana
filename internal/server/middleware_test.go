package server_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/server"
)

// withReqLogger wraps next so requests carry a request-scoped logger drawn
// from logger, exactly as the production chain does. logRequests and
// recoverPanic pull that logger off the context with loggerFrom, so a test
// can assert the lines they emit by reading buf - and the requestLogger
// wrapper means every line also carries the generated requestId.
func withReqLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return ExportRequestLogger(logger, next)
}

func TestLogRequests_LogsMethodPathStatus(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	handler := withReqLogger(logger, ExportLogRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test-path", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	log := buf.String()
	for _, want := range []string{"method=GET", "path=/test-path", "status=201", "duration=", "requestId="} {
		if !strings.Contains(log, want) {
			t.Errorf("log output %q missing %q", log, want)
		}
	}
}

func TestLogRequests_DefaultsTo200WhenWriteHeaderNotCalled(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	handler := withReqLogger(logger, ExportLogRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})))

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

	handler := withReqLogger(logger, ExportLogRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	})))

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

	handler := withReqLogger(logger, ExportRecoverPanic(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("kaboom - synthetic test panic")
	})))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/boom", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusInternalServerError; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	log := buf.String()
	for _, want := range []string{"handler panic recovered", "kaboom", "path=/boom", "method=GET", "stack=", "requestId="} {
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

	handler := withReqLogger(logger, ExportRecoverPanic(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic(http.ErrAbortHandler)
	})))

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

	handler := withReqLogger(logger, ExportLogRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rc := http.NewResponseController(w)
		if err := rc.Flush(); err != nil {
			t.Errorf("Flush() via Unwrap error: %v", err)
		}
	})))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/flush", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !rec.Flushed {
		t.Error("expected recorder to have been flushed via Unwrap")
	}
}

// TestRequestLogger_BindsRequestIDOnContextLogger pins that the handler can
// pull a logger off the request context whose lines carry the bound request
// id, so any handler line inherits the id without restating it. The capturing
// handler honours WithAttrs, so the With-bound requestId surfaces on the
// record.
func TestRequestLogger_BindsRequestIDOnContextLogger(t *testing.T) {
	t.Parallel()

	logs := newCaptureHandler()

	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ExportLoggerFrom(r.Context()).InfoContext(r.Context(), "handler line")
	})
	handler := withReqLogger(slog.New(logs), inner)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/scoped", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	attrs := logs.attrsFor(t, "handler line")
	if got := attrs["requestId"].String(); got == "" {
		t.Errorf("handler line missing a non-empty requestId attr (attrs: %v)", attrs)
	}
}

// TestRequestLogger_DistinctIDsPerRequest pins that two requests through the
// same wrapped handler get different request ids, so lines from concurrent
// requests can be told apart.
func TestRequestLogger_DistinctIDsPerRequest(t *testing.T) {
	t.Parallel()

	logs := newCaptureHandler()
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ExportLoggerFrom(r.Context()).InfoContext(r.Context(), "tick")
	})
	handler := withReqLogger(slog.New(logs), inner)

	for range 2 {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/scoped", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	ids := logs.attrValuesFor("tick", "requestId")
	if got, want := len(ids), 2; got != want {
		t.Fatalf("captured %d %q lines, want %d", got, "tick", want)
	}
	if ids[0] == "" || ids[1] == "" {
		t.Errorf("requestId attrs = %q, %q, want both non-empty", ids[0], ids[1])
	}
	if ids[0] == ids[1] {
		t.Errorf("requestId attrs = %q, %q, want distinct", ids[0], ids[1])
	}
}

// TestLoggerFrom_FallsBackToDefault pins that loggerFrom on a context with no
// request-scoped logger returns a usable logger rather than nil, so a handler
// invoked outside the middleware chain still logs.
func TestLoggerFrom_FallsBackToDefault(t *testing.T) {
	t.Parallel()

	if got := ExportLoggerFrom(context.Background()); got == nil {
		t.Error("loggerFrom on a bare context = nil, want a usable logger")
	}
}
