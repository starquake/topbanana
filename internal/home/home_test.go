package home_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/home"
)

// stubStore feeds the handler canned rows so the render path is the
// only thing under test. The two slice fields drive each fetch
// independently; the err fields exercise the degraded-render branch
// (the page should still render, with the failing section empty).
type stubStore struct {
	popular    []*PopularQuiz
	active     []*ActivePlayer
	popularErr error
	activeErr  error
}

func (s *stubStore) ListPopularQuizzes(_ context.Context) ([]*PopularQuiz, error) {
	return s.popular, s.popularErr
}

func (s *stubStore) ListMostActivePlayers(_ context.Context) ([]*ActivePlayer, error) {
	return s.active, s.activeErr
}

func TestHandle_RendersPopularAndActiveSections(t *testing.T) {
	t.Parallel()

	store := &stubStore{
		popular: []*PopularQuiz{
			{
				ID:          7,
				Title:       "Bananas of the World",
				Slug:        "bananas-of-the-world",
				Description: "Twenty rounds on cultivars.",
				PlayCount:   5,
			},
			{ID: 9, Title: "Capital Cities", Slug: "capital-cities", Description: "Quickfire geography.", PlayCount: 3},
		},
		active: []*ActivePlayer{
			{ID: 1, Username: "alice", FinishedCount: 4},
			{ID: 2, Username: "bob", FinishedCount: 2},
		},
	}

	body := serve(t, store)

	for _, want := range []string{
		`<title>Top Banana!</title>`,
		`href="/play/bananas-of-the-world-7"`,
		`Bananas of the World`,
		`5 plays`,
		`href="/play/capital-cities-9"`,
		`3 plays`,
		`alice`,
		`bob`,
		`4 quizzes`,
		`2 quizzes`,
		`href="/admin"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestHandle_SingularPlayAndQuizPluralization(t *testing.T) {
	t.Parallel()

	store := &stubStore{
		popular: []*PopularQuiz{
			{ID: 11, Title: "Solo", Slug: "solo", PlayCount: 1},
		},
		active: []*ActivePlayer{
			{ID: 1, Username: "carol", FinishedCount: 1},
		},
	}
	body := serve(t, store)

	// Body contains "1 play" but never "1 plays" or "1 quizzes" - proves
	// the {{if eq .PlayCount 1}}...{{else}}...{{end}} branch fired.
	if !strings.Contains(body, "1 play") {
		t.Errorf("body missing singular play form %q", "1 play")
	}
	if !strings.Contains(body, "1 quiz") {
		t.Errorf("body missing singular quiz form %q", "1 quiz")
	}
	if strings.Contains(body, "1 plays") || strings.Contains(body, "1 quizzes") {
		t.Error("body contained plural form alongside count of 1")
	}
}

func TestHandle_EmptyState(t *testing.T) {
	t.Parallel()

	body := serve(t, &stubStore{})

	for _, want := range []string{
		"No quizzes have been played yet",
		"No finishers yet",
		`href="/admin"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestHandle_StoreErrorsDegradeToEmptyState(t *testing.T) {
	t.Parallel()

	store := &stubStore{
		popularErr: errors.New("boom"),
		activeErr:  errors.New("boom"),
	}
	rec := httptest.NewRecorder()
	handler := Handle(slog.New(slog.DiscardHandler), store, nil, nil)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	body := rec.Body.String()
	if got, want := body, "No quizzes have been played yet"; !strings.Contains(got, want) {
		t.Errorf("body missing empty-state %q", want)
	}
	if got, want := body, `href="/admin"`; !strings.Contains(got, want) {
		t.Errorf("body missing admin link %q", want)
	}
}

func TestHandle_TruncatesToTopN(t *testing.T) {
	t.Parallel()

	// Feed more than the page-level cap of 6 to confirm the handler
	// slices instead of dumping the full list into the template.
	popular := make([]*PopularQuiz, 0, 10)
	for i := range 10 {
		popular = append(popular, &PopularQuiz{
			ID:        int64(i + 1),
			Title:     "T" + string(rune('A'+i)),
			Slug:      "t" + string(rune('a'+i)),
			PlayCount: 10 - i,
		})
	}
	body := serve(t, &stubStore{popular: popular})

	// The 7th entry onward must not be in the response.
	if got, want := body, "TG"; strings.Contains(got, want) {
		t.Errorf("body should not contain entry past the cap %q", want)
	}
	// The first 6 should be present.
	for _, want := range []string{"TA", "TB", "TC", "TD", "TE", "TF"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing in-cap entry %q", want)
		}
	}
}

func serve(t *testing.T, store *stubStore) string {
	t.Helper()

	handler := Handle(slog.New(slog.DiscardHandler), store, nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Content-Type"), "text/html"; !strings.HasPrefix(got, want) {
		t.Errorf("Content-Type = %q, want prefix %q", got, want)
	}

	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read body err = %v, want nil", err)
	}

	return string(body)
}
