package migrations_test

import (
	"database/sql"
	"testing"

	"github.com/starquake/topbanana/internal/dbtest"
)

// TestMediaMigration_TableShape pins the #936 media schema: the table, its
// columns, the quiz_id index, and the two foreign keys exist after the
// migration runs against a fresh DB.
func TestMediaMigration_TableShape(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	wantCols := map[string]bool{
		"id": true, "quiz_id": true, "type": true, "mime": true,
		"path": true, "thumb_path": true, "width": true, "height": true,
		"size_bytes": true, "sha256": true, "created_by_player_id": true,
		"created_at": true,
	}
	gotCols := tableColumns(t, db, "media")
	for col := range wantCols {
		if !gotCols[col] {
			t.Errorf("media is missing column %q", col)
		}
	}

	if !indexExists(t, db, "media_quiz_id_idx") {
		t.Error("media_quiz_id_idx index is missing")
	}

	fkTargets := foreignKeyTargets(t, db, "media")
	if !fkTargets["quizzes"] {
		t.Error("media is missing a foreign key to quizzes")
	}
	if !fkTargets["players"] {
		t.Error("media is missing a foreign key to players")
	}
}

// TestMediaMigration_QuizDeleteCascades pins the quiz_id ON DELETE CASCADE
// against seeded rows: deleting a quiz drops its media rows, and an unrelated
// quiz's media survives.
func TestMediaMigration_QuizDeleteCascades(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	doomedQuiz := seedQuiz(t, db, "Doomed", "media-cascade-doomed")
	keepQuiz := seedQuiz(t, db, "Keep", "media-cascade-keep")
	seedMedia(t, db, doomedQuiz)
	seedMedia(t, db, doomedQuiz)
	keepMediaID := seedMedia(t, db, keepQuiz)

	if _, err := db.ExecContext(
		t.Context(), "DELETE FROM quizzes WHERE id = ?", doomedQuiz,
	); err != nil {
		t.Fatalf("delete quiz err = %v, want nil", err)
	}

	if got, want := countMedia(t, db, doomedQuiz), 0; got != want {
		t.Errorf("doomed quiz media count = %d, want %d (cascade)", got, want)
	}
	if got, want := countMedia(t, db, keepQuiz), 1; got != want {
		t.Errorf("kept quiz media count = %d, want %d", got, want)
	}
	if !mediaExists(t, db, keepMediaID) {
		t.Errorf("kept media id %d was removed, want it to survive", keepMediaID)
	}
}

// TestMediaMigration_NoIDReuseAfterDelete pins the AUTOINCREMENT switch from
// 20260616180000: after deleting the highest-id row, the next inserted row
// gets a strictly higher id, not the one we just freed. Without AUTOINCREMENT
// the deleted id would be recycled and concurrent uploads would race on the
// reused filesystem paths (#951).
func TestMediaMigration_NoIDReuseAfterDelete(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "Reuse Probe", "media-no-id-reuse")
	first := seedMedia(t, db, quizID)
	second := seedMedia(t, db, quizID)

	if _, err := db.ExecContext(
		t.Context(), "DELETE FROM media WHERE id = ?", second,
	); err != nil {
		t.Fatalf("delete media err = %v, want nil", err)
	}

	next := seedMedia(t, db, quizID)
	if next == second {
		t.Errorf("media id %d was recycled after delete (first = %d, deleted = %d, next = %d)",
			next, first, second, next)
	}
	if next <= second {
		t.Errorf("next media id = %d, want > %d (monotonic AUTOINCREMENT)", next, second)
	}
}

// TestMediaReadyMigration_ColumnAndIndex pins the #992 two-phase flag: the
// ready column and its partial index exist, ready defaults to 1 (so pre-existing
// rows count as ready), and the CHECK constraint rejects an out-of-range value.
func TestMediaReadyMigration_ColumnAndIndex(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	if !tableColumns(t, db, "media")["ready"] {
		t.Error("media is missing the ready column")
	}
	if !indexExists(t, db, "media_not_ready_idx") {
		t.Error("media_not_ready_idx index is missing")
	}

	quizID := seedQuiz(t, db, "Ready Default", "media-ready-default")
	id := seedMedia(t, db, quizID)

	var ready int
	if err := db.QueryRowContext(
		t.Context(), "SELECT ready FROM media WHERE id = ?", id,
	).Scan(&ready); err != nil {
		t.Fatalf("select ready err = %v, want nil", err)
	}
	if got, want := ready, 1; got != want {
		t.Errorf("ready default = %d, want %d", got, want)
	}

	if _, err := db.ExecContext(
		t.Context(), "UPDATE media SET ready = 2 WHERE id = ?", id,
	); err == nil {
		t.Error("UPDATE ready = 2 err = nil, want a CHECK constraint violation")
	}
}

// seedMedia inserts a minimal media row for quizID (created_by the seeded admin
// id 1) and returns its id.
func seedMedia(t *testing.T, db *sql.DB, quizID int64) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRowContext(
		t.Context(),
		`INSERT INTO media (quiz_id, mime, path, size_bytes, sha256, created_by_player_id)
		 VALUES (?, 'image/jpeg', 'p.jpg', 10, 'deadbeef', 1) RETURNING id`,
		quizID,
	).Scan(&id); err != nil {
		t.Fatalf("seed media err = %v, want nil", err)
	}

	return id
}

func countMedia(t *testing.T, db *sql.DB, quizID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(
		t.Context(), "SELECT count(*) FROM media WHERE quiz_id = ?", quizID,
	).Scan(&n); err != nil {
		t.Fatalf("count media err = %v, want nil", err)
	}

	return n
}

func mediaExists(t *testing.T, db *sql.DB, id int64) bool {
	t.Helper()
	var n int
	if err := db.QueryRowContext(
		t.Context(), "SELECT count(*) FROM media WHERE id = ?", id,
	).Scan(&n); err != nil {
		t.Fatalf("media exists err = %v, want nil", err)
	}

	return n == 1
}

// tableColumns returns the set of column names on the named table.
func tableColumns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.QueryContext(
		t.Context(), "SELECT name FROM pragma_table_info(?)", table,
	)
	if err != nil {
		t.Fatalf("pragma_table_info err = %v, want nil", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			t.Errorf("rows.Close err = %v", cerr)
		}
	}()

	cols := map[string]bool{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatalf("scan column name err = %v, want nil", err)
		}
		cols[name] = true
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("rows err = %v, want nil", err)
	}

	return cols
}

// indexExists reports whether an index with the given name exists.
func indexExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRowContext(
		t.Context(),
		"SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?", name,
	).Scan(&n); err != nil {
		t.Fatalf("index exists err = %v, want nil", err)
	}

	return n == 1
}

// foreignKeyTargets returns the set of tables the named table foreign-keys to.
func foreignKeyTargets(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.QueryContext(
		t.Context(), "SELECT \"table\" FROM pragma_foreign_key_list(?)", table,
	)
	if err != nil {
		t.Fatalf("pragma_foreign_key_list err = %v, want nil", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			t.Errorf("rows.Close err = %v", cerr)
		}
	}()

	targets := map[string]bool{}
	for rows.Next() {
		var target string
		if err = rows.Scan(&target); err != nil {
			t.Fatalf("scan fk target err = %v, want nil", err)
		}
		targets[target] = true
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("rows err = %v, want nil", err)
	}

	return targets
}
