package store_test

import (
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/quiz"
	. "github.com/starquake/topbanana/internal/store"
)

// finishGame seeds one game_question row per quiz question for the given
// (game, quiz) pair so the home queries treat the game as finished. The
// home queries' finisher condition is COUNT(game_questions) >=
// COUNT(questions); issuing all questions is enough - actual answers
// are not required.
func finishGame(t *testing.T, gs *GameStore, g *game.Game, q *quiz.Quiz) {
	t.Helper()
	now := time.Now()
	for _, qs := range q.Questions {
		gq := &game.Question{
			GameID:     g.ID,
			QuestionID: qs.ID,
			StartedAt:  now,
			ExpiredAt:  now.Add(10 * time.Second),
		}
		if err := gs.CreateQuestion(t.Context(), gq); err != nil {
			t.Fatalf("finishGame CreateQuestion err = %v, want nil", err)
		}
	}
}

// homeSeed bundles the artefacts a home-store test needs after seeding.
// Returned by [seedHomeDB] so callers do not exceed the revive
// function-result-limit cap of 3 return values.
type homeSeed struct {
	DB    *sql.DB
	Quiz1 *quiz.Quiz
	Quiz2 *quiz.Quiz
	Alice *auth.Player
	Bob   *auth.Player
}

// seedHomeDB seeds two quizzes, two claimed players, and the games that
// drive the home page ranking. The one-attempt-per-(player, quiz) rule
// (#273) means each player plays a quiz at most once, so distinct play
// counts come from distinct players:
//   - quiz1 has 3 finished games (alice + bob + ghost) - popular #1
//   - quiz2 has 1 finished game (alice) - popular #2
//   - alice finishes 2 games (quiz1 + quiz2) - active #1
//   - bob finishes 1 game (quiz1) - active #2
//   - an anonymous (unclaimed) player finishes 1 game and must NOT
//     appear in the active list
//   - an in-progress game (no game_questions) for an unrelated player
//     must NOT bump the play count
func seedHomeDB(t *testing.T) homeSeed {
	t.Helper()
	db := dbtest.Open(t)
	logger := slog.Default()
	quizzes := NewQuizStore(db, logger)
	games := NewGameStore(db, logger)
	players := NewPlayerStore(db, logger)

	quiz1 := &quiz.Quiz{
		Title: "Q1", Slug: "q1", Description: "Q1 desc",
		CreatedByPlayerID: seededAdminID,
		Questions: []*quiz.Question{
			{Text: "Q1-Q1", Position: 1, Options: []*quiz.Option{{Text: "a"}, {Text: "b", Correct: true}}},
			{Text: "Q1-Q2", Position: 2, Options: []*quiz.Option{{Text: "c"}, {Text: "d", Correct: true}}},
		},
	}
	if err := quizzes.CreateQuiz(t.Context(), quiz1); err != nil {
		t.Fatalf("CreateQuiz q1 err = %v, want nil", err)
	}
	quiz2 := &quiz.Quiz{
		Title: "Q2", Slug: "q2", Description: "Q2 desc",
		CreatedByPlayerID: seededAdminID,
		Questions: []*quiz.Question{
			{Text: "Q2-Q1", Position: 1, Options: []*quiz.Option{{Text: "e", Correct: true}}},
		},
	}
	if err := quizzes.CreateQuiz(t.Context(), quiz2); err != nil {
		t.Fatalf("CreateQuiz q2 err = %v, want nil", err)
	}

	alice, err := players.CreatePlayer(t.Context(), "alice", "alice"+"@example.test", "hash", auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer alice err = %v, want nil", err)
	}
	bob, err := players.CreatePlayer(t.Context(), "bob", "bob"+"@example.test", "hash", auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer bob err = %v, want nil", err)
	}
	// Anonymous player - must not surface in the active list because the
	// query filters on displayName_claimed = 1.
	ghost, err := players.CreateAnonymousPlayer(t.Context(), "ghost-petname")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer ghost err = %v, want nil", err)
	}

	// alice: quiz1 + quiz2 = 2 finished total
	finishGameFor(t, games, alice.ID, quiz1, quiz1.ID)
	finishGameFor(t, games, alice.ID, quiz2, quiz2.ID)
	// bob: quiz1 = 1 finished total
	finishGameFor(t, games, bob.ID, quiz1, quiz1.ID)
	// ghost: quiz1 = 1 finished, bumps quiz1 play count to 3 but must
	// NOT show up in the active list (unclaimed displayName).
	finishGameFor(t, games, ghost.ID, quiz1, quiz1.ID)

	// In-progress game on quiz1 by a fresh anonymous bystander: created,
	// participant added, but no game_questions issued. The home queries
	// should not count it as a play. The bystander has to be a fresh
	// player because alice + bob + ghost all already have a participant
	// row on quiz1 (the UNIQUE INDEX from the #273 migration disallows
	// duplicates).
	bystander, err := players.CreateAnonymousPlayer(t.Context(), "bystander-inflight")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer bystander err = %v, want nil", err)
	}
	g := &game.Game{QuizID: quiz1.ID}
	if err := games.CreateGame(t.Context(), g); err != nil {
		t.Fatalf("CreateGame in-progress err = %v, want nil", err)
	}
	if err := games.CreateParticipant(t.Context(), &game.Participant{
		GameID: g.ID, PlayerID: bystander.ID, QuizID: quiz1.ID,
	}); err != nil {
		t.Fatalf("CreateParticipant in-progress err = %v, want nil", err)
	}

	return homeSeed{DB: db, Quiz1: quiz1, Quiz2: quiz2, Alice: alice, Bob: bob}
}

