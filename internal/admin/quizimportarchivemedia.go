package admin

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"

	"github.com/gosimple/slug"

	"github.com/starquake/topbanana/internal/quiz"
)

// readArchivePart reads the multipart "archive" file part into memory under the
// already-capped request body (MaxMultipartFormMiddlewareWithLimit bounds it via
// [http.MaxBytesReader]). A missing part or read error renders a 400 / 500. The
// part is read fully because archive/zip needs a ReaderAt over the whole file.
func readArchivePart(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger,
	renderErr func(http.ResponseWriter, *http.Request, int, string),
) ([]byte, bool) {
	if r.MultipartForm == nil {
		renderErr(w, r, http.StatusBadRequest, "no archive uploaded")

		return nil, false
	}
	files := r.MultipartForm.File[importArchiveFormField]
	if len(files) == 0 {
		renderErr(w, r, http.StatusBadRequest, "choose a .zip archive to import")

		return nil, false
	}

	f, err := files[0].Open()
	if err != nil {
		logger.ErrorContext(r.Context(), "error opening archive upload part", slog.Any("err", err))
		renderErr(w, r, http.StatusInternalServerError, "could not read the uploaded archive")

		return nil, false
	}
	defer func() { _ = f.Close() }()

	raw, err := io.ReadAll(f)
	if err != nil {
		// A read past the MaxBytesReader cap surfaces here; treat it as a client
		// error (the archive is too large) rather than a server fault.
		renderErr(w, r, http.StatusRequestEntityTooLarge, "the uploaded archive is too large")

		return nil, false
	}

	return raw, true
}

// importArchiveFormField is the multipart field the uploaded archive arrives
// under.
const importArchiveFormField = "archive"

// checkArchiveLimits enforces the zip-bomb guards on the opened archive BEFORE
// any entry is read: the entry-count cap, the per-entry uncompressed cap (by the
// declared UncompressedSize64, cross-checked against actual bytes at read time
// by the media pipeline's own caps), and the total uncompressed budget across
// all entries. The declared sizes are attacker-controlled, so the budget is
// compared without summing into a uint64 that a crafted pair of near-max entries
// could overflow past the cap. Returns a sentinel the caller maps to a 400.
func checkArchiveLimits(zr *zip.Reader, limits ArchiveImportLimits) error {
	if len(zr.File) > maxArchiveEntries {
		return ErrArchiveTooManyEntries
	}

	var remaining uint64
	if limits.totalMaxBytes > 0 {
		remaining = uint64(limits.totalMaxBytes)
	}
	for _, f := range zr.File {
		entryCap := perEntryCap(f.Name, limits)
		if entryCap > 0 && f.UncompressedSize64 > uint64(entryCap) {
			return fmt.Errorf("%w: %q", ErrArchiveEntryTooLarge, f.Name)
		}
		// Subtract from the remaining budget rather than summing into a running
		// total, so a declared size near uint64's max cannot wrap the accumulator
		// under the cap when the per-entry guard is disabled.
		if limits.totalMaxBytes > 0 {
			if f.UncompressedSize64 > remaining {
				return ErrArchiveTooLarge
			}
			remaining -= f.UncompressedSize64
		}
	}

	return nil
}

// perEntryCap returns the uncompressed size cap for an archive entry by its
// name: the audio cap for an audio extension, the image cap otherwise (the
// manifest itself is small and falls under the image cap comfortably). A cap of
// zero or less disables the per-entry check.
func perEntryCap(name string, limits ArchiveImportLimits) int64 {
	switch path.Ext(name) {
	case ".mp3", ".m4a", ".ogg", ".wav":
		return limits.audioMaxBytes
	default:
		return limits.imageMaxBytes
	}
}

