// seed-dev populates the local dev database with a fixture-driven set
// of quizzes plus a handful of finished games. Intended purely for
// hand-eyeballing the player/admin UI on a populated DB — the fixture
// file lives in dev/fixtures and is not embedded into the production
// binary. Idempotent on quizzes (a duplicate slug is treated as
// already-present and skipped, surfaced via [quiz.ErrSlugTaken]) so
// re-running the seeder against an already-populated DB is a no-op.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	mrand "math/rand/v2"
	"os"
	"time"

	"github.com/gosimple/slug"
	_ "modernc.org/sqlite"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/database"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// defaultDBURI mirrors config.DBURIDefault. Duplicated here so the
// seeder can run without depending on the full config package's
// production-vs-dev gates.
const defaultDBURI = "file:topbanana.sqlite?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"

// seededAdminID matches the ID set by migration
// 20260111110308_add_admin_player.sql. The seeder attributes every
// quiz to this account so the NOT NULL created_by_player_id (#281)
// is satisfied without needing to register a fresh admin first.
const seededAdminID int64 = 1

// Defaults for the player + play counts surfaced as CLI flags. Pulled
// out so the magic-number linter has named symbols to point at and an
// operator scanning the file sees the dev-time scale up front.
const (
	defaultPlayerCount    = 5
	defaultPlaysPerPlayer = 3
	// answerWindowSeconds is the per-question answer window stamped
	// onto the synthesised game_questions rows. The seeder doesn't
	// run a real game clock, so the value just needs to be long
	// enough that the row reads as a normally-finished question to
	// any later reader.
	answerWindowSeconds = 10
)

// PCG seed words for [seedPlays]. Arbitrary values picked so the
// deterministic seed has named symbols the magic-number linter can
// accept. No security relevance — the shuffle is observable.
const (
	rngSeed1 uint64 = 0xfeed
	rngSeed2 uint64 = 0xc0ffee
)

// quizFixture mirrors the admin import payload shape so the same
// JSON can flow through either the live admin endpoint or this
// tool. Decoupling from quiz.Quiz keeps the wire shape small and
// LLM-friendly (no IDs, no timestamps).
type quizFixture struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Questions   []questionFixture `json:"questions"`
}

type questionFixture struct {
	Text    string          `json:"text"`
	Options []optionFixture `json:"options"`
}

type optionFixture struct {
	Text    string `json:"text"`
	Correct bool   `json:"correct"`
}

func main() {
	fixturePath := flag.String("fixtures", "dev/fixtures/quizzes.json", "path to the JSON fixture file")
	dbURI := flag.String("db", "", "DB URI (defaults to $DB_URI or the dev default)")
	playersFlag := flag.Int("players", defaultPlayerCount, "number of anonymous players to seed")
	playsFlag := flag.Int("plays", defaultPlaysPerPlayer, "number of quizzes each seeded player finishes")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	uri := *dbURI
	if uri == "" {
		uri = os.Getenv("DB_URI")
	}
	if uri == "" {
		uri = defaultDBURI
	}

	if err := run(logger, *fixturePath, uri, *playersFlag, *playsFlag); err != nil {
		logger.Error("seed-dev failed", slog.Any("err", err))
		os.Exit(1)
	}
}

// run is the testable entry point: returns errors so main() can keep
// its [os.Exit] call at the surface and unit tests (should we ever
// add them) get a non-fatal path.
func run(logger *slog.Logger, fixturePath, dbURI string, playerCount, playsPerPlayer int) error {
	ctx := context.Background()

	fixtures, err := loadFixtures(fixturePath)
	if err != nil {
		return fmt.Errorf("load fixtures: %w", err)
	}

	database.SetupGoose()
	conn, err := sql.Open("sqlite", dbURI)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			logger.Warn("db close", slog.Any("err", cerr))
		}
	}()
	if mErr := database.Migrate(conn); mErr != nil {
		return fmt.Errorf("migrate: %w", mErr)
	}
	stores := store.New(conn, logger)

	createdQuizzes, err := seedQuizzes(ctx, logger, stores.Quizzes, fixtures)
	if err != nil {
		return fmt.Errorf("seed quizzes: %w", err)
	}
	logger.Info("quizzes seeded", slog.Int("count", len(createdQuizzes)))

	if playerCount > 0 && playsPerPlayer > 0 && len(createdQuizzes) > 0 {
		plays, err := seedPlays(ctx, logger, stores, createdQuizzes, playerCount, playsPerPlayer)
		if err != nil {
			return fmt.Errorf("seed plays: %w", err)
		}
		logger.Info("plays seeded", slog.Int("count", plays))
	}

	return nil
}

// loadFixtures reads + decodes the JSON fixture file. DisallowUnknownFields
// mirrors the live admin import handler so a stray field surfaces as a
// fail-fast error rather than silently being ignored.
func loadFixtures(path string) ([]quizFixture, error) {
	f, err := os.Open(path) //nolint:gosec // dev tool reads a path the operator passed in
	if err != nil {
		return nil, fmt.Errorf("open fixture: %w", err)
	}
	defer f.Close() //nolint:errcheck // dev tool; close-on-read errors are not actionable

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var out []quizFixture
	if dErr := dec.Decode(&out); dErr != nil {
		return nil, fmt.Errorf("decode fixture: %w", dErr)
	}

	return out, nil
}

