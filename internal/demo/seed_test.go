package demo_test

import (
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/demo"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/store"
)

// demoTestConfig is a minimal config with the media caps SeedIfEnabled needs.
func demoTestConfig() *config.Config {
	return &config.Config{
		AppEnvironment:      config.AppEnvironmentDefault,
		MediaImageMaxBytes:  config.MediaImageMaxBytesDefault,
		MediaAudioMaxBytes:  config.MediaAudioMaxBytesDefault,
		MediaImportMaxBytes: config.MediaImportMaxBytesDefault,
	}
}

// demoArchives reads every committed demo quiz archive from dev/fixtures/demo/
// (sorted by filename) and returns one byte slice per archive. The path resolves
// from internal/demo/ to the repo root.
func demoArchives(t *testing.T) [][]byte {
	t.Helper()

	paths, err := demo.ArchivePaths("../../dev/fixtures/demo")
	if err != nil {
		t.Fatalf("ArchivePaths() err = %v, want nil", err)
	}
	archives := make([][]byte, 0, len(paths))
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read demo archive %q: %v", path, err)
		}
		archives = append(archives, raw)
	}
	if len(archives) < 2 {
		t.Fatalf("demo archive count = %d, want at least 2 (multi-quiz set)", len(archives))
	}

	return archives
}

func TestSeedIfEnabled(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	stores := store.New(dbtest.Open(t), logger)
	mediaSvc := media.NewService(stores.Media, t.TempDir(),
		config.MediaImageMaxBytesDefault, config.MediaAudioMaxBytesDefault, logger)
	cfg := demoTestConfig()
	cfg.DemoMode = true
	archives := demoArchives(t)

	if err := demo.SeedIfEnabled(t.Context(), cfg, stores, mediaSvc, logger, archives); err != nil {
		t.Fatalf("SeedIfEnabled() err = %v, want nil", err)
	}

	host, err := stores.Players.GetPlayerByDisplayName(t.Context(), "Demo Host")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName() err = %v, want nil", err)
	}
	if got, want := host.Role, auth.RoleHost; got != want {
		t.Errorf("host Role = %q, want %q", got, want)
	}
	if got, want := host.IsEmailVerified(), true; got != want {
		t.Errorf("host IsEmailVerified() = %v, want %v", got, want)
	}

	quizzes, err := stores.Quizzes.ListQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListQuizzes() err = %v, want nil", err)
	}
	if got, want := len(quizzes), len(archives); got != want {
		t.Errorf("quiz count = %d, want %d (one per archive)", got, want)
	}

	// Every imported demo quiz must be published, not left a draft, or it 404s
	// on play (#1192).
	for _, q := range quizzes {
		full, getErr := stores.Quizzes.GetQuiz(t.Context(), q.ID)
		if getErr != nil {
			t.Fatalf("GetQuiz(%d) err = %v, want nil", q.ID, getErr)
		}
		if got, want := full.Published, true; got != want {
			t.Errorf("demo quiz %q Published = %v, want %v", q.Title, got, want)
		}
	}

	// Every demo quiz must appear in the Popular list with at least one play.
	popular, err := stores.Home.ListPopularQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListPopularQuizzes() err = %v, want nil", err)
	}
	if got, want := len(popular), len(archives); got != want {
		t.Fatalf("popular quiz count = %d, want %d (one per archive)", got, want)
	}
	firstPlayCounts := make(map[string]int, len(popular))
	for _, p := range popular {
		if p.PlayCount <= 0 {
			t.Errorf("popular quiz %q PlayCount = %d, want > 0", p.Title, p.PlayCount)
		}
		firstPlayCounts[p.Title] = p.PlayCount
	}

	// Idempotent: a second call neither errors, duplicates a quiz, nor increases
	// any play count (every quiz already exists so plays are skipped).
	err = demo.SeedIfEnabled(t.Context(), cfg, stores, mediaSvc, logger, archives)
	if err != nil {
		t.Fatalf("second SeedIfEnabled() err = %v, want nil", err)
	}
	quizzes, err = stores.Quizzes.ListQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListQuizzes() after re-seed err = %v, want nil", err)
	}
	if got, want := len(quizzes), len(archives); got != want {
		t.Errorf("quiz count after re-seed = %d, want %d (idempotent)", got, want)
	}
	popular, err = stores.Home.ListPopularQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListPopularQuizzes() after re-seed err = %v, want nil", err)
	}
	if got, want := len(popular), len(archives); got != want {
		t.Fatalf("popular quiz count after re-seed = %d, want %d", got, want)
	}
	for _, p := range popular {
		if got, want := p.PlayCount, firstPlayCounts[p.Title]; got != want {
			t.Errorf("popular quiz %q PlayCount after re-seed = %d, want %d (idempotent)", p.Title, got, want)
		}
	}
}

