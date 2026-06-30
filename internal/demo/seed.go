package demo

import (
	"archive/zip"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"

	"github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// demoHostName is the display name of the shared demo Host that owns the demo
// quiz and that /demo/enter logs visitors into. It is the stable lookup key.
const demoHostName = "Demo Host"

//go:embed baseline/demo-quiz.zip
var baselineArchive []byte

// SeedIfEnabled ensures the demo baseline (the shared demo Host and the demo
// quiz) exists when demo mode is on. It is idempotent - a present host or quiz
// is left as-is - so it is safe to call on every boot, which is how a
// freshly-reset demo DB gets its content back. A no-op when demo mode is off.
func SeedIfEnabled(
	ctx context.Context, cfg *config.Config,
	stores *store.Stores, mediaSvc *media.Service, logger *slog.Logger,
) error {
	if !Enabled() {
		return nil
	}

	hostID, err := ensureDemoHost(ctx, stores.Players, stores.AdminPlayers)
	if err != nil {
		return fmt.Errorf("ensure demo host: %w", err)
	}
	if err := ensureDemoQuiz(ctx, cfg, stores.Quizzes, mediaSvc, hostID, logger); err != nil {
		return fmt.Errorf("ensure demo quiz: %w", err)
	}

	return nil
}

// ensureDemoHost returns the id of the shared demo Host, creating it on first
// call. It starts the account as an anonymous player (which sidesteps the
// "first credentialled registrant becomes admin" rule) then elevates it to a
// verified Host - the role + verified-email gates that the host routes enforce.
func ensureDemoHost(ctx context.Context, players auth.PlayerStore, adminPlayers auth.AdminPlayerStore) (int64, error) {
	host, err := players.GetPlayerByDisplayName(ctx, demoHostName)
	if err != nil && !errors.Is(err, auth.ErrPlayerNotFound) {
		return 0, fmt.Errorf("get player by display name: %w", err)
	}
	if err == nil && host.Role == auth.RoleHost && host.IsEmailVerified() {
		return host.ID, nil
	}

	if errors.Is(err, auth.ErrPlayerNotFound) {
		host, err = players.CreateAnonymousPlayer(ctx, demoHostName)
		if errors.Is(err, auth.ErrDisplayNameTaken) {
			// A concurrent boot won the race; look up the winner.
			host, err = players.GetPlayerByDisplayName(ctx, demoHostName)
		}
		if err != nil {
			return 0, fmt.Errorf("create anonymous player: %w", err)
		}
	}
	if err := adminPlayers.SetPlayerRole(ctx, host.ID, auth.RoleHost); err != nil {
		return 0, fmt.Errorf("set player role: %w", err)
	}
	if err := adminPlayers.SetPlayerEmailVerifiedNow(ctx, host.ID); err != nil {
		return 0, fmt.Errorf("set player email verified: %w", err)
	}

	return host.ID, nil
}

// ensureDemoQuiz restores the embedded baseline quiz attributed to the demo Host
// through the same HTTP-free import path the admin upload uses. A slug collision
// (the quiz already exists) is the idempotent no-op.
func ensureDemoQuiz(
	ctx context.Context, cfg *config.Config,
	quizzes quiz.Store, mediaSvc *media.Service, hostID int64, logger *slog.Logger,
) error {
	zr, err := zip.NewReader(bytes.NewReader(baselineArchive), int64(len(baselineArchive)))
	if err != nil {
		return fmt.Errorf("open baseline archive: %w", err)
	}
	limits := admin.NewArchiveImportLimits(
		cfg.MediaImageMaxBytes, cfg.MediaAudioMaxBytes, cfg.MediaImportMaxBytes,
	)
	if _, err := admin.ImportQuizArchive(ctx, logger, quizzes, mediaSvc, zr, hostID, limits); err != nil {
		if errors.Is(err, quiz.ErrSlugTaken) {
			return nil
		}

		return fmt.Errorf("import quiz archive: %w", err)
	}

	return nil
}
