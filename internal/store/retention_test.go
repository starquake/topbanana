package store_test

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/dbtest"
	. "github.com/starquake/topbanana/internal/store"
)

// retentionSeed holds the IDs the retention sweep test seeds so the
// post-sweep assertions can name each row precisely.
type retentionSeed struct {
	anonFinishedID int64
	anonCruftID    int64
	anonCruft2ID   int64
	anonRecentID   int64
	signedInID     int64

	anonFinishedGameID     string
	anonFinishedUnfGameID  string
	anonCruftGameID        string
	anonCruft2GameID       string
	anonRecentGameID       string
	abandonedGameID        string
	abandoned2GameID       string
	recentUnfinishedGameID string
	signedInFinishedGameID string
}

// Seed timestamps as SQLite datetime expressions so the rows carry the same
// CURRENT_TIMESTAMP text encoding production rows are minted with, exercising
// the real datetime('now','-N days') comparison in the sweep queries. The
// relative offsets keep classification deterministic against wall-clock now:
// 100d is past both windows (90d/30d), 40d is past only the abandoned-game
// 30d window, and 1h is inside both.
const (
	seedOld    = "datetime('now', '-100 days')"
	seedMidOld = "datetime('now', '-40 days')"
	seedRecent = "datetime('now', '-1 hour')"
)

// TestRetentionSweep exercises both retention sweeps against a real migrated
// SQLite database (no HTTP server): stale anonymous players with no finished
// game (and their game data) are removed, but an old anonymous player holding
// a finished game is kept so its leaderboard score survives (#626); abandoned
// never-finished games are pruned regardless of player (#627); and recent
// players, finished games, and signed-in players all survive. The sweeps take
// the retention window in days; the cutoff is computed in SQL against real
// wall-clock now.
func TestRetentionSweep(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)

	seed := seedRetention(ctx, t, db)

	retention := NewRetentionStore(db, slog.Default())

	if err := retention.SweepStaleAnonymousPlayers(ctx, AnonymousRetentionDays); err != nil {
		t.Fatalf("SweepStaleAnonymousPlayers err = %v, want nil", err)
	}
	if err := retention.SweepAbandonedGames(ctx, AbandonedGameDays); err != nil {
		t.Fatalf("SweepAbandonedGames err = %v, want nil", err)
	}

	assertRetention(ctx, t, db, seed)
}

// TestSweepAbandonedGames_DeleteError pins the deleteGamesByIDs error
// branch: an abandoned game is seeded with a game_answer, then a
// BEFORE DELETE trigger on game_answers aborts the first dependent
// delete so the sweep surfaces the wrapped failure instead of swallowing
// it.
func TestSweepAbandonedGames_DeleteError(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := dbtest.Open(t)

	ownerID := insertSignedInPlayer(ctx, t, db, "abandoned-owner", seedRecent)
	q := seedQuiz(ctx, t, db, "abandoned-err", seedMidOld, ownerID)
	gameID := insertGame(ctx, t, db, "g-abandoned-err", q.quizID, seedMidOld)
	insertParticipant(ctx, t, db, gameID, ownerID, q.quizID, seedMidOld)
	gq := insertGameQuestion(ctx, t, db, gameID, q.q1, seedMidOld)
	insertGameAnswer(ctx, t, db, gameID, ownerID, gq, q.opt1, seedMidOld)

	if _, err := db.ExecContext(ctx, `
		CREATE TRIGGER game_answers_no_delete
		BEFORE DELETE ON game_answers
		BEGIN
			SELECT RAISE(ABORT, 'no deletes');
		END;
	`); err != nil {
		t.Fatalf("failed to create trigger: %v", err)
	}

	retention := NewRetentionStore(db, slog.Default())
	err := retention.SweepAbandonedGames(ctx, AbandonedGameDays)
	if err == nil {
		t.Fatal("got nil, want error")
	}
	if got, want := err.Error(), "failed to delete game answers"; !strings.Contains(got, want) {
		t.Errorf("err.Error() = %q, should contain %q", got, want)
	}
	if got, want := err.Error(), "failed to sweep abandoned games"; !strings.Contains(got, want) {
		t.Errorf("err.Error() = %q, should contain %q", got, want)
	}
}