// decodeArchiveManifest reads quiz.json from the archive and decodes it into a
// quizArchiveManifest. It does NOT use DisallowUnknownFields so a newer minor
// archive (extra fields) still imports (forward-compat). It rejects a
// formatVersion newer than this build understands; an equal or older version is
// accepted.
func decodeArchiveManifest(zr *zip.Reader, limits ArchiveImportLimits) (quizArchiveManifest, error) {
	f, err := zr.Open(manifestFileName)
	if err != nil {
		return quizArchiveManifest{}, ErrArchiveMissingManifest
	}
	defer func() { _ = f.Close() }()

	// Bound the manifest read with the image cap (the manifest is small JSON; a
	// crafted huge manifest is still bounded by the per-entry + total guards).
	reader := io.Reader(f)
	if limits.imageMaxBytes > 0 {
		reader = io.LimitReader(f, limits.imageMaxBytes+1)
	}

	var manifest quizArchiveManifest
	dec := json.NewDecoder(reader)
	if err = dec.Decode(&manifest); err != nil {
		return quizArchiveManifest{}, fmt.Errorf("decoding %s: %w", manifestFileName, err)
	}

	if manifest.FormatVersion > archiveFormatVersion {
		return quizArchiveManifest{}, fmt.Errorf(
			"%w (archive is v%d, this build supports up to v%d)",
			ErrArchiveUnsupportedVersion, manifest.FormatVersion, archiveFormatVersion,
		)
	}

	return manifest, nil
}

// archiveLimitMessage maps a zip-bomb guard sentinel to a host-facing message.
func archiveLimitMessage(err error) string {
	switch {
	case errors.Is(err, ErrArchiveTooManyEntries):
		return "the archive contains too many files"
	case errors.Is(err, ErrArchiveEntryTooLarge):
		return "the archive contains a file that is too large"
	case errors.Is(err, ErrArchiveTooLarge):
		return "the archive is too large once unpacked"
	default:
		return "the archive could not be read"
	}
}

// archiveManifestMessage maps a manifest decode failure to a host-facing
// message.
func archiveManifestMessage(err error) string {
	switch {
	case errors.Is(err, ErrArchiveMissingManifest):
		return "the archive is missing its quiz.json manifest"
	case errors.Is(err, ErrArchiveUnsupportedVersion):
		return err.Error()
	default:
		return fmt.Sprintf("the archive's quiz.json is invalid: %v", err)
	}
}

// builtArchiveQuiz pairs the domain quiz built from a manifest with its media
// plan: one entry per built question that referenced media, so the importer can
// re-store each archive file and wire the new media id onto the right question
// after the quiz exists.
type builtArchiveQuiz struct {
	quiz *quiz.Quiz
	plan []questionMediaPlan
}

// questionMediaPlan records the archive media a single built question
// references. The question pointer is the same one persisted by storeQuiz, so it
// carries the assigned id after persist. Image / Audio are nil when the question
// has no media of that kind.
type questionMediaPlan struct {
	question *quiz.Question
	image    *quizArchiveImageRef
	audio    *quizArchiveAudioRef
}

// quizFromArchiveManifest converts a decoded manifest into the domain quiz plus
// its media plan, mirroring quizFromImportPayload but media-aware. The slug is
// derived from the title server-side; positions are assigned 1..N across all
// rounds; visibility and mode are the resolved form/manifest values; the creator
// is the importing player. Questions[] and Rounds[] are mutually exclusive, same
// as the paste import.
func quizFromArchiveManifest(
	m quizArchiveManifest, creatorID int64, visibility, mode string,
) (builtArchiveQuiz, error) {
	if (len(m.Questions) == 0) == (len(m.Rounds) == 0) {
		return builtArchiveQuiz{}, errImportQuestionsOrRounds
	}

	timeLimit := quiz.DefaultTimeLimitSeconds
	if m.TimeLimitSeconds != nil {
		timeLimit = *m.TimeLimitSeconds
	}
	qz := &quiz.Quiz{
		Title:            m.Title,
		Slug:             slug.Make(m.Title),
		Description:      m.Description,
		TimeLimitSeconds: timeLimit,
		Visibility:       visibility,
		Mode:             mode,
		// Empty (a pre-#1115 archive) maps to LanguageEN in the store.
		Language:          m.Language,
		CreatedByPlayerID: creatorID,
	}

	if len(m.Rounds) > 0 {
		plan, err := fillQuizFromArchiveRounds(qz, m.Rounds)
		if err != nil {
			return builtArchiveQuiz{}, err
		}

		return builtArchiveQuiz{quiz: qz, plan: plan}, nil
	}

	plan := fillQuizFromArchiveQuestions(qz, m.Questions)

	return builtArchiveQuiz{quiz: qz, plan: plan}, nil
}

