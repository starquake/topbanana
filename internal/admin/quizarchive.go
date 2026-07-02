package admin

// archiveFormatVersion is the on-disk schema version stamped into every
// exported quiz archive's manifest. It lets a later importer reject (or
// migrate) an archive whose layout it does not understand. Bump it only on
// a breaking change to the manifest shape.
const archiveFormatVersion = 1

// quizArchiveManifest is the decoded form of the quiz.json file at the root
// of an exported quiz archive (the .zip). It is a superset of the
// paste-import payload ([quizImportPayload]): it reuses the same field names
// so the import slice can share the round/question/option shape, and adds
// FormatVersion, Visibility, Mode, and per-question Image / Audio references
// that point at the archive's media/<id>.<ext> files. Only serialization is
// exercised today (export); the import slice consumes the same types.
//
// Questions and Rounds are mutually exclusive, mirroring the import payload:
// a quiz with authored rounds exports Rounds; a flat quiz exports a top-level
// Questions list.
type quizArchiveManifest struct {
	FormatVersion    int                   `json:"formatVersion"`
	Title            string                `json:"title"`
	Description      string                `json:"description"`
	TimeLimitSeconds *int                  `json:"timeLimitSeconds,omitempty"`
	Visibility       string                `json:"visibility"`
	Mode             string                `json:"mode"`
	Questions        []quizArchiveQuestion `json:"questions,omitempty"`
	Rounds           []quizArchiveRound    `json:"rounds,omitempty"`
}

// quizArchiveRound is one authored round in the manifest.
type quizArchiveRound struct {
	Title                   string                `json:"title"`
	Summary                 string                `json:"summary,omitempty"`
	BoundaryDurationSeconds *int                  `json:"boundaryDurationSeconds,omitempty"`
	Questions               []quizArchiveQuestion `json:"questions"`
}

// quizArchiveQuestion is one question in the manifest. Image and Audio are
// nil when the question has no attached media; when set they reference a file
// in the archive's media/ directory by relative path.
type quizArchiveQuestion struct {
	Text             string               `json:"text"`
	TimeLimitSeconds *int                 `json:"timeLimitSeconds,omitempty"`
	Image            *quizArchiveImageRef `json:"image,omitempty"`
	Audio            *quizArchiveAudioRef `json:"audio,omitempty"`
	Options          []quizArchiveOption  `json:"options"`
}

// quizArchiveOption is one answer option in the manifest.
type quizArchiveOption struct {
	Text    string `json:"text"`
	Correct bool   `json:"correct"`
}

// quizArchiveImageRef points at an image file stored in the archive. File is
// the archive-relative path ("media/<id>.<ext>"); MIME is the stored content
// type so the importer can re-register the media without re-sniffing.
// OriginalFilename carries the row's original upload name (#1137) so a re-import
// restores the library tooltip; empty for archives written before it was added.
type quizArchiveImageRef struct {
	File             string `json:"file"`
	MIME             string `json:"mime"`
	OriginalFilename string `json:"originalFilename,omitempty"`
}

// quizArchiveAudioRef points at an audio file stored in the archive. It
// carries the same File / MIME as the image ref plus the advisory audio
// metadata (host-supplied description, duration, repeat flag) so an import
// round-trips the clip's playback behaviour. OriginalFilename is the row's
// original upload name (#1137), empty for pre-#1137 archives.
type quizArchiveAudioRef struct {
	File             string `json:"file"`
	MIME             string `json:"mime"`
	Description      string `json:"description,omitempty"`
	DurationMs       *int   `json:"durationMs,omitempty"`
	Repeat           bool   `json:"repeat,omitempty"`
	OriginalFilename string `json:"originalFilename,omitempty"`
}
