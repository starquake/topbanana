package server

import (
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

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
// field (#346). Mount as the outermost wrapper so it covers
// logRequests too — otherwise the panic kills the response writer
// before logRequests can record the status.
func recoverPanic(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// http.ErrAbortHandler is the documented sentinel for
			// "I'm aborting this response on purpose"; net/http treats
			// it as silent — log at Warn but skip the stack dump and
			// the 500. Anything else is a real bug. recover() returns
			// `any`, so type-assert to error before errors.Is.
			if recErr, ok := rec.(error); ok && errors.Is(recErr, http.ErrAbortHandler) {
				logger.WarnContext(r.Context(), "handler aborted via http.ErrAbortHandler",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
				)

				return
			}
			logger.ErrorContext(r.Context(), "handler panic recovered",
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

func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rw, r)
		logger.InfoContext(r.Context(), "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rw.status),
			slog.String("duration", time.Since(start).String()),
		)
	})
}