// seedQuizzes calls CreateQuiz for each fixture. Slug collisions
// (re-running the seeder) are treated as "already present" and logged
// at info level rather than failing the whole run — the operator
// usually wants idempotent behaviour for a dev-time seed.
func seedQuizzes(
	ctx context.Context, logger *slog.Logger,
	quizzes quiz.Store, fixtures []quizFixture,
) ([]*quiz.Quiz, error) {
	out := make([]*quiz.Quiz, 0, len(fixtures))
	for i := range fixtures {
		qz := quizFromFixture(&fixtures[i])
		if err := quizzes.CreateQuiz(ctx, qz); err != nil {
			if errors.Is(err, quiz.ErrSlugTaken) {
				logger.Info("quiz already exists (skipping)", slog.String("title", qz.Title))

				continue
			}

			return out, fmt.Errorf("create quiz %q: %w", qz.Title, err)
		}
		out = append(out, qz)
	}

	return out, nil
}

// quizFromFixture converts a fixture into a domain Quiz pinned to the
// seed admin. Question positions are 1..N in document order; the slug
// is derived from the title the same way the admin import handler
// does so an operator who imports the same JSON via /admin/quizzes/
// import gets the same row shape.
func quizFromFixture(f *quizFixture) *quiz.Quiz {
	qz := &quiz.Quiz{
		Title:             f.Title,
		Slug:              slug.Make(f.Title),
		Description:       f.Description,
		CreatedByPlayerID: seededAdminID,
	}
	qz.Questions = make([]*quiz.Question, 0, len(f.Questions))
	for i, qf := range f.Questions {
		qq := &quiz.Question{Text: qf.Text, Position: i + 1}
		qq.Options = make([]*quiz.Option, 0, len(qf.Options))
		for _, of := range qf.Options {
			qq.Options = append(qq.Options, &quiz.Option{Text: of.Text, Correct: of.Correct})
		}
		qz.Questions = append(qz.Questions, qq)
	}

	return qz
}

// seedPlays creates playerCount anonymous players and finishes
// playsPerPlayer random quizzes for each. Returns the total number of
// finished games written. A finished game in this schema is one with
// every quiz question registered as a game_questions row — the home
// page's popular list counts these as "plays" (#166).
//
// The deterministic-but-arbitrary PCG seed makes the per-run mix
// reproducible — re-running on the same fixture yields the same
// (player, quiz) pairings, so the popular list visibly differs by
// fixture set, not by run-to-run noise.
func seedPlays(
	ctx context.Context, logger *slog.Logger,
	stores *store.Stores, quizzes []*quiz.Quiz,
	playerCount, playsPerPlayer int,
) (int, error) {
	// Deterministic seed (not a security boundary): the values are
	// arbitrary, the goal is reproducible (player, quiz) pairings
	// across runs against the same fixture set.
	rng := mrand.New(mrand.NewPCG(rngSeed1, rngSeed2)) //nolint:gosec // dev tool; deterministic shuffle by design

	players := make([]*auth.Player, 0, playerCount)
	for i := range playerCount {
		username := fmt.Sprintf("seed-player-%d-%d", time.Now().UnixNano(), i)
		p, err := stores.Players.CreateAnonymousPlayer(ctx, username)
		if err != nil {
			return 0, fmt.Errorf("create anonymous player: %w", err)
		}
		players = append(players, p)
	}

	total := 0
	for _, p := range players {
		// Sample without replacement so a single player doesn't get
		// counted twice on the same quiz — the (player, quiz) unique
		// index would reject the second insert anyway, but failing
		// loudly on a foreseeable collision is worse than just picking
		// distinct quizzes up front.
		picks := rng.Perm(len(quizzes))
		n := min(playsPerPlayer, len(picks))
		for i := range n {
			qz := quizzes[picks[i]]
			if err := finishGame(ctx, stores.Games, p.ID, qz); err != nil {
				logger.Warn(
					"finish game",
					slog.String("player", p.Username),
					slog.String("quiz", qz.Title),
					slog.Any("err", err),
				)

				continue
			}
			total++
		}
	}

	return total, nil
}

// finishGame creates a game + participant + one game_question per
// quiz question so the row counts as finished by both the leaderboard
// SQL and the popular-quiz SQL. Answers aren't written — the home
// page and the per-quiz leaderboard both gate on questions-issued,
// not answers-submitted.
func finishGame(ctx context.Context, games game.Store, playerID int64, q *quiz.Quiz) error {
	g := &game.Game{QuizID: q.ID}
	if err := games.CreateGame(ctx, g); err != nil {
		return fmt.Errorf("create game: %w", err)
	}
	if err := games.CreateParticipant(ctx, &game.Participant{
		GameID: g.ID, PlayerID: playerID, QuizID: q.ID,
	}); err != nil {
		return fmt.Errorf("create participant: %w", err)
	}
	now := time.Now()
	for _, qs := range q.Questions {
		gq := &game.Question{
			GameID:     g.ID,
			QuestionID: qs.ID,
			StartedAt:  now,
			ExpiredAt:  now.Add(answerWindowSeconds * time.Second),
		}
		if err := games.CreateQuestion(ctx, gq); err != nil {
			return fmt.Errorf("create question: %w", err)
		}
	}

	return nil
}
