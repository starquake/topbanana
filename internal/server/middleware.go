package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/starquake/topbanana/internal/handlers"
)

// loggerFrom returns the request-scoped logger stashed on ctx by
// requestLogger, or a usable default for a context that was never annotated.
// A thin alias over handlers.LoggerFromContext so the server middleware reads
// naturally at every call site.
func loggerFrom(ctx context.Context) *slog.Logger {
	return handlers.LoggerFromContext(ctx)
}

// requestIDBytes is the entropy width of a generated request id. Eight bytes
// (16 hex chars) keeps ids distinct across a game night's worth of requests
// while staying short enough to eyeball in a log line.
const requestIDBytes = 8

// newRequestID returns a random hex request id. [crypto/rand.Read] does not
// return a short read or error on the platforms we target, so the error
// path is defensive only; an empty id still yields a usable (if unscoped)
// log line rather than failing the request.
func newRequestID() string {
	var b [requestIDBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}

	return hex.EncodeToString(b[:])
}

// requestLogger derives a per-request logger that carries a generated
// request id (bound once via [slog.Logger.With]) and stashes it on the request
// context, so every downstream line - the access log, the panic log, and
// any handler that pulls it with loggerFrom - inherits the id without
// repeating it. Mount it as the outermost wrapper so the id is bound
// before recoverPanic and logRequests run.
func requestLogger(base *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqLogger := base.With(slog.String("requestId", newRequestID()))
		ctx := handlers.WithLogger(r.Context(), reqLogger)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type responseWriter struct {
	http.ResponseWriter

	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// recoverPanic wraps the handler chain so a panic in any downstream
// handler becomes an Error-level slog entry (with the stack trace) on
// the request's context plus a generic 500 to the client, instead of
// the stdlib's default-logger stderr dump that loses every request
// field (#346). A panic unwinds straight past logRequests without its
// post-call log line firing, so this recover is the only entry that
// records the request when a handler panics. Mount it just inside
// requestLogger so the recover happens before the stdlib server sees the
// panic, yet still draws its line from the request-scoped logger (carrying
// the request id) via loggerFrom.
func recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := loggerFrom(ctx)
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// http.ErrAbortHandler is the documented sentinel for
			// "I'm aborting this response on purpose"; net/http treats
			// it as silent - log at Warn but skip the stack dump and
			// the 500. Anything else is a real bug. recover() returns
			// `any`, so type-assert to error before errors.Is.
			if recErr, ok := rec.(error); ok && errors.Is(recErr, http.ErrAbortHandler) {
				logger.WarnContext(ctx, "handler aborted via http.ErrAbortHandler",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
				)

				return
			}
			logger.ErrorContext(ctx, "handler panic recovered",
				slog.Any("panic", rec),
				slog.String("stack", string(debug.Stack())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
			)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rw, r)
		loggerFrom(r.Context()).InfoContext(r.Context(), "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rw.status),
			slog.String("duration", time.Since(start).String()),
		)
	})
}
