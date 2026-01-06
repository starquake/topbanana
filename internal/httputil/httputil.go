// Package httputil provides utility functions for HTTP servers.
package httputil

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
)

const (
	base10    = 10
	int64Size = 64
)

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
