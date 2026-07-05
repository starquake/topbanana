package integration_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
)

// TestQuizIsolation_Integration covers #1207: a Host sees and opens only the
// quizzes they created, while an Admin keeps seeing every quiz. Host A and Host
// B each create a quiz; the admin quiz list and the read-only quiz view are then
// probed across the three roles. A non-owner Host must get an opaque 404 on
// another host's quiz (never a 403 that would reveal it exists), and the admin
// must see and open both.
func TestQuizIsolation_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		// The first registrant consumes the first-registrant Admin promotion;
		// putting the same address in ADMIN_EMAILS keeps the boss an Admin so it
		// can stand in for the "admin sees all" role.
		"ADMIN_EMAILS": "isolation-boss@example.test",
	})
	baseURL := srv.BaseURL

	admin := registerAdminClient(ctx, t, baseURL, srv.DBURI, "isolation-boss")
	hostA := registerAdminClient(ctx, t, baseURL, srv.DBURI, "isolation-host-a")
	hostB := registerAdminClient(ctx, t, baseURL, srv.DBURI, "isolation-host-b")
	makeHost(ctx, t, srv.DBURI, "isolation-host-a")
	makeHost(ctx, t, srv.DBURI, "isolation-host-b")

	quizA := createQuizAs(ctx, t, hostA, baseURL, "Isolation Host A Quiz")
	quizB := createQuizAs(ctx, t, hostB, baseURL, "Isolation Host B Quiz")

	t.Run("host list shows only own quizzes", func(t *testing.T) {
		t.Parallel()
		body := getQuizListBody(ctx, t, hostB, baseURL)
		if !strings.Contains(body, "Isolation Host B Quiz") {
			t.Error("host B quiz list missing host B's own quiz")
		}
		if strings.Contains(body, "Isolation Host A Quiz") {
			t.Error("host B quiz list shows host A's quiz, want it scoped out")
		}
	})

	t.Run("host cannot view another host's quiz", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, hostB, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizA))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("host B view of host A quiz status = %d, want %d", got, want)
		}
	})

	t.Run("host can view own quiz", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, hostA, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizA))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Errorf("host A view of own quiz status = %d, want %d", got, want)
		}
	})

	t.Run("admin list shows all quizzes", func(t *testing.T) {
		t.Parallel()
		body := getQuizListBody(ctx, t, admin, baseURL)
		if !strings.Contains(body, "Isolation Host A Quiz") {
			t.Error("admin quiz list missing host A's quiz")
		}
		if !strings.Contains(body, "Isolation Host B Quiz") {
			t.Error("admin quiz list missing host B's quiz")
		}
	})

	t.Run("admin can view any host's quiz", func(t *testing.T) {
		t.Parallel()
		for _, id := range []int64{quizA, quizB} {
			func() {
				resp := httpGet(ctx, t, admin, baseURL+fmt.Sprintf("/admin/quizzes/%d", id))
				defer closeBody(t, resp.Body)
				if got, want := resp.StatusCode, http.StatusOK; got != want {
					t.Errorf("admin view of quiz %d status = %d, want %d", id, got, want)
				}
			}()
		}
	})
}

// TestQuizIsolation_HostPicker_Integration covers the live-quiz host picker half
// of #1207: the shared picker (GET /host/quizzes) lists only the host's own
// live-eligible quizzes, while an Admin sees every host's. Live quizzes owned by
// each host are seeded directly through the store, since a runnable picker card
// needs a published live quiz with at least one question.
func TestQuizIsolation_HostPicker_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"ADMIN_EMAILS":         "picker-boss@example.test",
	})
	baseURL := srv.BaseURL

	admin := registerAdminClient(ctx, t, baseURL, srv.DBURI, "picker-boss")
	hostA := registerAdminClient(ctx, t, baseURL, srv.DBURI, "picker-host-a")
	// Host B only owns a seeded quiz here; its client is never driven, so the
	// registration is just to create the player row hostBID resolves against.
	registerAdminClient(ctx, t, baseURL, srv.DBURI, "picker-host-b")
	makeHost(ctx, t, srv.DBURI, "picker-host-a")
	makeHost(ctx, t, srv.DBURI, "picker-host-b")

	hostAID := playerIDByDisplayName(ctx, t, srv.DBURI, "picker-host-a")
	hostBID := playerIDByDisplayName(ctx, t, srv.DBURI, "picker-host-b")

	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.
	seedOwnedLiveQuiz(ctx, t, stores.Quizzes, "Picker Host A Live", "picker-live-a", hostAID)
	seedOwnedLiveQuiz(ctx, t, stores.Quizzes, "Picker Host B Live", "picker-live-b", hostBID)

	t.Run("host picker lists only own live quizzes", func(t *testing.T) {
		t.Parallel()
		_, body := getHostQuizListHTML(ctx, t, hostA, baseURL)
		if !strings.Contains(body, "Picker Host A Live") {
			t.Error("host A picker missing host A's own live quiz")
		}
		if strings.Contains(body, "Picker Host B Live") {
			t.Error("host A picker shows host B's live quiz, want it scoped out")
		}
	})

	t.Run("admin picker lists all live quizzes", func(t *testing.T) {
		t.Parallel()
		_, body := getHostQuizListHTML(ctx, t, admin, baseURL)
		if !strings.Contains(body, "Picker Host A Live") {
			t.Error("admin picker missing host A's live quiz")
		}
		if !strings.Contains(body, "Picker Host B Live") {
			t.Error("admin picker missing host B's live quiz")
		}
	})
}

// getQuizListBody GETs /admin/quizzes on the given client and returns the HTML
// body. Fails the test on a non-200 status or a read error.
func getQuizListBody(ctx context.Context, t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	resp := httpGet(ctx, t, client, baseURL+"/admin/quizzes")
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("admin quiz list status = %d, want %d", got, want)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read admin quiz list body err = %v, want nil", err)
	}

	return string(body)
}

// seedOwnedLiveQuiz seeds a published mode='live' quiz with one question,
// attributed to ownerID, so the host picker offers it as a runnable card.
func seedOwnedLiveQuiz(
	ctx context.Context, t *testing.T, quizzes quiz.Store, title, slug string, ownerID int64,
) {
	t.Helper()
	qz := &quiz.Quiz{
		Title:             title,
		Slug:              slug,
		Description:       "hosted only",
		Published:         true,
		CreatedByPlayerID: ownerID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeLive,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "A", Correct: true}, {Text: "B"}}},
		},
	}
	if err := quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz %q err = %v, want nil", title, err)
	}
}