// seededQuiz bundles the quiz, its round, and its two questions (each with
// one option) so a game can be seeded as finished (both questions issued) or
// unfinished (one issued). Each scenario gets its own quiz so the UNIQUE
// (player_id, quiz_id) index on game_participants does not reject a player
// who appears in two seeded games.
type seededQuiz struct {
	quizID  int64
	roundID int64
	q1, q2  int64
	opt1    int64
}

// at is a SQLite datetime expression (e.g. seedOld) substituted into the
// created_at columns rather than bound as a parameter, so the rows store the
// CURRENT_TIMESTAMP text encoding the sweep queries compare against. ownerID
// owns the quiz (quizzes.created_by_player_id is NOT NULL).
func seedQuiz(ctx context.Context, t *testing.T, db *sql.DB, slug, at string, ownerID int64) seededQuiz {
	t.Helper()

	quizID := insertQuiz(ctx, t, db, slug, at, ownerID)
	roundID := insertRound(ctx, t, db, quizID, at)
	q1 := insertQuestion(ctx, t, db, quizID, roundID, 0)
	q2 := insertQuestion(ctx, t, db, quizID, roundID, 1)
	opt1 := insertOption(ctx, t, db, q1)
	insertOption(ctx, t, db, q2)

	return seededQuiz{quizID: quizID, roundID: roundID, q1: q1, q2: q2, opt1: opt1}
}

