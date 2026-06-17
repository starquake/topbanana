package media_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/starquake/topbanana/internal/dbtest"
	. "github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/store"
)

// seededAdminID matches the migration-seeded admin player (id 1); media rows
// FK created_by_player_id to it.
const seededAdminID int64 = 1

// fixture bundles a media Service with the DB, quiz id, and temp root it was
// built over so tests can reach whichever it needs without unpacking a wide
// tuple. The DB is exposed so a test can seed a second quiz in the same DB to
// prove per-quiz scoping or delete a quiz to prove the cascade.
type fixture struct {
	svc    *Service
	db     *sql.DB
	quizID int64
	root   string
}

// newServiceWithQuiz opens a migrated dbtest DB, seeds a quiz owned by the
// seeded admin, and returns a media Service writing under a fresh temp dir. It
// skips under -short via dbtest.Open.
func newServiceWithQuiz(t *testing.T) fixture {
	t.Helper()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "media-svc")
	root := t.TempDir()
	svc := NewService(store.NewMediaStore(db, slog.Default()), root, slog.Default())

	return fixture{svc: svc, db: db, quizID: quizID, root: root}
}

func seedQuiz(t *testing.T, db *sql.DB, slug string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRowContext(
		t.Context(),
		`INSERT INTO quizzes (title, slug, description, created_by_player_id)
		 VALUES ('Media', ?, 'd', 1) RETURNING id`,
		slug,
	).Scan(&id); err != nil {
		t.Fatalf("seed quiz %q err = %v, want nil", slug, err)
	}

	return id
}

// pngUpload returns a w x h PNG with a deterministic gradient fill, a valid
// upload the Service can process.
func pngUpload(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode err = %v, want nil", err)
	}

	return buf.Bytes()
}

// TestServiceStoreRoundTrip pins the full persist path: Store processes and
// writes the full + thumb jpeg under <root>/<quizID>/ and records a row with
// the metadata the pipeline computed and the relative paths.
func TestServiceStoreRoundTrip(t *testing.T) {
	t.Parallel()

	fx := newServiceWithQuiz(t)

	m, err := fx.svc.Store(t.Context(), fx.quizID, seededAdminID, bytes.NewReader(pngUpload(t, 800, 400)))
	if err != nil {
		t.Fatalf("Store err = %v, want nil", err)
	}

	if got, want := m.QuizID, fx.quizID; got != want {
		t.Errorf("QuizID = %d, want %d", got, want)
	}
	if got, want := m.Type, TypeImage; got != want {
		t.Errorf("Type = %q, want %q", got, want)
	}
	if got, want := m.MIME, "image/jpeg"; got != want {
		t.Errorf("MIME = %q, want %q", got, want)
	}
	if got, want := m.Width, 800; got != want {
		t.Errorf("Width = %d, want %d", got, want)
	}
	if got, want := m.Height, 400; got != want {
		t.Errorf("Height = %d, want %d", got, want)
	}
	if m.SizeBytes <= 0 {
		t.Errorf("SizeBytes = %d, want > 0", m.SizeBytes)
	}
	if len(m.SHA256) != 64 {
		t.Errorf("len(SHA256) = %d, want 64 hex chars", len(m.SHA256))
	}

	fullStat, err := os.Stat(filepath.Join(fx.root, m.Path))
	if err != nil {
		t.Fatalf("stat full file err = %v, want nil", err)
	}
	if got, want := fullStat.Size(), m.SizeBytes; got != want {
		t.Errorf("full file size = %d, want %d (SizeBytes)", got, want)
	}
	if _, err = os.Stat(filepath.Join(fx.root, m.ThumbPath)); err != nil {
		t.Fatalf("stat thumb file err = %v, want nil", err)
	}

	// The DB row must agree with the returned struct.
	row, err := fx.svc.Get(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("Get err = %v, want nil", err)
	}
	if got, want := row.Path, m.Path; got != want {
		t.Errorf("stored Path = %q, want %q", got, want)
	}
	if got, want := row.SHA256, m.SHA256; got != want {
		t.Errorf("stored SHA256 = %q, want %q", got, want)
	}
}

