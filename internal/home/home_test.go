package home_test

import (
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/game"
	. "github.com/starquake/topbanana/internal/home"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// seededAdminID is the player row the migrations insert; quizzes need a
// creator and this id is guaranteed present on a freshly migrated DB.
const seededAdminID int64 = 1

// serve drives the real Handle handler against the real home store and
// returns the rendered body. viewer and csrfToken stay nil: every case
// here renders the anonymous page, and Handle treats both as optional.
func serve(t *testing.T, db *sql.DB) string {
	t.Helper()

	stores := store.New(db, slog.New(slog.DiscardHandler))
	handler := Handle(slog.New(slog.DiscardHandler), stores.Home, nil, nil)
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

// createQuiz inserts a public quiz with the given questions and returns
// it with its id populated.
func createQuiz(t *testing.T, quizzes quiz.Store, title, slug, desc string, questions []*quiz.Question) *quiz.Quiz {
	t.Helper()

	qz := &quiz.Quiz{
		Title:             title,
		Slug:              slug,
		Description:       desc,
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Questions:         questions,
	}
	if err := quizzes.CreateQuiz(t.Context(), qz); err != nil {
		t.Fatalf("CreateQuiz %q err = %v, want nil", slug, err)
	}

	return qz
}

// oneQuestion returns a single-question quiz body so a quiz is playable
// (the home queries gate on at least one question).
func oneQuestion() []*quiz.Question {
	return []*quiz.Question{
		{Text: "Q", Position: 1, Options: []*quiz.Option{{Text: "a", Correct: true}}},
	}
}

// finishGameFor seeds a finished game for the (player, quiz) pair: game +
// participant + one game_question per quiz question. Issuing every
// question is what the home queries treat as a finished play. The quiz
// must already have its questions loaded (CreateQuiz populates them).
func finishGameFor(t *testing.T, games game.Store, playerID int64, qz *quiz.Quiz) {
	t.Helper()

	g := &game.Game{QuizID: qz.ID}
	if err := games.CreateGame(t.Context(), g); err != nil {
		t.Fatalf("CreateGame err = %v, want nil", err)
	}
	if err := games.CreateParticipant(t.Context(), &game.Participant{
		GameID: g.ID, PlayerID: playerID, QuizID: qz.ID,
	}); err != nil {
		t.Fatalf("CreateParticipant err = %v, want nil", err)
	}

	now := time.Now()
	for _, qs := range qz.Questions {
		if err := games.CreateQuestion(t.Context(), &game.Question{
			GameID:     g.ID,
			QuestionID: qs.ID,
			StartedAt:  now,
			ExpiredAt:  now.Add(10 * time.Second),
		}); err != nil {
			t.Fatalf("CreateQuestion err = %v, want nil", err)
		}
	}
}

// claimedPlayer creates a player whose displayName is claimed, so the
// most-active-players query (which filters on displayName_claimed = 1)
// surfaces them.
func claimedPlayer(t *testing.T, players auth.PlayerStore, name string) *auth.Player {
	t.Helper()

	p, err := players.CreatePlayer(t.Context(), name, name+"@example.test", "hash", auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer %q err = %v, want nil", name, err)
	}

	return p
}

func playHref(qz *quiz.Quiz) string {
	return fmt.Sprintf(`href="/play/%s-%d"`, qz.Slug, qz.ID)
}

func TestHandle_RendersPopularAndActiveSections(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	stores := store.New(db, slog.New(slog.DiscardHandler))

	// bananas: 5 plays (5 distinct players, one-attempt-per-pair rule).
	// capitals: 3 plays. alice tops the active list with 4 finishes
	// across both quizzes; bob has 2.
	bananas := createQuiz(t, stores.Quizzes, "Bananas of the World", "bananas-of-the-world",
		"Twenty rounds on cultivars.", oneQuestion())
	capitals := createQuiz(t, stores.Quizzes, "Capital Cities", "capital-cities",
		"Quickfire geography.", oneQuestion())

	alice := claimedPlayer(t, stores.Players, "alice")
	bob := claimedPlayer(t, stores.Players, "bob")
	carol := claimedPlayer(t, stores.Players, "carol")
	dave := claimedPlayer(t, stores.Players, "dave")
	erin := claimedPlayer(t, stores.Players, "erin")

	// bananas: alice, bob, carol, dave, erin = 5 plays.
	for _, p := range []*auth.Player{alice, bob, carol, dave, erin} {
		finishGameFor(t, stores.Games, p.ID, bananas)
	}
	// capitals: alice, bob, carol = 3 plays.
	for _, p := range []*auth.Player{alice, bob, carol} {
		finishGameFor(t, stores.Games, p.ID, capitals)
	}

	// alice finishes bananas + capitals = 2; to reach the "4 quizzes"
	// pill the active count must be 4. Distinct quizzes are required per
	// the one-attempt rule, so add two more single-play quizzes alice
	// finishes. They each have only her play, ranking below the two
	// popular quizzes, but bump her finished_count to 4.
	extra1 := createQuiz(t, stores.Quizzes, "Extra One", "extra-one", "", oneQuestion())
	extra2 := createQuiz(t, stores.Quizzes, "Extra Two", "extra-two", "", oneQuestion())
	finishGameFor(t, stores.Games, alice.ID, extra1)
	finishGameFor(t, stores.Games, alice.ID, extra2)
	// bob finishes one more single-play quiz to reach "2 quizzes".
	extra3 := createQuiz(t, stores.Quizzes, "Extra Three", "extra-three", "", oneQuestion())
	finishGameFor(t, stores.Games, bob.ID, extra3)

	body := serve(t, db)

	for _, want := range []string{
		`<title>Top Banana!</title>`,
		playHref(bananas),
		`Bananas of the World`,
		`5 plays`,
		playHref(capitals),
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

	db := dbtest.Open(t)
	stores := store.New(db, slog.New(slog.DiscardHandler))

	// One quiz with exactly one finished play, by one claimed player who
	// has finished exactly one game: drives "1 play" and "1 quiz".
	solo := createQuiz(t, stores.Quizzes, "Solo", "solo", "", oneQuestion())
	carol := claimedPlayer(t, stores.Players, "carol")
	finishGameFor(t, stores.Games, carol.ID, solo)

	body := serve(t, db)

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

func TestHandle_RendersNewestTab(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	stores := store.New(db, slog.New(slog.DiscardHandler))

	// fresh has 3 questions ("3 questions" plural pill); lone has 1
	// ("1 question" singular). Newest orders by created_at DESC, id DESC,
	// so the later-inserted lone renders before fresh; both must appear.
	fresh := createQuiz(t, stores.Quizzes, "Fresh Quiz", "fresh-quiz", "Just made.", []*quiz.Question{
		{Text: "F1", Position: 1, Options: []*quiz.Option{{Text: "a", Correct: true}}},
		{Text: "F2", Position: 2, Options: []*quiz.Option{{Text: "b", Correct: true}}},
		{Text: "F3", Position: 3, Options: []*quiz.Option{{Text: "c", Correct: true}}},
	})
	lone := createQuiz(t, stores.Quizzes, "Lone Question", "lone-question", "", oneQuestion())

	body := serve(t, db)

	for _, want := range []string{
		// Tab control: a tablist with a Newest tab the Alpine toggle drives.
		`role="tablist"`,
		`role="tab"`,
		`Newest`,
		// Newest card markup: play link, question-count pill (plural + singular).
		playHref(fresh),
		`Fresh Quiz`,
		`3 questions`,
		playHref(lone),
		`1 question`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, "1 questions") {
		t.Error("body contained plural question form alongside count of 1")
	}
}

func TestHandle_NewestEmptyState(t *testing.T) {
	t.Parallel()

	body := serve(t, dbtest.Open(t))

	if got, want := body, "No quizzes yet."; !strings.Contains(got, want) {
		t.Errorf("body missing newest empty-state %q", want)
	}
}

func TestHandle_EmptyState(t *testing.T) {
	t.Parallel()

	body := serve(t, dbtest.Open(t))

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

// TestHandle_StoreErrorsDegradeToEmptyState forces every home query to
// error by closing the DB before serving, then asserts the page still
// renders 200 with the empty state and a reachable admin link. Closing
// the real connection (no test double) exercises the handler's
// error-degradation branch against real store errors.
func TestHandle_StoreErrorsDegradeToEmptyState(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	stores := store.New(db, slog.New(slog.DiscardHandler))
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close err = %v, want nil", err)
	}

	handler := Handle(slog.New(slog.DiscardHandler), stores.Home, nil, nil)
	rec := httptest.NewRecorder()
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

	db := dbtest.Open(t)
	stores := store.New(db, slog.New(slog.DiscardHandler))

	// Seed more than the page-level cap of 6 quizzes to confirm the
	// handler slices instead of dumping the full list. Each quiz has one
	// finished play. Popular ranks by play_count then updated_at DESC, and
	// newest ranks by created_at DESC, so backdating both timestamps in
	// lockstep keeps the two tabs in agreement: TA newest in both, TG
	// oldest in both and thus past the cap on both. Without aligning
	// created_at too, TG would still surface in the newest tab and defeat
	// the absence assertion. Distinct players satisfy the
	// one-attempt-per-(player, quiz) rule.
	const seeded = 7
	titles := []string{"TA", "TB", "TC", "TD", "TE", "TF", "TG"}
	for i := range seeded {
		qz := createQuiz(t, stores.Quizzes, titles[i],
			fmt.Sprintf("trunc-%d", i), "", oneQuestion())
		player := claimedPlayer(t, stores.Players, fmt.Sprintf("trunc-player-%d", i))
		finishGameFor(t, stores.Games, player.ID, qz)
		if _, err := db.ExecContext(
			t.Context(),
			"UPDATE quizzes SET created_at = datetime('now', ?), updated_at = datetime('now', ?) WHERE id = ?",
			fmt.Sprintf("-%d minutes", i),
			fmt.Sprintf("-%d minutes", i),
			qz.ID,
		); err != nil {
			t.Fatalf("backdate timestamps err = %v, want nil", err)
		}
	}

	body := serve(t, db)

	// The 7th entry (oldest in both orderings) must be sliced off both tabs.
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