// seedRetention inserts the players, quizzes, and games the sweep test needs,
// stamping created_at via SQLite datetime expressions so the cutoff
// comparison is exercised against production-shaped timestamps. Two stale
// anonymous players and two abandoned games are seeded so the sweep is shown
// to handle more than a single stale row per category.
func seedRetention(ctx context.Context, t *testing.T, db *sql.DB) retentionSeed {
	t.Helper()

	// A signed-in player owns every seeded quiz (quizzes.created_by_player_id
	// is NOT NULL). It is not anonymous, so the sweep never touches it.
	ownerID := insertSignedInPlayer(ctx, t, db, "quiz-owner", seedRecent)

	var s retentionSeed
	s.anonFinishedID = insertAnonPlayer(ctx, t, db, "anon-finished", seedOld)
	s.anonCruftID = insertAnonPlayer(ctx, t, db, "anon-cruft", seedOld)
	s.anonCruft2ID = insertAnonPlayer(ctx, t, db, "anon-cruft-2", seedOld)
	s.anonRecentID = insertAnonPlayer(ctx, t, db, "anon-recent", seedRecent)
	s.signedInID = insertSignedInPlayer(ctx, t, db, "signed-in", seedOld)

	// Anonymous old player holding a FINISHED game (both questions issued,
	// with an answer and a seen-round row). The player and the finished game
	// are kept regardless of age so the leaderboard score survives; its
	// separate old unfinished game is still pruned by the abandoned-game sweep.
	qf := seedQuiz(ctx, t, db, "anon-fin", seedOld, ownerID)
	s.anonFinishedGameID = insertGame(ctx, t, db, "g-anon-fin", qf.quizID, seedOld)
	insertParticipant(ctx, t, db, s.anonFinishedGameID, s.anonFinishedID, qf.quizID, seedOld)
	gqA := insertGameQuestion(ctx, t, db, s.anonFinishedGameID, qf.q1, seedOld)
	insertGameQuestion(ctx, t, db, s.anonFinishedGameID, qf.q2, seedOld)
	insertGameAnswer(ctx, t, db, s.anonFinishedGameID, s.anonFinishedID, gqA, qf.opt1, seedOld)
	insertSeenRound(ctx, t, db, s.anonFinishedGameID, qf.roundID, "intro", seedOld)

	qfu := seedQuiz(ctx, t, db, "anon-fin-unf", seedOld, ownerID)
	s.anonFinishedUnfGameID = insertGame(ctx, t, db, "g-anon-fin-unf", qfu.quizID, seedOld)
	insertParticipant(ctx, t, db, s.anonFinishedUnfGameID, s.anonFinishedID, qfu.quizID, seedOld)
	insertGameQuestion(ctx, t, db, s.anonFinishedUnfGameID, qfu.q1, seedOld)

	// Two anonymous old players with NO finished game (only an old unfinished
	// one each). Pure casual-visitor cruft: the players and their games are
	// swept by #626. Two of them prove the sweep handles multiple stale rows.
	qc := seedQuiz(ctx, t, db, "anon-cruft", seedOld, ownerID)
	s.anonCruftGameID = insertGame(ctx, t, db, "g-anon-cruft", qc.quizID, seedOld)
	insertParticipant(ctx, t, db, s.anonCruftGameID, s.anonCruftID, qc.quizID, seedOld)
	insertGameQuestion(ctx, t, db, s.anonCruftGameID, qc.q1, seedOld)

	qc2 := seedQuiz(ctx, t, db, "anon-cruft-2", seedOld, ownerID)
	s.anonCruft2GameID = insertGame(ctx, t, db, "g-anon-cruft-2", qc2.quizID, seedOld)
	insertParticipant(ctx, t, db, s.anonCruft2GameID, s.anonCruft2ID, qc2.quizID, seedOld)
	insertGameQuestion(ctx, t, db, s.anonCruft2GameID, qc2.q1, seedOld)

	// Anonymous recent player: an unfinished game. Survives - minted inside
	// the 90-day window, and its game is younger than 30 days.
	qr := seedQuiz(ctx, t, db, "anon-rec", seedRecent, ownerID)
	s.anonRecentGameID = insertGame(ctx, t, db, "g-anon-rec", qr.quizID, seedRecent)
	insertParticipant(ctx, t, db, s.anonRecentGameID, s.anonRecentID, qr.quizID, seedRecent)
	insertGameQuestion(ctx, t, db, s.anonRecentGameID, qr.q1, seedRecent)

	// Signed-in player: two abandoned (unfinished, >30d) games that the
	// abandoned-game sweep should prune even though the player stays. Two of
	// them prove the sweep handles multiple stale games.
	qa := seedQuiz(ctx, t, db, "abandoned", seedMidOld, ownerID)
	s.abandonedGameID = insertGame(ctx, t, db, "g-abandoned", qa.quizID, seedMidOld)
	insertParticipant(ctx, t, db, s.abandonedGameID, s.signedInID, qa.quizID, seedMidOld)
	insertGameQuestion(ctx, t, db, s.abandonedGameID, qa.q1, seedMidOld)

	qa2 := seedQuiz(ctx, t, db, "abandoned-2", seedMidOld, ownerID)
	s.abandoned2GameID = insertGame(ctx, t, db, "g-abandoned-2", qa2.quizID, seedMidOld)
	insertParticipant(ctx, t, db, s.abandoned2GameID, s.signedInID, qa2.quizID, seedMidOld)
	insertGameQuestion(ctx, t, db, s.abandoned2GameID, qa2.q1, seedMidOld)

	// Signed-in player: a recent unfinished game. Survives - younger than 30d.
	qru := seedQuiz(ctx, t, db, "recent-unf", seedRecent, ownerID)
	s.recentUnfinishedGameID = insertGame(ctx, t, db, "g-recent-unf", qru.quizID, seedRecent)
	insertParticipant(ctx, t, db, s.recentUnfinishedGameID, s.signedInID, qru.quizID, seedRecent)
	insertGameQuestion(ctx, t, db, s.recentUnfinishedGameID, qru.q1, seedRecent)

	// Signed-in player: an old but FINISHED game. Survives - "abandoned"
	// is unfinished-only, so age alone does not prune a completed game.
	qsf := seedQuiz(ctx, t, db, "signed-fin", seedOld, ownerID)
	s.signedInFinishedGameID = insertGame(ctx, t, db, "g-signed-fin", qsf.quizID, seedOld)
	insertParticipant(ctx, t, db, s.signedInFinishedGameID, s.signedInID, qsf.quizID, seedOld)
	insertGameQuestion(ctx, t, db, s.signedInFinishedGameID, qsf.q1, seedOld)
	insertGameQuestion(ctx, t, db, s.signedInFinishedGameID, qsf.q2, seedOld)

	return s
}

