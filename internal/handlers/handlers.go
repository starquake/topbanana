// Package handlers provides utility functions for HTTP servers.
package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

const (
	base10    = 10
	int64Size = 64
)

// ErrNoSlugSeparator is returned by IDFromSlugID when the input contains no "-".
var ErrNoSlugSeparator = errors.New("no separator found in slug")

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
// It returns the parsed ID and true if the parsing was successful.
// It returns 0 and true if the path value is empty.
// It renders a 400 error page if the path value cannot be parsed.
func ParseIDFromPath(w http.ResponseWriter, r *http.Request, logger *slog.Logger, s string) (int64, bool) {
	pathValue := r.PathValue(s)
	if pathValue == "" {
		return 0, true
	}

	id, err := IDFromString(pathValue)
	if err != nil {
		msg := "error parsing " + s
		logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
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

// DecodeJSON decodes JSON from r.
func DecodeJSON[T any](r *http.Request) (T, error) {
	var v T
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		return v, fmt.Errorf("failed to decode json: %w", err)
	}

	return v, nil
}