// TestServiceStoreCancelledBeforeProcessing pins the ctx-cancelled short-
// circuit: a cancel that reaches the handler before Process runs returns the
// cancel error AND leaves no row + no files behind, so the host's apparent
// cancel matches the server-side state (#951).
func TestServiceStoreCancelledBeforeProcessing(t *testing.T) {
	t.Parallel()

	fx := newServiceWithQuiz(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := fx.svc.Store(ctx, fx.quizID, seededAdminID, bytes.NewReader(pngUpload(t, 64, 64)))
	if got, want := err, context.Canceled; !errors.Is(got, want) {
		t.Errorf("Store err = %v, want %v", got, want)
	}

	rows, err := fx.svc.ListByQuiz(t.Context(), fx.quizID)
	if err != nil {
		t.Fatalf("ListByQuiz err = %v, want nil", err)
	}
	if got, want := len(rows), 0; got != want {
		t.Errorf("rows after cancelled Store = %d, want %d", got, want)
	}

	entries, err := os.ReadDir(fx.root)
	if err != nil {
		t.Fatalf("ReadDir err = %v, want nil", err)
	}
	if got, want := len(entries), 0; got != want {
		t.Errorf("files under root after cancelled Store = %d, want %d", got, want)
	}
}

// cancelOnUpdatePathsStore wraps a real Store to fire a registered cancel
// function the moment UpdateMediaPaths is invoked, simulating a client that
// closes the connection between CreateMedia and UpdateMediaPaths. Without
// this, a pre-cancelled context short-circuits before any row or file is
// created and the cleanup paths in Service.Store are never exercised.
type cancelOnUpdatePathsStore struct {
	Store

	cancel context.CancelFunc
}

func (c *cancelOnUpdatePathsStore) UpdateMediaPaths(ctx context.Context, id int64, path, thumbPath string) error {
	c.cancel()

	return c.Store.UpdateMediaPaths(ctx, id, path, thumbPath)
}

// TestServiceStoreCancelledMidFlightCleansUp pins the in-flight cancel
// cleanup: when the connection drops between CreateMedia (row + files just
// written) and UpdateMediaPaths, Store must return an error AND tear the row
// + files back down via the cancel-immune cleanup path (#951). The vacuous
// pre-Process variant above can't exercise this branch.
func TestServiceStoreCancelledMidFlightCleansUp(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	quizID := seedQuiz(t, db, "media-svc-cancel-midflight")
	root := t.TempDir()
	innerStore := store.NewMediaStore(db, slog.Default())
	ctx, cancel := context.WithCancel(t.Context())
	wrapped := &cancelOnUpdatePathsStore{Store: innerStore, cancel: cancel}
	svc := NewService(wrapped, root, slog.Default())

	_, err := svc.Store(ctx, quizID, seededAdminID, bytes.NewReader(pngUpload(t, 64, 64)))
	if err == nil {
		t.Fatal("Store err = nil, want non-nil (ctx was cancelled mid-flight)")
	}

	// Use a fresh ctx for verification so the cancelled ctx doesn't taint
	// the post-conditions checks.
	probeCtx := t.Context()
	rows, err := svc.ListByQuiz(probeCtx, quizID)
	if err != nil {
		t.Fatalf("ListByQuiz err = %v, want nil", err)
	}
	if got, want := len(rows), 0; got != want {
		t.Errorf("rows after mid-flight cancel = %d, want %d (cleanup branch did not delete)", got, want)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir err = %v, want nil", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub, derr := os.ReadDir(filepath.Join(root, e.Name()))
		if derr != nil {
			t.Fatalf("ReadDir(%s) err = %v, want nil", e.Name(), derr)
		}
		if got := len(sub); got != 0 {
			t.Errorf("files under root/%s after mid-flight cancel = %d, want 0"+
				" (writeFiles cleanup did not remove)", e.Name(), got)
		}
	}
}

// TestServiceStoreCreateMediaFailureLeavesNoDir pins that a failed media-row
// insert leaves no per-quiz directory on disk (#998). The directory is created
// only after CreateMedia succeeds, so a quiz whose very first upload fails at
// the insert step does not accumulate a stray empty dir under root. The closed
// DB forces CreateMedia to error after Process has run.
func TestServiceStoreCreateMediaFailureLeavesNoDir(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizID := seedQuiz(t, db, "media-svc-insert-fail")
	root := t.TempDir()
	svc := NewService(store.NewMediaStore(db, slog.Default()), root, slog.Default())

	// Close the DB so the CreateMedia insert fails; Process runs first and
	// does not touch the DB, so the failure lands exactly at the insert.
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close err = %v, want nil", err)
	}

	_, err := svc.Store(t.Context(), quizID, seededAdminID, bytes.NewReader(pngUpload(t, 64, 64)))
	if err == nil {
		t.Fatal("Store err = nil, want non-nil (CreateMedia ran against a closed DB)")
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir err = %v, want nil", err)
	}
	if got, want := len(entries), 0; got != want {
		t.Errorf("entries under root after failed insert = %d, want %d (no stray per-quiz dir)", got, want)
	}
}

// TestServiceDeleteRemovesRowAndFiles pins that Delete drops the row and unlinks
// both files.
func TestServiceDeleteRemovesRowAndFiles(t *testing.T) {
	t.Parallel()

	fx := newServiceWithQuiz(t)

	m, err := fx.svc.Store(t.Context(), fx.quizID, seededAdminID, bytes.NewReader(pngUpload(t, 320, 240)))
	if err != nil {
		t.Fatalf("Store err = %v, want nil", err)
	}

	if err = fx.svc.Delete(t.Context(), m.ID); err != nil {
		t.Fatalf("Delete err = %v, want nil", err)
	}

	if _, err = fx.svc.Get(t.Context(), m.ID); !errors.Is(err, ErrMediaNotFound) {
		t.Errorf("Get after delete err = %v, want ErrMediaNotFound", err)
	}
	if _, err = os.Stat(filepath.Join(fx.root, m.Path)); !os.IsNotExist(err) {
		t.Errorf("full file stat err = %v, want not-exist", err)
	}
	if _, err = os.Stat(filepath.Join(fx.root, m.ThumbPath)); !os.IsNotExist(err) {
		t.Errorf("thumb file stat err = %v, want not-exist", err)
	}
}

