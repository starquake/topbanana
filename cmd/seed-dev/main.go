// seed-dev populates the local dev database with quizzes plus a handful
// of finished games, purely for hand-eyeballing the player/admin UI on a
// populated DB. The -seed flag chooses the seed set: "test" (the default)
// loads the small fixture quizzes from dev/fixtures/quizzes.json, while
// "demo" restores one large showcase quiz with real public-domain media
// from the committed archive at dev/fixtures/demo-quiz.zip (the inverse of
// the #1113 quiz-archive export). Neither the fixtures nor the archive are
// embedded into the production binary. Idempotent on quizzes (a duplicate
// slug is treated as already-present and skipped, surfaced via
// [quiz.ErrSlugTaken]) so re-running the seeder against an already-populated
// DB is a no-op.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	mrand "math/rand/v2"
	"os"
	"time"

	"github.com/gosimple/slug"
	_ "modernc.org/sqlite"

	"github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/config"
	"github.com/starquake/topbanana/internal/database"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/store"
)

// seedSetTest loads the small fixture quizzes from the JSON fixture file (the
// default seed set); seedSetDemo restores one large showcase quiz with real
// media from the committed archive. They are the only values -seed accepts.
const (
	seedSetTest = "test"
	seedSetDemo = "demo"
	// defaultDemoArchivePath is the committed demo quiz archive restored by the
	// demo seed set, an export from the #1113 quiz-archive feature.
	defaultDemoArchivePath = "dev/fixtures/demo-quiz.zip"
)

// seededAdminID matches the ID set by migration
// 20260111110308_add_admin_player.sql. The seeder attributes every
// quiz to this account so the NOT NULL created_by_player_id (#281)
// is satisfied without needing to register a fresh admin first.
const seededAdminID int64 = 1

// sampleAudio is a short MP3 tone bundled into the seeder so a fixture
// question with "audio": true gets a real, browser-playable clip stored
// through the media service - a proper ready media row plus a file on disk
// served at /media/{id}. It is dev-seed-only and never ships in the
// production binary.
//
//go:embed testdata/sample-tone.mp3
var sampleAudio []byte

// sampleAudioDurationMs is the bundled clip's playback length, passed to
// StoreAudio as the advisory duration: audio is not decoded server-side, so
// the caller supplies it. It matches the generated tone.
const sampleAudioDurationMs = 1071

// sampleAudioFilename is the filename handed to StoreAudio. It drives only the
// default description label; the stored MIME and extension come from sniffing
// the bytes, not this name.
const sampleAudioFilename = "sample-tone.mp3"

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
	// mediaDirPerm is the permission for the media root the seeder creates
	// when it does not already exist.
	mediaDirPerm os.FileMode = 0o755
)

// PCG seed words for [seedPlays]. Arbitrary values picked so the
// deterministic seed has named symbols the magic-number linter can
// accept. No security relevance - the shuffle is observable.
const (
	rngSeed1 uint64 = 0xfeed
	rngSeed2 uint64 = 0xc0ffee
)

// quizFixture mirrors the admin import payload shape so the same
// JSON can flow through either the live admin endpoint or this
// tool. Decoupling from quiz.Quiz keeps the wire shape small and
// LLM-friendly (no IDs, no timestamps).
//
// Questions and Rounds are mutually exclusive, matching the admin
// import payload (#546): a flat quiz supplies Questions (every
// question lands in the default round), a multi-round quiz supplies
// Rounds with their own questions.
type quizFixture struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Questions   []questionFixture `json:"questions,omitempty"`
	Rounds      []roundFixture    `json:"rounds,omitempty"`
}

type roundFixture struct {
	Title     string            `json:"title"`
	Summary   string            `json:"summary,omitempty"`
	Questions []questionFixture `json:"questions"`
}

