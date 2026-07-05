package demo

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// demoHostName is the display name of the shared demo Host that owns the demo
// quiz and that /demo/enter logs visitors into. It is the stable lookup key.
const demoHostName = "Demo Host"

// demoAnswerWindowSeconds is the per-question window stamped onto synthesised
// game_questions rows. The seeder does not run a real game clock, so the value
// only needs to be long enough that a later reader treats the question as
// normally finished.
const demoAnswerWindowSeconds = 10

// demoPlayerNames is a small pool of imaginative display names for the
// anonymous players seeded alongside the demo quizzes; the same pool backs the
// finished games recorded against every quiz in the demo set.
//
//nolint:gochecknoglobals // dictionary table; values never mutate.
var demoPlayerNames = []string{
	"Allegro Alicia", "Fortissimo Frank", "Tempo Tilda", "Crescendo Carlo",
	"Aria Amelia", "Maestro Milo", "Cadenza Kate", "Nocturne Ned",
}

// SeedIfEnabled ensures the demo baseline (the shared demo Host and the demo
// quiz set) exists when demo mode is on. It is idempotent - a present host or
// quiz is left as-is - so re-running it against a freshly-reset demo DB restores
// the content. A no-op when cfg.DemoMode is off. archives holds the raw bytes of
// each demo quiz zip, in the order they should be restored; the files are read
// from the DEMO_SEED_ARCHIVE_DIR directory by the caller and not embedded in the
// binary.
func SeedIfEnabled(
	ctx context.Context, cfg *config.Config,
	stores *store.Stores, mediaSvc *media.Service, logger *slog.Logger,
	archives [][]byte,
) error {
	if !cfg.DemoMode {
		return nil
	}

	// Open every archive up front so a corrupt zip container fails before any DB
	// side effect, never leaving a partially-seeded demo set.
	readers := make([]*zip.Reader, 0, len(archives))
	for i, archive := range archives {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return fmt.Errorf("open demo archive %d: %w", i, err)
		}
		readers = append(readers, zr)
	}

	hostID, err := ensureDemoHost(ctx, stores.Players, stores.AdminPlayers)
	if err != nil {
		return fmt.Errorf("ensure demo host: %w", err)
	}

	var created []*quiz.Quiz
	for _, zr := range readers {
		qz, importErr := ensureDemoQuiz(ctx, cfg, stores.Quizzes, mediaSvc, hostID, logger, zr)
		if importErr != nil {
			return fmt.Errorf("ensure demo quiz: %w", importErr)
		}
		if qz != nil {
			created = append(created, qz)
		}
	}
	if len(created) == 0 {
		return nil
	}

	poolIDs, err := buildDemoPlayerPool(ctx, stores.Players)
	if err != nil {
		return fmt.Errorf("build demo player pool: %w", err)
	}
	for _, qz := range created {
		seedDemoPlays(ctx, stores.Games, qz, poolIDs, logger)
	}

	return nil
}

// buildDemoPlayerPool creates one anonymous player per name in demoPlayerNames
// and returns the ids of those it created. A name already taken is skipped
// (not looked up) so a real, pre-existing account holding a pool name is never
// attributed a synthesised demo play. Any other create error aborts.
func buildDemoPlayerPool(ctx context.Context, players auth.PlayerStore) ([]int64, error) {
	ids := make([]int64, 0, len(demoPlayerNames))
	for _, name := range demoPlayerNames {
		p, err := players.CreateAnonymousPlayer(ctx, name)
		if err != nil {
			if errors.Is(err, auth.ErrDisplayNameTaken) {
				continue
			}

			return nil, fmt.Errorf("create anonymous player %q: %w", name, err)
		}
		ids = append(ids, p.ID)
	}

	return ids, nil
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

// ensureDemoQuiz restores one quiz (from zr) attributed to the demo Host
// through the same HTTP-free import path the admin upload uses. A slug collision
// (the quiz already exists) is the idempotent no-op and returns (nil, nil). A
// newly created quiz is returned with its questions populated (IDs set).
func ensureDemoQuiz(
	ctx context.Context, cfg *config.Config,
	quizzes quiz.Store, mediaSvc *media.Service, hostID int64, logger *slog.Logger,
	zr *zip.Reader,
) (*quiz.Quiz, error) {
	limits := admin.NewArchiveImportLimits(
		cfg.MediaImageMaxBytes, cfg.MediaAudioMaxBytes, cfg.MediaImportMaxBytes,
	)
	qz, err := admin.ImportQuizArchive(ctx, logger, quizzes, mediaSvc, zr, hostID, limits)
	if err != nil {
		if errors.Is(err, quiz.ErrSlugTaken) {
			return nil, nil //nolint:nilnil // nil quiz + nil error signals "already present", the idempotent no-op.
		}

		return nil, fmt.Errorf("import quiz archive: %w", err)
	}

	// The import lands as a draft; publish so the demo quiz is playable (#1192).
	if err := quizzes.SetQuizPublished(ctx, qz.ID, true); err != nil {
		return nil, fmt.Errorf("publish demo quiz: %w", err)
	}
	qz.Published = true

	return qz, nil
}

// seedDemoPlays records a finished game against qz for each player id in the
// pre-built demo player pool, so the quiz appears in the home Popular list.
// Play-seeding is only called for newly-created quizzes, so idempotent boots
// that find every quiz already present leave existing play counts untouched. A
// per-play failure is logged and skipped: a transient error against one player
// should not abort the whole demo seed.
func seedDemoPlays(ctx context.Context, games game.Store, qz *quiz.Quiz, poolIDs []int64, logger *slog.Logger) {
	for _, playerID := range poolIDs {
		if err := finishDemoGame(ctx, games, playerID, qz); err != nil {
			logger.Warn("finish demo game",
				slog.Int64("player_id", playerID),
				slog.String("quiz", qz.Title),
				slog.Any("err", err),
			)
		}
	}
}

// finishDemoGame creates a game + participant + one game_question per quiz
// question so the row counts as finished by the popular-quiz SQL (#891).
// Answers are not written -- the home Popular list ranks by play_count,
// not answers submitted.
func finishDemoGame(ctx context.Context, games game.Store, playerID int64, qz *quiz.Quiz) error {
	g := &game.Game{QuizID: qz.ID}
	if err := games.CreateGame(ctx, g); err != nil {
		return fmt.Errorf("create game: %w", err)
	}
	if err := games.CreateParticipant(ctx, &game.Participant{
		GameID: g.ID, PlayerID: playerID, QuizID: qz.ID,
	}); err != nil {
		return fmt.Errorf("create participant: %w", err)
	}
	now := time.Now()
	for i, qs := range qz.Questions {
		gq := &game.Question{
			GameID:     g.ID,
			QuestionID: qs.ID,
			StartedAt:  now,
			ExpiredAt:  now.Add(demoAnswerWindowSeconds * time.Second),
		}
		completesGame := i == len(qz.Questions)-1
		if err := games.CreateQuestion(ctx, gq, completesGame); err != nil {
			return fmt.Errorf("create question: %w", err)
		}
	}

	return nil
}
