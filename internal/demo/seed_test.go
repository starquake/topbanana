package demo_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/demo"
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

	const dir = "../../dev/fixtures/demo"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read demo archive dir: %v", err)
	}
	var archives [][]byte
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read demo archive %q: %v", entry.Name(), err)
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