// finishGameFor creates a finished game for the (player, quiz) pair:
// game + participant + one game_question per quiz question. The
// explicit quizID argument is denormalised onto game_participants per
// the #273 migration so the UNIQUE INDEX on (player_id, quiz_id) can
// fire if a test accidentally calls this twice for the same pair -
// the failure now surfaces as ErrGameAlreadyExists.
func finishGameFor(t *testing.T, games *GameStore, playerID int64, q *quiz.Quiz, quizID int64) {
	t.Helper()
	g := &game.Game{QuizID: q.ID}
	if err := games.CreateGame(t.Context(), g); err != nil {
		t.Fatalf("CreateGame err = %v, want nil", err)
	}
	if err := games.CreateParticipant(t.Context(), &game.Participant{
		GameID: g.ID, PlayerID: playerID, QuizID: quizID,
	}); err != nil {
		t.Fatalf("CreateParticipant err = %v, want nil", err)
	}
	finishGame(t, games, g, q)
}

func TestHomeStore_ListPopularQuizzes(t *testing.T) {
	t.Parallel()

	seed := seedHomeDB(t)
	hs := NewHomeStore(seed.DB, slog.Default())

	rows, err := hs.ListPopularQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListPopularQuizzes err = %v, want nil", err)
	}
	if got, want := len(rows), 2; got != want {
		t.Fatalf("len(rows) = %d, want %d (rows=%+v)", got, want, rows)
	}

	// quiz1 has 3 finished plays (alice, bob, ghost); quiz2 has 1
	// (alice). The in-progress game by the bystander on quiz1 must
	// not bump the count.
	if got, want := rows[0].ID, seed.Quiz1.ID; got != want {
		t.Errorf("rows[0].ID = %d, want %d (quiz1 should rank first)", got, want)
	}
	if got, want := rows[0].PlayCount, 3; got != want {
		t.Errorf("rows[0].PlayCount = %d, want %d", got, want)
	}
	if got, want := rows[1].ID, seed.Quiz2.ID; got != want {
		t.Errorf("rows[1].ID = %d, want %d", got, want)
	}
	if got, want := rows[1].PlayCount, 1; got != want {
		t.Errorf("rows[1].PlayCount = %d, want %d", got, want)
	}
}

