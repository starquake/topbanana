package integration_test

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"
)

// TestAdminQuizList_RendersPlayCount pins the #891 admin-list footer: each
// quiz row carries its play_count alongside the question count. Seeding the
// quiz with the store starts the counter at 0; bumping it through the SQL
// surface the migration added must surface as the rendered figure.
func TestAdminQuizList_RendersPlayCount(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "play-count-host", "play-count-pass-123")

	qz := seedSoloQuiz(ctx, t, setup.Stores.Quizzes, "play-count-list")

	db, err := sql.Open("sqlite", setup.DBURI)
	if err != nil {
		t.Fatalf("open db err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})
	if _, err := db.ExecContext(
		ctx, "UPDATE quizzes SET play_count = 7 WHERE id = ?", qz.ID,
	); err != nil {
		t.Fatalf("seed play_count err = %v, want nil", err)
	}

	body := readBody(ctx, t, host, baseURL+"/admin/quizzes")
	if !strings.Contains(body, qz.Title) {
		t.Fatalf("admin list body missing quiz title %q", qz.Title)
	}
	// The footer renders the counter as a bold number inside an
	// inline-flex span tagged with the "Times played" tooltip; assert the
	// figure and the label without coupling to the exact SVG markup.
	if want := `>7</strong>`; !strings.Contains(body, want) {
		t.Errorf("admin list body missing rendered play_count %q", want)
	}
	if want := `Times played`; !strings.Contains(body, want) {
		t.Errorf("admin list body missing %q label", want)
	}
}
