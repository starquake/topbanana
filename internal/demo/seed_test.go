package demo_test

import (
	"log/slog"
	"os"
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

// demoArchive reads the real demo quiz archive from dev/fixtures/ and returns
// the bytes. The path resolves from internal/demo/ to the repo root.
func demoArchive(t *testing.T) []byte {
	t.Helper()

	raw, err := os.ReadFile("../../dev/fixtures/demo-quiz.zip")
	if err != nil {
		t.Fatalf("read demo archive: %v", err)
	}

	return raw
}

func TestSeedIfEnabled(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	stores := store.New(dbtest.Open(t), logger)
	mediaSvc := media.NewService(stores.Media, t.TempDir(),
		config.MediaImageMaxBytesDefault, config.MediaAudioMaxBytesDefault, logger)
	cfg := demoTestConfig()
	cfg.DemoMode = true
	archive := demoArchive(t)

	if err := demo.SeedIfEnabled(t.Context(), cfg, stores, mediaSvc, logger, archive); err != nil {
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
	if got, want := len(quizzes), 1; got != want {
		t.Errorf("quiz count = %d, want %d", got, want)
	}

	// The demo quiz must appear in the Popular list with at least one play.
	popular, err := stores.Home.ListPopularQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListPopularQuizzes() err = %v, want nil", err)
	}
	if got, want := len(popular), 1; got != want {
		t.Fatalf("popular quiz count = %d, want %d", got, want)
	}
	if got, want := popular[0].Title, quizzes[0].Title; got != want {
		t.Errorf("popular[0].Title = %q, want %q", got, want)
	}
	if got := popular[0].PlayCount; got <= 0 {
		t.Errorf("popular[0].PlayCount = %d, want > 0", got)
	}
	firstPlayCount := popular[0].PlayCount

	// Idempotent: a second call neither errors, duplicates the quiz, nor
	// increases the play count (the quiz already exists so plays are skipped).
	err = demo.SeedIfEnabled(t.Context(), cfg, stores, mediaSvc, logger, archive)
	if err != nil {
		t.Fatalf("second SeedIfEnabled() err = %v, want nil", err)
	}
	quizzes, err = stores.Quizzes.ListQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListQuizzes() after re-seed err = %v, want nil", err)
	}
	if got, want := len(quizzes), 1; got != want {
		t.Errorf("quiz count after re-seed = %d, want %d (idempotent)", got, want)
	}
	popular, err = stores.Home.ListPopularQuizzes(t.Context())
	if err != nil {
		t.Fatalf("ListPopularQuizzes() after re-seed err = %v, want nil", err)
	}
	if got, want := len(popular), 1; got != want {
		t.Fatalf("popular quiz count after re-seed = %d, want %d", got, want)
	}
	if got, want := popular[0].PlayCount, firstPlayCount; got != want {
		t.Errorf("popular[0].PlayCount after re-seed = %d, want %d (idempotent)", got, want)
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