// assertRetention checks the post-sweep state: swept rows are gone, surviving
// rows remain, and the orphaned dependent rows (answers, questions, seen
// rounds, participants) of swept games are removed too.
func assertRetention(ctx context.Context, t *testing.T, db *sql.DB, s retentionSeed) {
	t.Helper()

	// Players: both cruft anonymous players (no finished game) are gone; the
	// anon holding a finished game, the recent anon, and the signed-in player
	// survive.
	assertPlayerAbsent(ctx, t, db, s.anonCruftID)
	assertPlayerAbsent(ctx, t, db, s.anonCruft2ID)
	assertPlayerPresent(ctx, t, db, s.anonFinishedID)
	assertPlayerPresent(ctx, t, db, s.anonRecentID)
	assertPlayerPresent(ctx, t, db, s.signedInID)

	// Games swept: both cruft anons' games (#626), and the abandoned/unfinished
	// games older than 30 days (#627) - including the preserved anon's separate
	// unfinished game.
	assertGameAbsent(ctx, t, db, s.anonCruftGameID)
	assertGameAbsent(ctx, t, db, s.anonCruft2GameID)
	assertGameAbsent(ctx, t, db, s.anonFinishedUnfGameID)
	assertGameAbsent(ctx, t, db, s.abandonedGameID)
	assertGameAbsent(ctx, t, db, s.abandoned2GameID)

	// Surviving games: the anon's finished game keeps its leaderboard score.
	assertGamePresent(ctx, t, db, s.anonFinishedGameID)
	assertGamePresent(ctx, t, db, s.anonRecentGameID)
	assertGamePresent(ctx, t, db, s.recentUnfinishedGameID)
	assertGamePresent(ctx, t, db, s.signedInFinishedGameID)

	// No dependent rows of the swept games linger.
	assertNoOrphans(ctx, t, db, s.anonCruftGameID)
	assertNoOrphans(ctx, t, db, s.anonCruft2GameID)
	assertNoOrphans(ctx, t, db, s.anonFinishedUnfGameID)
	assertNoOrphans(ctx, t, db, s.abandonedGameID)
	assertNoOrphans(ctx, t, db, s.abandoned2GameID)
}

// The at argument is a trusted SQLite datetime expression (a test constant),
// inlined into the statement so created_at evaluates server-side to a
// CURRENT_TIMESTAMP-shaped string rather than a value bound in the driver's
// time.Time.String() text encoding.
func insertQuiz(ctx context.Context, t *testing.T, db *sql.DB, slug, at string, ownerID int64) int64 {
	t.Helper()
	res, err := db.ExecContext(ctx,
		`INSERT INTO quizzes (title, slug, created_at, updated_at, created_by_player_id)
		 VALUES ('Retention Quiz', ?, `+at+`, `+at+`, ?)`,
		slug, ownerID,
	)
	if err != nil {
		t.Fatalf("insert quiz err = %v, want nil", err)
	}

	return lastID(t, res)
}

func insertRound(ctx context.Context, t *testing.T, db *sql.DB, quizID int64, at string) int64 {
	t.Helper()
	res, err := db.ExecContext(ctx,
		`INSERT INTO rounds (quiz_id, position, title, created_at, updated_at)
		 VALUES (?, 0, 'Round 1', `+at+`, `+at+`)`,
		quizID,
	)
	if err != nil {
		t.Fatalf("insert round err = %v, want nil", err)
	}

	return lastID(t, res)
}

func insertQuestion(ctx context.Context, t *testing.T, db *sql.DB, quizID, roundID int64, pos int) int64 {
	t.Helper()
	res, err := db.ExecContext(ctx,
		`INSERT INTO questions (quiz_id, round_id, text, position) VALUES (?, ?, 'Q', ?)`,
		quizID, roundID, pos,
	)
	if err != nil {
		t.Fatalf("insert question err = %v, want nil", err)
	}

	return lastID(t, res)
}

func insertOption(ctx context.Context, t *testing.T, db *sql.DB, questionID int64) int64 {
	t.Helper()
	res, err := db.ExecContext(ctx,
		`INSERT INTO options (question_id, text, is_correct) VALUES (?, 'opt', 1)`,
		questionID,
	)
	if err != nil {
		t.Fatalf("insert option err = %v, want nil", err)
	}

	return lastID(t, res)
}

func insertAnonPlayer(ctx context.Context, t *testing.T, db *sql.DB, name, at string) int64 {
	t.Helper()
	res, err := db.ExecContext(ctx,
		`INSERT INTO players (display_name, role, display_name_claimed, created_at)
		 VALUES (?, 'player', 0, `+at+`)`,
		name,
	)
	if err != nil {
		t.Fatalf("insert anon player err = %v, want nil", err)
	}

	return lastID(t, res)
}

func insertSignedInPlayer(ctx context.Context, t *testing.T, db *sql.DB, name, at string) int64 {
	t.Helper()
	res, err := db.ExecContext(ctx,
		`INSERT INTO players (display_name, email, password_hash, role, display_name_claimed, created_at)
		 VALUES (?, ?, 'hash', 'player', 1, `+at+`)`,
		name, name+"@example.com",
	)
	if err != nil {
		t.Fatalf("insert signed-in player err = %v, want nil", err)
	}

	return lastID(t, res)
}