// TestHomeStore_ListNewestQuizzes pins the "Newest" tab data source:
// most-recently-created public quizzes first, with private/unlisted and
// zero-question quizzes excluded so only playable public quizzes surface.
func TestHomeStore_ListNewestQuizzes(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	logger := slog.Default()
	quizzes := NewQuizStore(db, logger)
	hs := NewHomeStore(db, logger)

	withQuestion := func() []*quiz.Question {
		return []*quiz.Question{
			{Text: "Q", Position: 1, Options: []*quiz.Option{{Text: "a", Correct: true}}},
		}
	}

	// Two public quizzes created in a known order. older is inserted
	// first, newer second; the query must return newer before older.
	older := &quiz.Quiz{
		Title: "Older Public", Slug: "older-public", Description: "first",
		CreatedByPlayerID: seededAdminID, Visibility: quiz.VisibilityPublic,
		Questions: withQuestion(),
	}
	if err := quizzes.CreateQuiz(t.Context(), older); err != nil {
		t.Fatalf("CreateQuiz older err = %v, want nil", err)
	}
	newer := &quiz.Quiz{
		Title: "Newer Public", Slug: "newer-public", Description: "second",
		CreatedByPlayerID: seededAdminID, Visibility: quiz.VisibilityPublic,
		Questions: withQuestion(),
	}
	if err := quizzes.CreateQuiz(t.Context(), newer); err != nil {
		t.Fatalf("CreateQuiz newer err = %v, want nil", err)
	}

	// A private quiz with questions: must be excluded by the visibility
	// gate even though it is the most recently created.
	private := &quiz.Quiz{
		Title: "Private", Slug: "private", Description: "hidden",
		CreatedByPlayerID: seededAdminID, Visibility: quiz.VisibilityPrivate,
		Questions: withQuestion(),
	}
	if err := quizzes.CreateQuiz(t.Context(), private); err != nil {
		t.Fatalf("CreateQuiz private err = %v, want nil", err)
	}

	// A public quiz with zero questions: cannot be played, must be
	// excluded by the EXISTS gate.
	empty := &quiz.Quiz{
		Title: "Empty Public", Slug: "empty-public", Description: "no questions",
		CreatedByPlayerID: seededAdminID, Visibility: quiz.VisibilityPublic,
	}
	if err := quizzes.CreateQuiz(t.Context(), empty); err != nil {
		t.Fatalf("CreateQuiz empty err = %v, want nil", err)
	}

	// CreateQuiz stamps created_at via the DB default, so all four rows
	// can share the same second. Backdate older one day so the
	// created_at DESC ordering is deterministic rather than relying on
	// the id-DESC tiebreaker alone.
	if _, err := db.ExecContext(
		t.Context(),
		"UPDATE quizzes SET created_at = datetime('now', '-1 day') WHERE id = ?",
		older.ID,
	); err != nil {
		t.Fatalf("backdate older err = %v, want nil", err)
	}

	rows, err := hs.ListNewestQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListNewestQuizzes err = %v, want nil", err)
	}
	if got, want := len(rows), 2; got != want {
		t.Fatalf("len(rows) = %d, want %d (rows=%+v)", got, want, rows)
	}
	if got, want := rows[0].ID, newer.ID; got != want {
		t.Errorf("rows[0].ID = %d, want %d (newer should rank first)", got, want)
	}
	if got, want := rows[1].ID, older.ID; got != want {
		t.Errorf("rows[1].ID = %d, want %d (older should rank second)", got, want)
	}
	if got, want := rows[0].QuestionCount, 1; got != want {
		t.Errorf("rows[0].QuestionCount = %d, want %d", got, want)
	}
	for _, r := range rows {
		if r.ID == private.ID {
			t.Errorf("private quiz surfaced in newest list: %+v", r)
		}
		if r.ID == empty.ID {
			t.Errorf("empty public quiz surfaced in newest list: %+v", r)
		}
	}
}

func TestHomeStore_ListMostActivePlayers(t *testing.T) {
	t.Parallel()

	seed := seedHomeDB(t)
	hs := NewHomeStore(seed.DB, slog.Default())

	rows, err := hs.ListMostActivePlayers(t.Context())
	if err != nil {
		t.Fatalf("ListMostActivePlayers err = %v, want nil", err)
	}
	// Anonymous "ghost-petname" finished a game but must be filtered out
	// because displayName_claimed = 0; only alice and bob remain.
	if got, want := len(rows), 2; got != want {
		t.Fatalf("len(rows) = %d, want %d (rows=%+v)", got, want, rows)
	}

	if got, want := rows[0].ID, seed.Alice.ID; got != want {
		t.Errorf("rows[0].ID = %d, want %d (alice should rank first)", got, want)
	}
	if got, want := rows[0].FinishedCount, 2; got != want {
		t.Errorf("rows[0].FinishedCount = %d, want %d", got, want)
	}
	if got, want := rows[1].ID, seed.Bob.ID; got != want {
		t.Errorf("rows[1].ID = %d, want %d", got, want)
	}
	if got, want := rows[1].FinishedCount, 1; got != want {
		t.Errorf("rows[1].FinishedCount = %d, want %d", got, want)
	}

	// Defensive: the anonymous player's auto-petname displayName must not
	// appear in any returned row.
	for _, r := range rows {
		if r.DisplayName == "ghost-petname" {
			t.Errorf("anonymous player surfaced in active list: %+v", r)
		}
	}
}