// TestSeedIfEnabled_CorruptArchiveNoPartialSeed pins that a corrupt zip in the
// set fails before any DB side effect: the demo Host and the earlier quizzes
// must not be created, so a bad archive never leaves a partially-seeded set.
func TestSeedIfEnabled_CorruptArchiveNoPartialSeed(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	stores := store.New(dbtest.Open(t), logger)
	mediaSvc := media.NewService(stores.Media, t.TempDir(),
		config.MediaImageMaxBytesDefault, config.MediaAudioMaxBytesDefault, logger)
	cfg := demoTestConfig()
	cfg.DemoMode = true

	// A valid archive first, then a corrupt one: the corrupt zip must abort the
	// whole seed even though the valid one precedes it.
	archives := [][]byte{demoArchives(t)[0], []byte("not a zip")}

	if err := demo.SeedIfEnabled(t.Context(), cfg, stores, mediaSvc, logger, archives); err == nil {
		t.Fatal("SeedIfEnabled() err = nil, want non-nil for a corrupt archive")
	}

	if _, err := stores.Players.GetPlayerByDisplayName(t.Context(), "Demo Host"); err == nil {
		t.Error("demo host was created despite a corrupt archive, want none")
	}
	quizzes, err := stores.Quizzes.ListQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListQuizzes() err = %v, want nil", err)
	}
	if got, want := len(quizzes), 0; got != want {
		t.Errorf("quiz count = %d, want %d (no partial seed)", got, want)
	}
}

// TestSeedIfEnabled_SkipsPreExistingPoolName pins that a pool display name
// already held by a pre-existing account is never attributed a synthesised demo
// play, so a real player's history and play_count stay untouched.
func TestSeedIfEnabled_SkipsPreExistingPoolName(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	stores := store.New(dbtest.Open(t), logger)
	mediaSvc := media.NewService(stores.Media, t.TempDir(),
		config.MediaImageMaxBytesDefault, config.MediaAudioMaxBytesDefault, logger)
	cfg := demoTestConfig()
	cfg.DemoMode = true

	// A real account already holds the first pool display name.
	const takenName = "Allegro Alicia"
	existing, err := stores.Players.CreateAnonymousPlayer(t.Context(), takenName)
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer() err = %v, want nil", err)
	}

	if seedErr := demo.SeedIfEnabled(t.Context(), cfg, stores, mediaSvc, logger, demoArchives(t)); seedErr != nil {
		t.Fatalf("SeedIfEnabled() err = %v, want nil", seedErr)
	}

	quizzes, err := stores.Quizzes.ListQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListQuizzes() err = %v, want nil", err)
	}
	for _, q := range quizzes {
		_, err := stores.Games.GetGameByPlayerAndQuiz(t.Context(), existing.ID, q.ID)
		if got, want := err, game.ErrGameNotFound; !errors.Is(got, want) {
			t.Errorf("GetGameByPlayerAndQuiz(pre-existing %q, quiz %q) err = %v, want %v",
				takenName, q.Title, got, want)
		}
	}
}

func TestSeedIfEnabled_NoopWhenDisabled(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	stores := store.New(dbtest.Open(t), logger)
	mediaSvc := media.NewService(stores.Media, t.TempDir(),
		config.MediaImageMaxBytesDefault, config.MediaAudioMaxBytesDefault, logger)

	if err := demo.SeedIfEnabled(t.Context(), demoTestConfig(), stores, mediaSvc, logger, nil); err != nil {
		t.Fatalf("SeedIfEnabled() err = %v, want nil", err)
	}
	if _, err := stores.Players.GetPlayerByDisplayName(t.Context(), "Demo Host"); err == nil {
		t.Error("demo host was created while disabled, want none")
	}
	quizzes, err := stores.Quizzes.ListQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListQuizzes() err = %v, want nil", err)
	}
	if got, want := len(quizzes), 0; got != want {
		t.Errorf("quiz count = %d, want %d (disabled)", got, want)
	}
}