type questionFixture struct {
	Text    string          `json:"text"`
	Options []optionFixture `json:"options"`
	// Audio, when true, opts this question into the bundled sample clip
	// (#1059); seedAudio attaches it after the quiz is created.
	Audio bool `json:"audio,omitempty"`
	// AudioRepeat maps to the question's AudioRepeat (#1073): the play
	// surfaces replay the clip up to 3 times. Meaningful only with Audio.
	AudioRepeat bool `json:"audioRepeat,omitempty"`
}

type optionFixture struct {
	Text    string `json:"text"`
	Correct bool   `json:"correct"`
}

// seedConfig carries the resolved CLI options into run, so a new seed set adds a
// field rather than another positional argument.
type seedConfig struct {
	seedSet         string
	fixturePath     string
	demoArchivePath string
	dbURI           string
	mediaDir        string
	playerCount     int
	playsPerPlayer  int
}

// seedSource is the loaded, validated seed data, ready to persist once the DB
// is open. Exactly one field is set, matching the chosen seed set.
type seedSource struct {
	fixtures    []quizFixture // test set
	demoArchive *zip.Reader   // demo set
}

func main() {
	seedSet := flag.String("seed", seedSetTest,
		`which seed set to load: "test" (small fixture quizzes) or "demo" (one large showcase quiz)`)
	fixturePath := flag.String("fixtures", "dev/fixtures/quizzes.json", "path to the JSON fixture file (test seed)")
	demoArchive := flag.String("demo-archive", defaultDemoArchivePath, "path to the demo quiz archive zip (demo seed)")
	dbURI := flag.String("db", "", "DB URI (defaults to $DB_URI or the dev default)")
	mediaDir := flag.String("media-dir", config.MediaDirDefault, "filesystem directory for stored media (audio clips)")
	playersFlag := flag.Int("players", defaultPlayerCount, "number of anonymous players to seed")
	playsFlag := flag.Int("plays", defaultPlaysPerPlayer, "number of quizzes each seeded player finishes")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if *seedSet != seedSetTest && *seedSet != seedSetDemo {
		logger.Error(`seed-dev: unknown -seed value (want "test" or "demo")`, slog.String("seed", *seedSet))
		os.Exit(1)
	}

	// ParseDatabase keeps the URI and its pragmas in one source of truth; it
	// bypasses the production server gates, so it is safe here.
	uri := *dbURI
	if uri == "" {
		dbc, err := config.ParseDatabase(os.Getenv)
		if err != nil {
			logger.Error("seed-dev failed to resolve DB URI", slog.Any("err", err))
			os.Exit(1)
		}
		uri = dbc.URI
	}

	cfg := seedConfig{
		seedSet:         *seedSet,
		fixturePath:     *fixturePath,
		demoArchivePath: *demoArchive,
		dbURI:           uri,
		mediaDir:        *mediaDir,
		playerCount:     *playersFlag,
		playsPerPlayer:  *playsFlag,
	}
	if err := run(logger, cfg); err != nil {
		logger.Error("seed-dev failed", slog.Any("err", err))
		os.Exit(1)
	}
}

// run is the non-fatal entry point: it returns errors so main() keeps its
// [os.Exit] call at the surface.
func run(logger *slog.Logger, cfg seedConfig) error {
	ctx := context.Background()

	// Load and validate the seed source before any DB side effects, so a bad
	// -fixtures / -demo-archive path fails fast without migrating the DB.
	src, err := loadSeedSource(cfg)
	if err != nil {
		return err
	}

	database.SetupGoose()
	conn, err := sql.Open("sqlite", cfg.dbURI)
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

	// The media service writes audio + image files under mediaDir, so ensure the
	// directory exists before storing (the production server creates it at
	// startup; the seeder owns that here).
	if mkErr := os.MkdirAll(cfg.mediaDir, mediaDirPerm); mkErr != nil {
		return fmt.Errorf("create media dir: %w", mkErr)
	}
	mediaSvc := media.NewService(
		stores.Media, cfg.mediaDir,
		config.MediaImageMaxBytesDefault, config.MediaAudioMaxBytesDefault, logger,
	)

	createdQuizzes, err := seedQuizSet(ctx, logger, stores, mediaSvc, src)
	if err != nil {
		return err
	}
	logger.Info("quizzes seeded", slog.Int("count", len(createdQuizzes)))

	if cfg.playerCount > 0 && cfg.playsPerPlayer > 0 && len(createdQuizzes) > 0 {
		plays, err := seedPlays(ctx, logger, stores, createdQuizzes, cfg.playerCount, cfg.playsPerPlayer)
		if err != nil {
			return fmt.Errorf("seed plays: %w", err)
		}
		logger.Info("plays seeded", slog.Int("count", plays))
	}

	return nil
}