// TestHomeStore_ListMostActivePlayers_AppliesThirtyDayWindow pins
// #355: a player who finished a game more than 30 days ago must NOT
// surface in the active list. Without the window the home page's
// two leaderboards disagree on what "recently active" means.
func TestHomeStore_ListMostActivePlayers_AppliesThirtyDayWindow(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	logger := slog.Default()
	quizzes := NewQuizStore(db, logger)
	games := NewGameStore(db, logger)
	players := NewPlayerStore(db, logger)
	hs := NewHomeStore(db, logger)

	q := &quiz.Quiz{
		Title: "T", Slug: "t", Description: "",
		CreatedByPlayerID: seededAdminID,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "a", Correct: true}}},
		},
	}
	if err := quizzes.CreateQuiz(t.Context(), q); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	stale, err := players.CreatePlayer(
		t.Context(),
		"stale-winner",
		"stale-winner"+"@example.test",
		"hash",
		auth.RolePlayer,
	)
	if err != nil {
		t.Fatalf("CreatePlayer stale err = %v, want nil", err)
	}
	recent, err := players.CreatePlayer(
		t.Context(),
		"recent-winner",
		"recent-winner"+"@example.test",
		"hash",
		auth.RolePlayer,
	)
	if err != nil {
		t.Fatalf("CreatePlayer recent err = %v, want nil", err)
	}

	finishGameFor(t, games, stale.ID, q, q.ID)
	finishGameFor(t, games, recent.ID, q, q.ID)

	// Backdate the stale player's game 60 days. The query filters on
	// games.created_at, so this should drop the stale player from the
	// active list while leaving the recent player.
	_, err = db.ExecContext(
		t.Context(),
		"UPDATE games SET created_at = datetime('now', '-60 days') WHERE id IN (SELECT game_id FROM game_participants WHERE player_id = ?)",
		stale.ID,
	)
	if err != nil {
		t.Fatalf("backdate stale game err = %v, want nil", err)
	}

	rows, err := hs.ListMostActivePlayers(t.Context())
	if err != nil {
		t.Fatalf("ListMostActivePlayers err = %v, want nil", err)
	}
	if got, want := len(rows), 1; got != want {
		t.Fatalf("len(rows) = %d, want %d (stale player must be filtered out)", got, want)
	}
	if got, want := rows[0].ID, recent.ID; got != want {
		t.Errorf("rows[0].ID = %d, want %d (recent player should be the only row)", got, want)
	}
}

// TestHomeStore_ExcludesEmptyQuizFromRankings pins the #275 fix: a
// quiz with zero questions used to satisfy the finisher predicate
// (0 >= 0) and surface on the popular list. The EXISTS gate added to
// both home queries now requires the quiz to have at least one
// question, matching game.Game.IsCompleted's `len(Quiz.Questions) > 0`
// rule.
func TestHomeStore_ExcludesEmptyQuizFromRankings(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	logger := slog.Default()
	quizzes := NewQuizStore(db, logger)
	games := NewGameStore(db, logger)
	players := NewPlayerStore(db, logger)
	hs := NewHomeStore(db, logger)

	// Author a quiz with no questions and seed a game + participant on
	// it. Before the fix the home queries would happily count this as a
	// finished play. quiz.Quiz doesn't currently require questions to
	// validate, so the admin can produce one of these any time they
	// start authoring and never get back to it.
	emptyQuiz := &quiz.Quiz{
		Title:             "Empty",
		Slug:              "empty",
		Description:       "no questions",
		CreatedByPlayerID: seededAdminID,
	}
	if err := quizzes.CreateQuiz(t.Context(), emptyQuiz); err != nil {
		t.Fatalf("CreateQuiz empty err = %v, want nil", err)
	}
	player, err := players.CreatePlayer(t.Context(), "lonely", "lonely"+"@example.test", "hash", auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	g := &game.Game{QuizID: emptyQuiz.ID}
	if err = games.CreateGame(t.Context(), g); err != nil {
		t.Fatalf("CreateGame err = %v, want nil", err)
	}
	if err = games.CreateParticipant(t.Context(), &game.Participant{
		GameID: g.ID, PlayerID: player.ID, QuizID: emptyQuiz.ID,
	}); err != nil {
		t.Fatalf("CreateParticipant err = %v, want nil", err)
	}

	popular, err := hs.ListPopularQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListPopularQuizzes err = %v, want nil", err)
	}
	if got, want := len(popular), 0; got != want {
		t.Errorf("ListPopularQuizzes returned %d rows, want %d (empty quiz must not surface)", got, want)
	}

	active, err := hs.ListMostActivePlayers(t.Context())
	if err != nil {
		t.Fatalf("ListMostActivePlayers err = %v, want nil", err)
	}
	if got, want := len(active), 0; got != want {
		t.Errorf("ListMostActivePlayers returned %d rows, want %d (empty-quiz play must not bump activity)", got, want)
	}
}

func TestHomeStore_EmptyDB(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	hs := NewHomeStore(db, slog.Default())

	quizzes, err := hs.ListPopularQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListPopularQuizzes err = %v, want nil", err)
	}
	if got, want := len(quizzes), 0; got != want {
		t.Errorf("len(quizzes) = %d, want %d", got, want)
	}

	players, err := hs.ListMostActivePlayers(t.Context())
	if err != nil {
		t.Fatalf("ListMostActivePlayers err = %v, want nil", err)
	}
	if got, want := len(players), 0; got != want {
		t.Errorf("len(players) = %d, want %d", got, want)
	}
}