// fillQuizFromArchiveQuestions maps a flat manifest questions[] onto
// qz.Questions with positions 1..N and returns the media plan, the flat-import
// path (every question lands in the quiz's default round).
func fillQuizFromArchiveQuestions(qz *quiz.Quiz, questions []quizArchiveQuestion) []questionMediaPlan {
	var plan []questionMediaPlan
	qz.Questions = make([]*quiz.Question, 0, len(questions))
	for i, qIn := range questions {
		qs, entry := questionFromArchive(qIn, i+1)
		qz.Questions = append(qz.Questions, qs)
		if entry != nil {
			plan = append(plan, *entry)
		}
	}

	return plan
}

// fillQuizFromArchiveRounds maps authored manifest rounds onto qz.Rounds and
// mirrors every question onto qz.Questions with a quiz-wide 1..N position, the
// same shape fillQuizFromRounds builds for the paste import. It collects the
// media plan as it goes.
func fillQuizFromArchiveRounds(qz *quiz.Quiz, rounds []quizArchiveRound) ([]questionMediaPlan, error) {
	qz.Rounds = make([]*quiz.Round, 0, len(rounds))
	var plan []questionMediaPlan
	pos := 0
	for i, rIn := range rounds {
		if rIn.Title == "" {
			return nil, fmt.Errorf("round %d: %w", i+1, errImportRoundTitleRequired)
		}
		if len(rIn.Questions) == 0 {
			return nil, fmt.Errorf("round %q: %w", rIn.Title, errImportRoundNoQuestions)
		}

		round := &quiz.Round{
			Position:                i,
			Title:                   rIn.Title,
			Summary:                 rIn.Summary,
			BoundaryDurationSeconds: rIn.BoundaryDurationSeconds,
			Questions:               make([]*quiz.Question, 0, len(rIn.Questions)),
		}
		for _, qIn := range rIn.Questions {
			pos++
			qs, entry := questionFromArchive(qIn, pos)
			round.Questions = append(round.Questions, qs)
			qz.Questions = append(qz.Questions, qs)
			if entry != nil {
				plan = append(plan, *entry)
			}
		}
		qz.Rounds = append(qz.Rounds, round)
	}

	return plan, nil
}

// questionFromArchive maps one manifest question onto the domain type at the
// given quiz-wide position and returns its media-plan entry (nil when the
// question carries no media). The media ids are left nil here: they are not
// known until the archive files are re-stored after the quiz exists.
func questionFromArchive(qIn quizArchiveQuestion, position int) (*quiz.Question, *questionMediaPlan) {
	qs := &quiz.Question{
		Text:             qIn.Text,
		Position:         position,
		TimeLimitSeconds: qIn.TimeLimitSeconds,
	}
	qs.Options = make([]*quiz.Option, 0, len(qIn.Options))
	for _, oIn := range qIn.Options {
		qs.Options = append(qs.Options, &quiz.Option{Text: oIn.Text, Correct: oIn.Correct})
	}

	if qIn.Image == nil && qIn.Audio == nil {
		return qs, nil
	}

	return qs, &questionMediaPlan{question: qs, image: qIn.Image, audio: qIn.Audio}
}
