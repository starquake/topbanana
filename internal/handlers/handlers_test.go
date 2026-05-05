package handlers_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/handlers"
)

func TestIDFromString(t *testing.T) {
	t.Parallel()

	t.Run("valid id", func(t *testing.T) {
		t.Parallel()
		id, err := IDFromString("42")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := id, int64(42); got != want {
			t.Errorf("IDFromString = %d, want %d", got, want)
		}
	})

	t.Run("empty string returns zero", func(t *testing.T) {
		t.Parallel()
		id, err := IDFromString("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := id, int64(0); got != want {
			t.Errorf("IDFromString = %d, want %d", got, want)
		}
	})

	t.Run("invalid string returns error", func(t *testing.T) {
		t.Parallel()
		_, err := IDFromString("not-a-number")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got, want := err.Error(), "error parsing"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("negative id", func(t *testing.T) {
		t.Parallel()
		id, err := IDFromString("-5")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := id, int64(-5); got != want {
			t.Errorf("IDFromString = %d, want %d", got, want)
		}
	})
}

func TestParseIDFromPath(t *testing.T) {
	t.Parallel()

	t.Run("valid id", func(t *testing.T) {
		t.Parallel()
		var gotID int64
		var gotOK bool
		mux := http.NewServeMux()
		mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, r *http.Request) {
			gotID, gotOK = ParseIDFromPath(w, r, slog.Default(), "id")
		})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/items/42", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)
		if got, want := gotOK, true; got != want {
			t.Errorf("ok = %v, want %v", got, want)
		}
		if got, want := gotID, int64(42); got != want {
			t.Errorf("id = %d, want %d", got, want)
		}
	})

	t.Run("missing path param returns zero and true", func(t *testing.T) {
		t.Parallel()
		var gotID int64
		var gotOK bool
		mux := http.NewServeMux()
		mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, r *http.Request) {
			gotID, gotOK = ParseIDFromPath(w, r, slog.Default(), "nonexistent")
		})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/items/42", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)
		if got, want := gotOK, true; got != want {
			t.Errorf("ok = %v, want %v", got, want)
		}
		if got, want := gotID, int64(0); got != want {
			t.Errorf("id = %d, want %d", got, want)
		}
	})

	t.Run("non-numeric id returns false and 400", func(t *testing.T) {
		t.Parallel()
		var gotOK bool
		mux := http.NewServeMux()
		mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, r *http.Request) {
			_, gotOK = ParseIDFromPath(w, r, slog.Default(), "id")
		})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/items/not-a-number", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if got, want := gotOK, false; got != want {
			t.Errorf("ok = %v, want %v", got, want)
		}
		if got, want := w.Code, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}

func TestIDFromSlugID(t *testing.T) {
	t.Parallel()

	t.Run("slug with single id segment", func(t *testing.T) {
		t.Parallel()
		id, err := IDFromSlugID("my-quiz-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := id, int64(123); got != want {
			t.Errorf("IDFromSlugID = %d, want %d", got, want)
		}
	})

	t.Run("slug with multiple segments", func(t *testing.T) {
		t.Parallel()
		id, err := IDFromSlugID("my-quiz-title-456")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := id, int64(456); got != want {
			t.Errorf("IDFromSlugID = %d, want %d", got, want)
		}
	})

	t.Run("non-numeric suffix returns error", func(t *testing.T) {
		t.Parallel()
		_, err := IDFromSlugID("no-numbers")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got, want := err.Error(), "error parsing id from slug"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})

	t.Run("empty string returns error", func(t *testing.T) {
		t.Parallel()
		_, err := IDFromSlugID("")
		if got, want := err, ErrNoSlugSeparator; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("trailing dash returns error", func(t *testing.T) {
		t.Parallel()
		_, err := IDFromSlugID("just-no-id-")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got, want := err.Error(), "error parsing id from slug"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestParseIDFromSlugPath(t *testing.T) {
	t.Parallel()

	t.Run("valid slug id", func(t *testing.T) {
		t.Parallel()
		var gotID int64
		var gotOK bool
		mux := http.NewServeMux()
		mux.HandleFunc("GET /quizzes/{slugID}", func(w http.ResponseWriter, r *http.Request) {
			gotID, gotOK = ParseIDFromSlugPath(w, r, slog.Default(), "slugID")
		})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/quizzes/my-quiz-42", nil)
		mux.ServeHTTP(httptest.NewRecorder(), req)
		if got, want := gotOK, true; got != want {
			t.Errorf("ok = %v, want %v", got, want)
		}
		if got, want := gotID, int64(42); got != want {
			t.Errorf("id = %d, want %d", got, want)
		}
	})

	t.Run("missing path param returns false and 400", func(t *testing.T) {
		t.Parallel()
		var gotOK bool
		mux := http.NewServeMux()
		mux.HandleFunc("GET /quizzes/{slugID}", func(w http.ResponseWriter, r *http.Request) {
			_, gotOK = ParseIDFromSlugPath(w, r, slog.Default(), "nonexistent")
		})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/quizzes/my-quiz-42", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if got, want := gotOK, false; got != want {
			t.Errorf("ok = %v, want %v", got, want)
		}
		if got, want := w.Code, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("non-numeric suffix returns false and 400", func(t *testing.T) {
		t.Parallel()
		var gotOK bool
		mux := http.NewServeMux()
		mux.HandleFunc("GET /quizzes/{slugID}", func(w http.ResponseWriter, r *http.Request) {
			_, gotOK = ParseIDFromSlugPath(w, r, slog.Default(), "slugID")
		})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/quizzes/no-numbers", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if got, want := gotOK, false; got != want {
			t.Errorf("ok = %v, want %v", got, want)
		}
		if got, want := w.Code, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}

func TestEncodeJSON(t *testing.T) {
	t.Parallel()

	t.Run("encodes value with correct content type and status", func(t *testing.T) {
		t.Parallel()
		type response struct {
			Name string `json:"name"`
		}
		w := httptest.NewRecorder()
		err := EncodeJSON(w, http.StatusOK, response{Name: "test"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := w.Code, http.StatusOK; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		if got, want := w.Header().Get("Content-Type"), "application/json"; got != want {
			t.Errorf("Content-Type = %q, want %q", got, want)
		}
		var res response
		if err = json.NewDecoder(w.Body).Decode(&res); err != nil {
			t.Fatalf("failed to decode response body: %v", err)
		}
		if got, want := res.Name, "test"; got != want {
			t.Errorf("res.Name = %q, want %q", got, want)
		}
	})

	t.Run("uses the provided status code", func(t *testing.T) {
		t.Parallel()
		w := httptest.NewRecorder()
		err := EncodeJSON(w, http.StatusCreated, struct{}{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := w.Code, http.StatusCreated; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}

func TestDecodeJSON(t *testing.T) {
	t.Parallel()

	t.Run("decodes valid JSON body", func(t *testing.T) {
		t.Parallel()
		type request struct {
			Name string `json:"name"`
		}
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(`{"name":"test"}`))
		req, err := DecodeJSON[request](r)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := req.Name, "test"; got != want {
			t.Errorf("req.Name = %q, want %q", got, want)
		}
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		t.Parallel()
		type request struct {
			Name string `json:"name"`
		}
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(`not json`))
		_, err := DecodeJSON[request](r)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got, want := err.Error(), "failed to decode json"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}