// TestServiceDeleteMissing pins that deleting a non-existent id returns
// ErrMediaNotFound rather than a generic error.
func TestServiceDeleteMissing(t *testing.T) {
	t.Parallel()

	fx := newServiceWithQuiz(t)

	if err := fx.svc.Delete(t.Context(), 999); !errors.Is(err, ErrMediaNotFound) {
		t.Errorf("Delete(missing) err = %v, want ErrMediaNotFound", err)
	}
}

// TestServiceListByQuizScoped pins that ListByQuiz returns only the quiz's own
// media, newest first.
func TestServiceListByQuizScoped(t *testing.T) {
	t.Parallel()

	fx := newServiceWithQuiz(t)
	// A second quiz in the same DB whose media must not leak into quizA's list.
	quizB := seedQuiz(t, fx.db, "media-svc-b")

	first, err := fx.svc.Store(t.Context(), fx.quizID, seededAdminID, bytes.NewReader(pngUpload(t, 100, 100)))
	if err != nil {
		t.Fatalf("Store first err = %v, want nil", err)
	}
	second, err := fx.svc.Store(t.Context(), fx.quizID, seededAdminID, bytes.NewReader(pngUpload(t, 120, 120)))
	if err != nil {
		t.Fatalf("Store second err = %v, want nil", err)
	}
	if _, err = fx.svc.Store(t.Context(), quizB, seededAdminID, bytes.NewReader(pngUpload(t, 80, 80))); err != nil {
		t.Fatalf("Store quizB err = %v, want nil", err)
	}

	list, err := fx.svc.ListByQuiz(t.Context(), fx.quizID)
	if err != nil {
		t.Fatalf("ListByQuiz err = %v, want nil", err)
	}
	if got, want := len(list), 2; got != want {
		t.Fatalf("len(list) = %d, want %d (quizB media must not leak)", got, want)
	}
	// Newest first: the second upload leads.
	if got, want := list[0].ID, second.ID; got != want {
		t.Errorf("list[0].ID = %d, want %d (newest first)", got, want)
	}
	if got, want := list[1].ID, first.ID; got != want {
		t.Errorf("list[1].ID = %d, want %d", got, want)
	}
}

// TestServiceOpenRoundTrip pins that Open reads back the exact stored full-image
// bytes for a media row's path.
func TestServiceOpenRoundTrip(t *testing.T) {
	t.Parallel()

	fx := newServiceWithQuiz(t)

	m, err := fx.svc.Store(t.Context(), fx.quizID, seededAdminID, bytes.NewReader(pngUpload(t, 200, 200)))
	if err != nil {
		t.Fatalf("Store err = %v, want nil", err)
	}

	f, err := fx.svc.Open(m.Path)
	if err != nil {
		t.Fatalf("Open err = %v, want nil", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Errorf("file Close err = %v", cerr)
		}
	}()

	var buf bytes.Buffer
	if _, err = buf.ReadFrom(f); err != nil {
		t.Fatalf("read opened file err = %v, want nil", err)
	}
	if got, want := int64(buf.Len()), m.SizeBytes; got != want {
		t.Errorf("opened file size = %d, want %d (SizeBytes)", got, want)
	}
}

// TestServiceOpenRejectsTraversal pins that Open refuses a path climbing out of
// the media root, so a corrupt or hostile DB value cannot read an arbitrary
// file.
func TestServiceOpenRejectsTraversal(t *testing.T) {
	t.Parallel()

	fx := newServiceWithQuiz(t)

	if _, err := fx.svc.Open("../../etc/passwd"); !errors.Is(err, ErrPathEscapesRoot) {
		t.Errorf("Open(traversal) err = %v, want ErrPathEscapesRoot", err)
	}
}

// TestServiceQuizDeleteCascadesMedia pins the media.quiz_id ON DELETE CASCADE:
// deleting the owning quiz row drops the quiz's media rows.
func TestServiceQuizDeleteCascadesMedia(t *testing.T) {
	t.Parallel()

	fx := newServiceWithQuiz(t)

	m, err := fx.svc.Store(t.Context(), fx.quizID, seededAdminID, bytes.NewReader(pngUpload(t, 160, 90)))
	if err != nil {
		t.Fatalf("Store err = %v, want nil", err)
	}

	if _, err = fx.db.ExecContext(t.Context(), "DELETE FROM quizzes WHERE id = ?", fx.quizID); err != nil {
		t.Fatalf("delete quiz err = %v, want nil", err)
	}

	if _, err = fx.svc.Get(t.Context(), m.ID); !errors.Is(err, ErrMediaNotFound) {
		t.Errorf("Get after quiz delete err = %v, want ErrMediaNotFound (cascade)", err)
	}
}
