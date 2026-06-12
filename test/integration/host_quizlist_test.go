package integration_test

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
)

// getHostQuizListHTML fetches GET /host/quizzes on the (host) client and
// returns the response status and body.
func getHostQuizListHTML(
	ctx context.Context, t *testing.T, client *http.Client, baseURL string,
) (int, string) {
	t.Helper()
	resp := httpGet(ctx, t, client, baseURL+"/host/quizzes")
	defer closeBody(t, resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read host quiz-list body: %v", err)
	}

	return resp.StatusCode, string(body)
}

// TestHostQuizList_ListsRunnableLiveQuizzes pins the host quiz picker (#889): a
// signed-in host GETs /host/quizzes and sees only the runnable quizzes - live
// mode with at least one question. A solo quiz and an empty live quiz are both
// filtered out, and each listed card carries the "Host this" form posting its
// quiz_id to /host.
func TestHostQuizList_ListsRunnableLiveQuizzes(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	live := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-list-live")
	solo := seedSoloQuiz(ctx, t, setup.Stores.Quizzes, "host-list-solo")
	emptyLive := &quiz.Quiz{
		Title:             "Empty live host-list",
		Slug:              "host-list-empty",
		Description:       "no questions yet",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeLive,
	}
	if err := setup.Stores.Quizzes.CreateQuiz(ctx, emptyLive); err != nil {
		t.Fatalf("CreateQuiz empty live err = %v, want nil", err)
	}

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-list-host", "host-list-pass-123")

	status, body := getHostQuizListHTML(ctx, t, host, baseURL)
	if got, want := status, http.StatusOK; got != want {
		t.Fatalf("host quiz-list status = %d, want %d", got, want)
	}

	// The runnable live quiz appears, with a form posting its quiz_id to /host.
	if !strings.Contains(body, live.Title) {
		t.Errorf("host quiz-list missing runnable live quiz title %q", live.Title)
	}
	if !strings.Contains(body, `action="/host"`) {
		t.Error(`host quiz-list missing form action="/host"`)
	}
	if want := `name="quiz_id" value="` + strconv.FormatInt(live.ID, 10) + `"`; !strings.Contains(body, want) {
		t.Errorf("host quiz-list missing hidden input %q", want)
	}
	if !strings.Contains(body, "Host this") {
		t.Error(`host quiz-list missing "Host this" action`)
	}

	// The solo quiz and the empty live quiz are both filtered out.
	if strings.Contains(body, solo.Title) {
		t.Errorf("host quiz-list shows solo quiz %q, want it filtered out", solo.Title)
	}
	if strings.Contains(body, emptyLive.Title) {
		t.Errorf("host quiz-list shows empty live quiz %q, want it filtered out", emptyLive.Title)
	}
}

// TestHostQuizList_GatesAnonymous pins the host gate on the picker: an
// unauthenticated GET /host/quizzes is bounced to login, the same as the other
// host GET routes (mirrors TestHostBigScreen_Authz).
func TestHostQuizList_GatesAnonymous(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	anon := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp := httpGet(ctx, t, anon, baseURL+"/host/quizzes")
	defer closeBody(t, resp.Body)

	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("anon host quiz-list status = %d, want %d", got, want)
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Errorf("anon host quiz-list redirect = %q, want /login", loc)
	}
}
