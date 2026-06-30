package demo_test

import (
	"log/slog"
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

func TestSeedIfEnabled(t *testing.T) {
	t.Setenv("DEMO_MODE_ENABLED", "true")

	logger := slog.New(slog.DiscardHandler)
	stores := store.New(dbtest.Open(t), logger)
	mediaSvc := media.NewService(stores.Media, t.TempDir(),
		config.MediaImageMaxBytesDefault, config.MediaAudioMaxBytesDefault, logger)
	cfg := demoTestConfig()

	if err := demo.SeedIfEnabled(t.Context(), cfg, stores, mediaSvc, logger); err != nil {
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

	// Idempotent: a second call neither errors nor duplicates.
	err = demo.SeedIfEnabled(t.Context(), cfg, stores, mediaSvc, logger)
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
}

func TestSeedIfEnabled_NoopWhenDisabled(t *testing.T) {
	t.Setenv("DEMO_MODE_ENABLED", "false")

	logger := slog.New(slog.DiscardHandler)
	stores := store.New(dbtest.Open(t), logger)
	mediaSvc := media.NewService(stores.Media, t.TempDir(),
		config.MediaImageMaxBytesDefault, config.MediaAudioMaxBytesDefault, logger)

	if err := demo.SeedIfEnabled(t.Context(), demoTestConfig(), stores, mediaSvc, logger); err != nil {
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