func insertGame(ctx context.Context, t *testing.T, db *sql.DB, id string, quizID int64, at string) string {
	t.Helper()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO games (id, quiz_id, created_at) VALUES (?, ?, `+at+`)`,
		id, quizID,
	); err != nil {
		t.Fatalf("insert game err = %v, want nil", err)
	}

	return id
}

func insertParticipant(
	ctx context.Context,
	t *testing.T,
	db *sql.DB,
	gameID string,
	playerID, quizID int64,
	at string,
) {
	t.Helper()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO game_participants (game_id, player_id, quiz_id, joined_at) VALUES (?, ?, ?, `+at+`)`,
		gameID, playerID, quizID,
	); err != nil {
		t.Fatalf("insert participant err = %v, want nil", err)
	}
}

func insertGameQuestion(
	ctx context.Context,
	t *testing.T,
	db *sql.DB,
	gameID string,
	questionID int64,
	at string,
) int64 {
	t.Helper()
	res, err := db.ExecContext(ctx,
		`INSERT INTO game_questions (game_id, question_id, started_at, expired_at)
		 VALUES (?, ?, `+at+`, datetime(`+at+`, '+1 minute'))`,
		gameID, questionID,
	)
	if err != nil {
		t.Fatalf("insert game question err = %v, want nil", err)
	}

	return lastID(t, res)
}

func insertGameAnswer(
	ctx context.Context,
	t *testing.T,
	db *sql.DB,
	gameID string,
	playerID, gameQuestionID, optionID int64,
	at string,
) {
	t.Helper()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO game_answers (game_id, player_id, game_question_id, option_id, answered_at)
		 VALUES (?, ?, ?, ?, `+at+`)`,
		gameID, playerID, gameQuestionID, optionID,
	); err != nil {
		t.Fatalf("insert game answer err = %v, want nil", err)
	}
}

func insertSeenRound(
	ctx context.Context,
	t *testing.T,
	db *sql.DB,
	gameID string,
	roundID int64,
	phase, at string,
) {
	t.Helper()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO game_seen_rounds (game_id, round_id, phase, seen_at) VALUES (?, ?, ?, `+at+`)`,
		gameID, roundID, phase,
	); err != nil {
		t.Fatalf("insert seen round err = %v, want nil", err)
	}
}

func lastID(t *testing.T, res sql.Result) int64 {
	t.Helper()
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId err = %v, want nil", err)
	}

	return id
}

func countRows(ctx context.Context, t *testing.T, db *sql.DB, query string, arg any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx, query, arg).Scan(&n); err != nil {
		t.Fatalf("count query err = %v, want nil", err)
	}

	return n
}

func assertPlayerAbsent(ctx context.Context, t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	if got := countRows(ctx, t, db, `SELECT COUNT(*) FROM players WHERE id = ?`, id); got != 0 {
		t.Errorf("player %d rows = %d, want 0 (should be swept)", id, got)
	}
}

func assertPlayerPresent(ctx context.Context, t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	if got := countRows(ctx, t, db, `SELECT COUNT(*) FROM players WHERE id = ?`, id); got != 1 {
		t.Errorf("player %d rows = %d, want 1 (should survive)", id, got)
	}
}

func assertGameAbsent(ctx context.Context, t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if got := countRows(ctx, t, db, `SELECT COUNT(*) FROM games WHERE id = ?`, id); got != 0 {
		t.Errorf("game %q rows = %d, want 0 (should be swept)", id, got)
	}
}

func assertGamePresent(ctx context.Context, t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if got := countRows(ctx, t, db, `SELECT COUNT(*) FROM games WHERE id = ?`, id); got != 1 {
		t.Errorf("game %q rows = %d, want 1 (should survive)", id, got)
	}
}

// assertNoOrphans verifies every dependent table of a swept game is empty for
// that game id, including game_seen_rounds which relies on ON DELETE CASCADE.
func assertNoOrphans(ctx context.Context, t *testing.T, db *sql.DB, gameID string) {
	t.Helper()
	for _, table := range []string{"game_answers", "game_questions", "game_participants", "game_seen_rounds"} {
		got := countRows(ctx, t, db, `SELECT COUNT(*) FROM `+table+` WHERE game_id = ?`, gameID)
		if got != 0 {
			t.Errorf("%s rows for swept game %q = %d, want 0", table, gameID, got)
		}
	}
}