// loadSeedSource reads and validates the configured seed set's source before any
// DB side effects, so a bad -fixtures / -demo-archive path fails fast without
// migrating the DB. The demo set opens the committed archive; the test set
// decodes the JSON fixtures.
func loadSeedSource(cfg seedConfig) (seedSource, error) {
	if cfg.seedSet == seedSetDemo {
		zr, err := openDemoArchive(cfg.demoArchivePath)
		if err != nil {
			return seedSource{}, err
		}

		return seedSource{demoArchive: zr}, nil
	}

	fixtures, err := loadFixtures(cfg.fixturePath)
	if err != nil {
		return seedSource{}, fmt.Errorf("load fixtures: %w", err)
	}

	return seedSource{fixtures: fixtures}, nil
}

// openDemoArchive reads the committed demo quiz archive into memory and returns a
// zip reader over it. The whole file is read up front because [zip.NewReader]
// needs random access to the bytes, so the backing buffer must outlive this call.
func openDemoArchive(path string) (*zip.Reader, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // dev tool reads a path the operator passed in
	if err != nil {
		return nil, fmt.Errorf("read demo archive: %w", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("open demo archive: %w", err)
	}

	return zr, nil
}

// seedQuizSet persists the loaded seed source: the demo set restores the single
// archived quiz, the test set creates the fixture quizzes. Both are idempotent on
// a slug collision, so a re-run returns the quizzes it could create (none, when
// they already exist) without erroring.
func seedQuizSet(
	ctx context.Context, logger *slog.Logger,
	stores *store.Stores, mediaSvc *media.Service, src seedSource,
) ([]*quiz.Quiz, error) {
	if src.demoArchive != nil {
		qz, err := seedDemoQuiz(ctx, logger, stores, mediaSvc, src.demoArchive)
		if err != nil {
			return nil, err
		}
		if qz == nil {
			return nil, nil
		}

		return []*quiz.Quiz{qz}, nil
	}

	created, err := seedQuizzes(ctx, logger, stores.Quizzes, mediaSvc, src.fixtures)
	if err != nil {
		return nil, fmt.Errorf("seed quizzes: %w", err)
	}

	return created, nil
}

// seedDemoQuiz restores the demo quiz archive through the same HTTP-free import
// path the admin upload uses, with the demo media files written under the media
// service's directory. It is idempotent: a slug collision (the demo quiz already
// exists) logs an info line and returns a nil quiz and nil error, the same no-op
// the fixture seeder gives on a re-run.
func seedDemoQuiz(
	ctx context.Context, logger *slog.Logger,
	stores *store.Stores, mediaSvc *media.Service, archive *zip.Reader,
) (*quiz.Quiz, error) {
	limits := admin.NewArchiveImportLimits(
		config.MediaImageMaxBytesDefault, config.MediaAudioMaxBytesDefault, config.MediaImportMaxBytesDefault,
	)
	qz, err := admin.ImportQuizArchive(ctx, logger, stores.Quizzes, mediaSvc, archive, seededAdminID, limits)
	if err != nil {
		if errors.Is(err, quiz.ErrSlugTaken) {
			logger.Info("demo quiz already exists (skipping)")

			return nil, nil //nolint:nilnil // nil quiz + nil error means "already present", the idempotent no-op.
		}

		return nil, fmt.Errorf("import demo quiz archive: %w", err)
	}

	// The archive import creates the quiz as a draft (published defaults to 0),
	// which would hide it from the public listing and 404 on play; the seeded
	// demo quiz is ready to play, so publish it (#1192).
	if err := stores.Quizzes.SetQuizPublished(ctx, qz.ID, true); err != nil {
		return nil, fmt.Errorf("publish demo quiz: %w", err)
	}
	qz.Published = true

	return qz, nil
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

// audioStorer is the narrow part of the media service the seeder needs: store a
// clip and get back its media row (with the assigned id). Defined here, at the
// consumer, so the audio-seeding logic can be tested with a real *media.Service
// or a double.
type audioStorer interface {
	StoreAudio(
		ctx context.Context, quizID, createdBy int64, durationMs int, description, filename string, r io.Reader,
	) (*media.Media, error)
}

// seedQuizzes calls CreateQuiz for each fixture, then attaches the bundled
// sample clip to any question that opted in via "audio": true. Slug collisions
// (re-running the seeder) are treated as "already present" and logged at info
// level rather than failing the whole run - the operator usually wants
// idempotent behaviour for a dev-time seed.
func seedQuizzes(
	ctx context.Context, logger *slog.Logger,
	quizzes quiz.Store, audio audioStorer, fixtures []quizFixture,
) ([]*quiz.Quiz, error) {
	out := make([]*quiz.Quiz, 0, len(fixtures))
	for i := range fixtures {
		qz, err := quizFromFixture(&fixtures[i])
		if err != nil {
			return out, fmt.Errorf("build quiz %q: %w", fixtures[i].Title, err)
		}
		if err := quizzes.CreateQuiz(ctx, qz); err != nil {
			if errors.Is(err, quiz.ErrSlugTaken) {
				logger.Info("quiz already exists (skipping)", slog.String("title", qz.Title))

				continue
			}

			return out, fmt.Errorf("create quiz %q: %w", qz.Title, err)
		}
		if err := seedAudio(ctx, quizzes, audio, qz, &fixtures[i]); err != nil {
			return out, fmt.Errorf("seed audio for quiz %q: %w", qz.Title, err)
		}
		out = append(out, qz)
	}

	return out, nil
}

// seedAudio attaches the bundled sample clip to every question in qz whose
// fixture set "audio": true. It runs after CreateQuiz so the quiz and its
// questions have ids: for each opted-in question it stores the clip through the
// media service (yielding a ready audio media row plus a file on disk), then
// points the question's AudioMediaID at the new row via UpdateQuestion. The
// flattened fixture questions line up 1:1 with qz.Questions in document order.
func seedAudio(
	ctx context.Context, quizzes quiz.Store, audio audioStorer,
	qz *quiz.Quiz, f *quizFixture,
) error {
	flat := flattenFixtureQuestions(f)
	for i, qq := range qz.Questions {
		if i >= len(flat) || !flat[i].Audio {
			continue
		}
		m, err := audio.StoreAudio(
			ctx, qz.ID, seededAdminID, sampleAudioDurationMs,
			"", sampleAudioFilename, bytes.NewReader(sampleAudio),
		)
		if err != nil {
			return fmt.Errorf("store audio for question %d: %w", qq.Position, err)
		}
		qq.AudioMediaID = &m.ID
		if err := quizzes.UpdateQuestion(ctx, qq); err != nil {
			return fmt.Errorf("update question %d with audio: %w", qq.Position, err)
		}
	}

	return nil
}

// errFixtureQuestionsOrRounds is returned when a fixture supplies both a
// top-level questions array and a rounds array, or neither. The two are
// mutually exclusive, mirroring the admin import payload (#546). The
// fixtures are in-repo, so a malformed one is a programming error worth
// failing the run over rather than tolerating.
var (
	errFixtureQuestionsOrRounds = errors.New(
		"provide either a top-level questions array or a rounds array, not both and not neither",
	)
	// errFixtureRoundTitleRequired is returned when a rounds fixture
	// carries a round with no title, mirroring the admin import (#546).
	errFixtureRoundTitleRequired = errors.New("round title is required")
	// errFixtureRoundNoQuestions is returned when a rounds fixture
	// carries a round with no questions, mirroring the admin import (#546).
	errFixtureRoundNoQuestions = errors.New("round needs at least one question")
)

// quizFromFixture converts a fixture into a domain Quiz pinned to the
// seed admin. Question positions are 1..N in document order; the slug
// is derived from the title the same way the admin import handler
// does so an operator who imports the same JSON via /admin/quizzes/
// import gets the same row shape.
//
// A rounds fixture maps each round onto qz.Rounds (with a 0-based
// round Position) and mirrors every question onto qz.Questions with a
// quiz-wide 1..N position across all rounds - the same flattening the
// admin import does (#546). The mirror matters here because finishGame
// iterates qz.Questions to write game_questions; a rounds quiz with no
// flat mirror would seed plays with zero questions.
func quizFromFixture(f *quizFixture) (*quiz.Quiz, error) {
	if (len(f.Questions) == 0) == (len(f.Rounds) == 0) {
		return nil, errFixtureQuestionsOrRounds
	}

	qz := &quiz.Quiz{
		Title:             f.Title,
		Slug:              slug.Make(f.Title),
		Description:       f.Description,
		CreatedByPlayerID: seededAdminID,
		// Seed fixtures are ready to play, so publish them (#1192); a draft
		// would be hidden from the public listing and not solo-playable.
		Published: true,
	}

	if len(f.Rounds) > 0 {
		if err := fillQuizFromRounds(qz, f.Rounds); err != nil {
			return nil, err
		}

		return qz, nil
	}

	qz.Questions = make([]*quiz.Question, 0, len(f.Questions))
	for i, qf := range f.Questions {
		qz.Questions = append(qz.Questions, questionFromFixture(qf, i+1))
	}

	return qz, nil
}

// fillQuizFromRounds maps the authored rounds onto qz.Rounds and mirrors
// every question onto qz.Questions with a quiz-wide 1..N position in
// document order, so finishGame still finds the full question set when
// seeding plays (#546). A round must carry a non-empty title and at
// least one question, mirroring the admin import's per-round checks.
func fillQuizFromRounds(qz *quiz.Quiz, rounds []roundFixture) error {
	qz.Rounds = make([]*quiz.Round, 0, len(rounds))
	pos := 0
	for i, rf := range rounds {
		if rf.Title == "" {
			return fmt.Errorf("round %d: %w", i+1, errFixtureRoundTitleRequired)
		}
		if len(rf.Questions) == 0 {
			return fmt.Errorf("round %q: %w", rf.Title, errFixtureRoundNoQuestions)
		}

		round := &quiz.Round{
			Position:  i,
			Title:     rf.Title,
			Summary:   rf.Summary,
			Questions: make([]*quiz.Question, 0, len(rf.Questions)),
		}
		for _, qf := range rf.Questions {
			pos++
			qq := questionFromFixture(qf, pos)
			round.Questions = append(round.Questions, qq)
			qz.Questions = append(qz.Questions, qq)
		}
		qz.Rounds = append(qz.Rounds, round)
	}

	return nil
}

// questionFromFixture maps one fixture question onto the domain type at
// the given quiz-wide position. AudioRepeat is carried straight through; the
// AudioMediaID is filled in later by seedAudio, which can only run once the
// quiz (and so its id) exists.
func questionFromFixture(qf questionFixture, position int) *quiz.Question {
	qq := &quiz.Question{Text: qf.Text, Position: position, AudioRepeat: qf.AudioRepeat}
	qq.Options = make([]*quiz.Option, 0, len(qf.Options))
	for _, of := range qf.Options {
		qq.Options = append(qq.Options, &quiz.Option{Text: of.Text, Correct: of.Correct})
	}

	return qq
}

// flattenFixtureQuestions returns a fixture's questions in quiz-wide document
// order - the same order quizFromFixture assigns positions 1..N and appends to
// qz.Questions - so seedAudio can zip the created questions back against the
// per-question Audio flags. A flat fixture yields its Questions directly; a
// rounds fixture yields every round's questions in round then question order.
func flattenFixtureQuestions(f *quizFixture) []questionFixture {
	if len(f.Rounds) == 0 {
		return f.Questions
	}

	total := 0
	for _, rf := range f.Rounds {
		total += len(rf.Questions)
	}
	out := make([]questionFixture, 0, total)
	for _, rf := range f.Rounds {
		out = append(out, rf.Questions...)
	}

	return out
}

// seedPlayerNames are imaginative display names for the anonymous players the
// seeder creates, so the dev home page's active-players list reads like real
// people rather than "seed-player-..." rows.
//
//nolint:gochecknoglobals // dictionary table; values never mutate.
var seedPlayerNames = []string{
	"Quizzy McQuizface", "Treble Maker", "Major Minor", "Beethoven's Ghost",
	"Captain Cortex", "The Know-It-Owl", "Trivia Newton-John", "Sir Guess-a-Lot",
	"Allegra Tempo", "Doc Decibel", "Maestro Mayhem", "The Quiz Wizard",
	"Polly Glot", "Echo Chamberlain", "Crescendo Kid", "Harmony Hooper",
	"Fermata Fred", "Brainstorm Betty", "The Lucky Guesser", "Riff Raffles",
	"Whisper Quartet", "Anonymous Andante", "The Fact Hunter", "Encore Eleanor",
}

// seedPlayerName returns a unique display name for the i-th seeded player: a
// name from the pool, with a lap suffix once i runs past the pool so any
// -players value still yields distinct names within a run.
func seedPlayerName(i int) string {
	name := seedPlayerNames[i%len(seedPlayerNames)]
	if lap := i / len(seedPlayerNames); lap > 0 {
		return fmt.Sprintf("%s %d", name, lap+1)
	}

	return name
}

// seedPlays creates playerCount anonymous players and finishes
// playsPerPlayer random quizzes for each. Returns the total number of
// finished games written. A finished game in this schema is one with
// every quiz question registered as a game_questions row - the home
// page's popular list counts these as "plays" (#166).
//
// The deterministic-but-arbitrary PCG seed makes the per-run mix
// reproducible - re-running on the same fixture yields the same
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
		displayName := seedPlayerName(i)
		p, err := stores.Players.CreateAnonymousPlayer(ctx, displayName)
		if err != nil {
			// A name claimed by a prior seed run against the same DB
			// (display_name is UNIQUE) is not fatal for a dev seed: skip
			// that player and keep the non-colliding ones.
			if errors.Is(err, auth.ErrDisplayNameTaken) {
				logger.Info("seed player already exists (skipping)", slog.String("name", displayName))

				continue
			}

			return 0, fmt.Errorf("create anonymous player: %w", err)
		}
		players = append(players, p)
	}

	total := 0
	for _, p := range players {
		// Sample without replacement so a single player doesn't get
		// counted twice on the same quiz - the (player, quiz) unique
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
					slog.String("player", p.DisplayName),
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
// SQL and the popular-quiz SQL. Answers aren't written - the home
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
	for i, qs := range q.Questions {
		gq := &game.Question{
			GameID:     g.ID,
			QuestionID: qs.ID,
			StartedAt:  now,
			ExpiredAt:  now.Add(answerWindowSeconds * time.Second),
		}
		completesGame := i == len(q.Questions)-1
		if err := games.CreateQuestion(ctx, gq, completesGame); err != nil {
			return fmt.Errorf("create question: %w", err)
		}
	}

	return nil
}
