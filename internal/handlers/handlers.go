// Package handlers provides utility functions for HTTP servers.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// loggerCtxKey is the unexported context-key type for the request-scoped
// logger. A struct (not a string) so no other package can collide with it.
type loggerCtxKey struct{}

// WithLogger returns a copy of ctx carrying logger as the request-scoped
// logger. A middleware binds the stable correlation attrs (a request id, the
// player once known) on logger via [slog.Logger.With] and stashes it here, so
// every downstream line inherits them without restating them.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerCtxKey{}, logger)
}

// LoggerFromContext returns the request-scoped logger stashed by WithLogger,
// or [slog.Default] for a context that was never annotated (a handler invoked
// outside the middleware chain, e.g. in a unit test).
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerCtxKey{}).(*slog.Logger); ok {
		return l
	}

	return slog.Default()
}

const (
	base10    = 10
	int64Size = 64
)

// maxJSONBodySize caps the request body for /api/* JSON endpoints. 64 KiB is
// generous for the small request shapes the API accepts (a quiz ID, an option
// ID, a displayName) and denies an unauthenticated client the ability to
// exhaust memory by streaming a multi-megabyte body into [json.Decoder].
const maxJSONBodySize = 64 * 1024

// ErrNoSlugSeparator is returned by IDFromSlugID when the input contains no "-".
var ErrNoSlugSeparator = errors.New("no separator found in slug")

// ErrTrailingJSONData is returned by DecodeJSON when the body carries more than
// a single JSON value.
var ErrTrailingJSONData = errors.New("unexpected data after JSON value")

// IDFromString parses an int64 ID from the given string.
// returns 0 if the path value is empty.
func IDFromString(pathValue string) (int64, error) {
	if pathValue == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(pathValue, base10, int64Size)
	if err != nil {
		return 0, fmt.Errorf("error parsing %q: %w", pathValue, err)
	}

	return id, nil
}

// IDFromSlugID extracts an int64 ID from a slug-id string such as "my-quiz-123".
// It splits on the last "-" and parses the suffix as int64.
// Returns an error if there is no "-" in the string or the suffix is not a valid int64.
func IDFromSlugID(s string) (int64, error) {
	i := strings.LastIndex(s, "-")
	if i < 0 {
		return 0, fmt.Errorf("%w: %q", ErrNoSlugSeparator, s)
	}

	suffix := s[i+1:]
	id, err := strconv.ParseInt(suffix, base10, int64Size)
	if err != nil {
		return 0, fmt.Errorf("error parsing id from slug %q: %w", s, err)
	}

	return id, nil
}

// ParseIDFromSlugPath parses an int64 ID from a slug-id path parameter.
// It calls IDFromSlugID on the path value identified by s.
// Returns the parsed ID and true on success, or renders a 400 error and returns 0, false on failure.
func ParseIDFromSlugPath(w http.ResponseWriter, r *http.Request, logger *slog.Logger, s string) (int64, bool) {
	pathValue := r.PathValue(s)

	id, err := IDFromSlugID(pathValue)
	if err != nil {
		msg := "error parsing " + s
		logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
		http.Error(w, msg, http.StatusBadRequest)

		return 0, false
	}

	return id, true
}

// ParseIDFromPath parses an int64 ID from the given path value.
// An empty path value returns (0, true) so handlers shared between a
// create route (no ID segment) and an edit route (with one) can treat
// the zero result as "create". A present path value must parse to a
// positive integer; a non-numeric or non-positive value renders a 400
// and returns (0, false) so callers never act on a zero or negative ID
// that was actually supplied (e.g. "/0" or "/-1").
func ParseIDFromPath(w http.ResponseWriter, r *http.Request, logger *slog.Logger, s string) (int64, bool) {
	pathValue := r.PathValue(s)
	if pathValue == "" {
		return 0, true
	}

	id, err := IDFromString(pathValue)
	if err != nil || id <= 0 {
		msg := "error parsing " + s
		logger.ErrorContext(r.Context(), msg, slog.String("value", pathValue), slog.Any("err", err))
		http.Error(w, msg, http.StatusBadRequest)

		return 0, false
	}

	return id, true
}

// EncodeJSON encodes v to JSON, sets status, and writes it to w.
func EncodeJSON[T any](w http.ResponseWriter, statusCode int, v T) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		return fmt.Errorf("failed to encode json: %w", err)
	}

	return nil
}

// DecodeJSON decodes a single JSON value from r, capping the body at
// maxJSONBodySize. Passing w lets [http.MaxBytesReader] signal the cap to the
// client; the returned error surfaces as a 400 in the caller. Unknown fields
// and any data after the first JSON value are rejected so a malformed or
// smuggled payload fails loudly rather than decoding partially.
func DecodeJSON[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var v T
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodySize)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, fmt.Errorf("failed to decode json: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return v, fmt.Errorf("%w: unexpected trailing data", ErrTrailingJSONData)
	}

	return v, nil
}
