// Package admin contains handlers for the admin dashboard
package admin

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gosimple/slug"

	"github.com/starquake/topbanana/internal/absurl"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/envtag"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/htmx"
	"github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/reltime"
	"github.com/starquake/topbanana/internal/render"
	"github.com/starquake/topbanana/internal/version"
	"github.com/starquake/topbanana/internal/web/tmpl"
)

// Validator is an interface for validating data.
type Validator interface {
	Valid(ctx context.Context) map[string]string
}

// baseLayout is the template name every admin page (and error page) executes.
const baseLayout = "base.gohtml"

// NewTemplateRenderer creates a renderer for the admin surface with the given
// logger, CSRF manager, and template path. It parses the template on creation.
//
// The CSRF manager may be nil for callers that render error pages without an
// embedded form (the placeholder {{csrfToken}} func still resolves to "").
func NewTemplateRenderer(logger *slog.Logger, csrfMgr *csrf.Manager, templatePath string) *render.Renderer {
	return render.New(logger, csrfMgr, parseTemplate(templatePath), baseLayout, adminPerRequestFuncs)
}

// adminPerRequestFuncs binds the admin top bar's per-request template funcs:
// the viewer's display name and admin flag (from the request context), the
// signed-in / section-nav flags the admin chrome always renders with, the
// admin logo href, the OG image URL, and the active nav section derived from
// the request path. render.Renderer binds csrfToken itself, so it is omitted
// here.
func adminPerRequestFuncs(r *http.Request) template.FuncMap {
	displayName := ""
	isAdmin := false
	if p, ok := auth.PlayerFromContext(r.Context()); ok {
		displayName = p.DisplayName
		isAdmin = p.IsAdmin()
	}

	section := navSection(r.URL.Path)

	return template.FuncMap{
		"viewerName":     func() string { return displayName },
		"isSignedIn":     func() bool { return true },
		"showSectionNav": func() bool { return true },
		"logoHref":       func() string { return "/admin" },
		"ogImage":        func() string { return absurl.BaseURL(r) + "/static/og-image.png" },
		"navSection":     func() string { return section },
		"isAdmin":        func() bool { return isAdmin },
	}
}

// QuizData is the data for the quiz list page, it shows multiple
// quizzes when available. CanEdit is the resolved
// "current-session-admin == creator" decision so the templates and
// the questions_list partial do not have to recompute the rule (#281)
// - handlers populate it via [attachCanEdit] before rendering, and a
// rule change lives entirely in Go.
type QuizData struct {
	ID            int64
	Title         string
	Slug          string
	Description   string
	UpdatedAt     time.Time
	QuestionCount int
	// RoundCount is the number of rounds, surfaced on the quiz-list card
	// footer; set by the list handler from the RoundCountsByQuiz aggregate
	// and 0 elsewhere (the detail view does not render the card).
	RoundCount           int
	CreatedByPlayerID    int64
	CreatedByDisplayName string
	CanEdit              bool
	TimeLimitSeconds     int
	Visibility           string
	// VisibilityOptions feeds the admin form's selector - pulled
	// straight from the domain constants so a future level addition
	// only touches one place.
	VisibilityOptions []string
	Mode              string
	// ModeOptions feeds the admin form's play-mode selector (MP-0 /
	// #677) - pulled straight from the domain constants.
	ModeOptions []string
	// PlayCount is the durable "times played" counter surfaced on the
	// admin quiz list footer (#891).
	PlayCount int64
	// Published reports whether the quiz is finished and locked from edits
	// (#1192). Draft quizzes show a Publish control; published ones show the
	// lock notice and an Unpublish control gated on CanUnpublish.
	Published bool
	// CanUnpublish reports whether a published quiz may still be unpublished:
	// true only while it has no real (non-preview) plays (#1192). The list
	// handler leaves it false (it does not compute per-quiz play state); the
	// quiz-view handler sets it from QuizHasRealPlays.
	CanUnpublish bool
	// ActionVariant selects which action cluster the shared quiz_card
	// partial renders ("admin" Edit/Delete vs. a future host variant);
	// html/template has no block/yield, so the card picks a named
	// sub-template by this discriminator (#889).
	ActionVariant string
	Questions     []*QuestionData
}

// QuestionData is the data for a question. TimeLimitSecondsValue is the
// pre-formatted value bound to the optional per-question time-limit
// input - empty when the question inherits the quiz default (#99), so
// the form's <input type="number"> stays blank rather than rendering 0.
type QuestionData struct {
	ID      int64
	QuizID  int64
	RoundID int64
	Text    string
	// ImageMediaID is the id of the attached library image, or 0 when none is
	// attached (#937). The picker pre-checks the radio whose value equals
	// it; 0 leaves the "None" radio checked.
	ImageMediaID int64
	// AudioMediaID is the id of the attached library audio, or 0 when none is
	// attached (#1059). Separate from ImageMediaID so a question can carry both
	// an image and audio; the audio picker pre-checks the radio matching it.
	AudioMediaID int64
	// AudioRepeat pre-checks the "repeat audio" checkbox; true makes the play
	// surfaces replay the attached clip up to 3 times (#1073).
	AudioRepeat           bool
	Position              int
	TimeLimitSecondsValue string
	Options               []*OptionData
}

// CorrectCount reports how many of the question's options are marked
// correct. Zero means the question has no correct option, a likely
// authoring mistake a caller can flag (#1141).
func (d *QuestionData) CorrectCount() int {
	n := 0
	for _, op := range d.Options {
		if op.Correct {
			n++
		}
	}

	return n
}

// RoundData backs the round sections on the quiz view and the round
// form. Mirrors the QuestionData/QuizData shape so the templates stay
// symmetric with their question equivalents (#444).
type RoundData struct {
	ID                      int64
	QuizID                  int64
	Title                   string
	Summary                 string
	Position                int
	BoundaryDurationSeconds *int
}

// BoundaryDurationSecondsValue is the pre-formatted value bound to the
// optional per-round boundary-duration input - empty when the round
// inherits the quiz default (#554), so the form's <input type="number">
// stays blank rather than rendering 0. Mirrors
// QuestionData.TimeLimitSecondsValue.
func (d *RoundData) BoundaryDurationSecondsValue() string {
	if d.BoundaryDurationSeconds == nil {
		return ""
	}

	return strconv.Itoa(*d.BoundaryDurationSeconds)
}

func roundDataFromRound(r *quiz.Round) *RoundData {
	return &RoundData{
		ID:                      r.ID,
		QuizID:                  r.QuizID,
		Title:                   r.Title,
		Summary:                 r.Summary,
		Position:                r.Position,
		BoundaryDurationSeconds: r.BoundaryDurationSeconds,
	}
}

// OptionData is the data for an option.
type OptionData struct {
	ID         int64
	QuestionID int64
	Text       string
	Correct    bool
	Position   int
}

const (
	maxOptions  = 4
	maxFormSize = 1 << 20 // 1 MB
)

// actionVariantAdmin selects the Edit/Delete action cluster in the shared
// quiz_card partial. The host variant lands with the host UI work (#889).
const actionVariantAdmin = "admin"

// attachCanEdit stamps qzd.CanEdit from the session player so templates
// can render the per-row affordances directly without recomputing the
// rule.
func attachCanEdit(r *http.Request, qzd *QuizData) {
	if qzd == nil {
		return
	}
	qzd.CanEdit = canEditQuiz(r, qzd.CreatedByPlayerID)
}

func quizDataFromQuiz(qz *quiz.Quiz) *QuizData {
	// QuestionCount defaults to len(Questions); the list handler overrides
	// it from a separate count query because ListQuizzes doesn't load the
	// question tree.
	visibility := qz.Visibility
	if visibility == "" {
		visibility = quiz.VisibilityPublic
	}
	mode := qz.Mode
	if mode == "" {
		mode = quiz.ModeSolo
	}

	return &QuizData{
		ID:                   qz.ID,
		Title:                qz.Title,
		Slug:                 qz.Slug,
		Description:          qz.Description,
		UpdatedAt:            qz.UpdatedAt,
		QuestionCount:        len(qz.Questions),
		CreatedByPlayerID:    qz.CreatedByPlayerID,
		CreatedByDisplayName: qz.CreatedByDisplayName,
		TimeLimitSeconds:     qz.TimeLimitSeconds,
		Visibility:           visibility,
		VisibilityOptions:    quiz.VisibilityValues(),
		Mode:                 mode,
		ModeOptions:          quiz.ModeValues(),
		PlayCount:            qz.PlayCount,
		Published:            qz.Published,
		ActionVariant:        actionVariantAdmin,
		Questions:            questionDataFromQuestions(qz.Questions),
	}
}

func quizDataFromQuizzes(quizzes []*quiz.Quiz) []*QuizData {
	data := make([]*QuizData, 0, len(quizzes))
	for _, qz := range quizzes {
		data = append(data, quizDataFromQuiz(qz))
	}

	return data
}

func questionDataFromQuestion(q *quiz.Question) *QuestionData {
	timeLimit := ""
	if q.TimeLimitSeconds != nil {
		timeLimit = strconv.Itoa(*q.TimeLimitSeconds)
	}

	var mediaID int64
	if q.ImageMediaID != nil {
		mediaID = *q.ImageMediaID
	}

	var audioMediaID int64
	if q.AudioMediaID != nil {
		audioMediaID = *q.AudioMediaID
	}

	return &QuestionData{
		ID:                    q.ID,
		QuizID:                q.QuizID,
		RoundID:               q.RoundID,
		Text:                  q.Text,
		ImageMediaID:          mediaID,
		AudioMediaID:          audioMediaID,
		AudioRepeat:           q.AudioRepeat,
		Position:              q.Position,
		TimeLimitSecondsValue: timeLimit,
		Options:               optionDataFromOptions(q.Options),
	}
}

func questionDataFromQuestions(questions []*quiz.Question) []*QuestionData {
	data := make([]*QuestionData, 0, len(questions))
	for _, q := range questions {
		data = append(data, questionDataFromQuestion(q))
	}

	slices.SortFunc(
		data,
		func(a, b *QuestionData) int { return a.Position - b.Position },
	)

	return data
}

func optionDataFromOption(op *quiz.Option) *OptionData {
	return &OptionData{
		ID:         op.ID,
		QuestionID: op.QuestionID,
		Text:       op.Text,
		Correct:    op.Correct,
	}
}

func optionDataFromOptions(options []*quiz.Option) []*OptionData {
	data := make([]*OptionData, 0, len(options))
	for _, op := range options {
		data = append(data, optionDataFromOption(op))
	}

	slices.SortFunc(
		data,
		func(a, b *OptionData) int { return a.Position - b.Position },
	)

	return data
}

// parseTemplate parses a template from the given path with layouts.
//
// Placeholder "viewerName", "csrfToken", and "navSection" funcs are
// registered before parse so the shared top bar's
// {{viewerName}}/{{navSection}} calls and any form's {{csrfToken}} call
// resolve at parse time. render.Renderer clones the parsed tree per request
// and replaces these placeholders (via adminPerRequestFuncs and the renderer's
// own csrfToken binding) with implementations that read the request context,
// CSRF manager, and request path, respectively.
//
// "humanizeTime" is a pure function of its argument, so it's registered with
// its real implementation here - no per-request override needed.
func parseTemplate(path string) *template.Template {
	funcs := template.FuncMap{
		"viewerName":        func() string { return "" },
		"isSignedIn":        func() bool { return false },
		"showSectionNav":    func() bool { return false },
		"logoHref":          func() string { return "/admin" },
		"profileHref":       func() string { return "/profile?next=/admin" },
		"csrfToken":         func() string { return "" },
		"ogImage":           func() string { return "" },
		"navSection":        func() string { return "" },
		"isAdmin":           func() bool { return false },
		"envTitleTag":       envtag.Get,
		"versionLabel":      version.Label,
		"humanizeTime":      reltime.Humanize,
		"passwordMinLength": func() int { return auth.MinPasswordLength },
	}
	// Partials are parsed alongside layouts so any page (or any HTMX-fragment
	// handler) can {{template "name" .}} a shared block without re-listing it.
	return render.Parse(
		tmpl.FS, funcs, path,
		"components/*.gohtml", "admin/layouts/*.gohtml", "admin/partials/*.gohtml",
	)
}

// render400 renders the 400 error page with the given message.
// Should be used as the final handler in the chain and probably be followed by a return.
//
// Error pages embed the top bar (which contains the logout form), so they need
// a CSRF manager to render a working {{csrfToken}}. We accept it as a
// parameter rather than re-derive it because error renderers are spawned ad
// hoc deep in the call stack - passing it explicitly keeps the rendering path
// honest about its dependencies.
func render400(w http.ResponseWriter, r *http.Request, logger *slog.Logger, csrfMgr *csrf.Manager, msg string) {
	renderer := render.New(
		logger,
		csrfMgr,
		parseTemplate("admin/errors/400.gohtml"),
		baseLayout,
		adminPerRequestFuncs,
	)
	data := struct {
		Title   string
		Message string
	}{
		Title:   "Error",
		Message: msg,
	}
	renderer.Render(w, r, http.StatusBadRequest, data)
}

// render404 renders the 404 error page.
// Should be used as the final handler in the chain and probably be followed by a return.
func render404(w http.ResponseWriter, r *http.Request, logger *slog.Logger, csrfMgr *csrf.Manager) {
	renderer := render.New(
		logger,
		csrfMgr,
		parseTemplate("admin/errors/404.gohtml"),
		baseLayout,
		adminPerRequestFuncs,
	)
	renderer.Render(w, r, http.StatusNotFound, nil)
}

// render403 renders the 403 error page with a message that names the
// quiz the caller tried to modify and the admin who owns it. Used by
// requireQuizOwner so a wrong-owner attempt surfaces a clear "not your
// quiz, ask <name> to make the change" instead of a generic 403.
func render403(w http.ResponseWriter, r *http.Request, logger *slog.Logger, csrfMgr *csrf.Manager, msg string) {
	renderer := render.New(
		logger,
		csrfMgr,
		parseTemplate("admin/errors/403.gohtml"),
		baseLayout,
		adminPerRequestFuncs,
	)
	data := struct {
		Title   string
		Message string
	}{
		Title:   "Forbidden",
		Message: msg,
	}
	renderer.Render(w, r, http.StatusForbidden, data)
}

// render409 renders the 409 conflict error page with the given message. Used
// by the edit-lock gate (a published quiz cannot be edited, #1192) and the
// unpublish gate (a played quiz can no longer be unpublished).
func render409(w http.ResponseWriter, r *http.Request, logger *slog.Logger, csrfMgr *csrf.Manager, msg string) {
	renderer := render.New(
		logger,
		csrfMgr,
		parseTemplate("admin/errors/409.gohtml"),
		baseLayout,
		adminPerRequestFuncs,
	)
	data := struct {
		Title   string
		Message string
	}{
		Title:   "Conflict",
		Message: msg,
	}
	renderer.Render(w, r, http.StatusConflict, data)
}

// render500 renders the 500 error page.
// Should be used as the final handler in the chain and probably be followed by a return.
func render500(w http.ResponseWriter, r *http.Request, logger *slog.Logger, csrfMgr *csrf.Manager) {
	renderer := render.New(
		logger,
		csrfMgr,
		parseTemplate("admin/errors/500.gohtml"),
		baseLayout,
		adminPerRequestFuncs,
	)
	renderer.Render(w, r, http.StatusInternalServerError, nil)
}

// requireQuizOwner loads the quiz and gates the request on the session
// player being its creator. Returns the loaded quiz on success;
// renders 403 / 404 / 500 on the failure paths.
func requireQuizOwner(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	id int64,
) (*quiz.Quiz, bool) {
	qz, ok := quizByID(w, r, logger, csrfMgr, quizStore, id)
	if !ok {
		return nil, false
	}

	// RequireAdmin (auth/middleware.go) already enforces a populated
	// player on the context before any admin handler runs, and
	// canEditQuiz below handles the not-present case correctly. The
	// previous explicit check rendered 500 on a state that's
	// unreachable under the production wiring (#371).
	if canEditQuiz(r, qz.CreatedByPlayerID) {
		return qz, true
	}

	owner := qz.CreatedByDisplayName
	if owner == "" {
		owner = "another admin"
	}
	render403(w, r, logger, csrfMgr, fmt.Sprintf(
		"Only %s can edit \"%s\". Ask them to make the change, or have them transfer ownership.",
		owner, qz.Title,
	))

	return nil, false
}

// requireEditableQuizOwner is requireQuizOwner plus the publish edit-lock
// (#1192): a published quiz is finished and locked from content edits, so every
// content-mutating admin handler gates through this. It renders a 409 and
// returns false when the (owned) quiz is published; the publish / unpublish
// handlers deliberately keep plain requireQuizOwner so they can still flip the
// flag.
func requireEditableQuizOwner(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	id int64,
) (*quiz.Quiz, bool) {
	qz, ok := requireQuizOwner(w, r, logger, csrfMgr, quizStore, id)
	if !ok {
		return nil, false
	}

	if qz.Published {
		render409(w, r, logger, csrfMgr,
			"This quiz is published and locked from edits. Unpublish it first to make changes.")

		return nil, false
	}

	return qz, true
}

// quizByID returns the quiz with the given ID from the store. It includes the questions.
// It logs any errors that occur, renders the errorpage and returns false.
func quizByID(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	id int64,
) (*quiz.Quiz, bool) {
	q, err := quizStore.GetQuiz(r.Context(), id)
	if err != nil {
		if errors.Is(err, quiz.ErrQuizNotFound) || errors.Is(err, quiz.ErrQuestionNotFound) {
			// User-supplied bad ID (or stale link after delete) - Info,
			// not Error (#369).
			logger.InfoContext(r.Context(), "quiz not found", slog.Any("err", err))
			render404(w, r, logger, csrfMgr)

			return nil, false
		}
		logger.ErrorContext(r.Context(), "error fetching data", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return nil, false
	}

	return q, true
}

// questionByID loads the question with the given ID and verifies it
// belongs to the supplied quizID. A mismatch renders as 404 (not 403)
// so the route never leaks "this question exists on another quiz"
// - the IDOR fix for #339 lives here: every mutating question route
// is quiz-scoped in the URL, so loading by questionID alone would let
// an admin who owns quizA edit a question on quizB by mounting it as
// /admin/quizzes/A/questions/B-question. SwapQuestionPositions does
// its own quiz-scoping; the read + write + delete paths route through
// this helper.
func questionByID(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	quizID, questionID int64,
) (*quiz.Question, bool) {
	qs, err := quizStore.GetQuestion(r.Context(), questionID)
	if err != nil {
		if errors.Is(err, quiz.ErrQuestionNotFound) {
			logger.InfoContext(
				r.Context(),
				fmt.Sprintf("question with ID %d not found", questionID),
				slog.Any("err", err),
			)
			render404(w, r, logger, csrfMgr)

			return nil, false
		}
		logger.ErrorContext(
			r.Context(),
			fmt.Sprintf("error fetching data for question with ID %d", questionID),
			slog.Any("err", err),
		)
		render500(w, r, logger, csrfMgr)

		return nil, false
	}

	if qs.QuizID != quizID {
		logger.InfoContext(
			r.Context(),
			fmt.Sprintf("question %d belongs to quiz %d, not URL-scoped quiz %d", questionID, qs.QuizID, quizID),
		)
		render404(w, r, logger, csrfMgr)

		return nil, false
	}

	return qs, true
}

// fillQuizFromForm fills the quiz fields from the form values.
// On a parse error it renders a 400 page directly and returns
// (nil, false); the caller should just return. On a validation error
// it leaves the fields populated on qz so the caller can re-render the
// form, and returns (fieldErrors, true) with a non-empty map keyed by
// lowercased form-field name (title, description). On success it
// returns (nil, true).
func fillQuizFromForm(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	qz *quiz.Quiz,
) (map[string]string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
	err := r.ParseForm()
	if err != nil {
		msg := "error parsing form"
		logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
		render400(w, r, logger, csrfMgr, msg)

		return nil, false
	}
	qz.Title = r.PostFormValue("title")
	qz.Slug = slug.Make(qz.Title)
	qz.Description = r.PostFormValue("description")
	// Per-quiz default time limit (#99). Empty input falls back to the
	// migration default so a host that never touched the field still
	// gets the historical 10-second window; an unparseable value lands
	// 0, which the Quiz.Valid range check rejects with an inline error.
	raw := strings.TrimSpace(r.PostFormValue("time_limit_seconds"))
	switch raw {
	case "":
		qz.TimeLimitSeconds = quiz.DefaultTimeLimitSeconds
	default:
		n, parseErr := strconv.Atoi(raw)
		if parseErr != nil {
			n = 0
		}
		qz.TimeLimitSeconds = n
	}
	// Visibility input (#103). Defaults to public if the form omits it
	// (older admin clients or curl probes); an unrecognised value is
	// passed through verbatim so Quiz.Valid surfaces an inline error.
	if v := r.PostFormValue("visibility"); v != "" {
		qz.Visibility = v
	} else {
		qz.Visibility = quiz.VisibilityPublic
	}
	// Play mode (MP-0 / #677). Defaults to solo if the form omits it; an
	// unrecognised value is passed through verbatim so Quiz.Valid
	// surfaces an inline error.
	if m := r.PostFormValue("mode"); m != "" {
		qz.Mode = m
	} else {
		qz.Mode = quiz.ModeSolo
	}
	if problems := (&quizForm{quiz: qz}).Valid(r.Context()); len(problems) > 0 {
		return problems, true
	}

	return nil, true
}

// parseOptionalTimeLimit interprets the optional per-question
// time_limit_seconds input. Blank -> nil (inherit the quiz default).
// Garbage -> a non-nil pointer to 0, which Question.Valid catches and
// surfaces as an inline range error.
func parseOptionalTimeLimit(raw string) *int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		n = 0
	}

	return &n
}

// Field-error messages for the question image picker (#937). Hoisted to
// package constants so the duplicated strings live in one place.
const (
	errMediaPickInvalid  = "Pick an image from this quiz's library, or choose None."
	errMediaNotInLibrary = "That image is not in this quiz's library."
	errMediaVerifyFailed = "Could not verify the selected image. Try again."
)

// Field-error messages for the question audio picker (#1059). Mirror the image
// strings but name audio, kept as package constants for one source of truth.
const (
	errAudioPickInvalid  = "Pick audio from this quiz's library, or choose None."
	errAudioNotInLibrary = "That audio is not in this quiz's library."
	errAudioVerifyFailed = "Could not verify the selected audio. Try again."
)

// resolveQuestionImage interprets the optional image_media_id picker input
// (#937). Blank or "0" -> (nil, "") meaning "no image attached" (NULL). A
// non-empty value must parse and must name an image (type=image) in quizID's own
// library; a missing, foreign, wrong-type, or unparseable id yields a field-error
// message so the save handler re-renders the form rather than persisting a
// cross-quiz or cross-type reference. A store failure also surfaces as a message
// so the caller never silently drops the attachment.
func resolveQuestionImage(
	ctx context.Context, mediaStore QuestionMediaStore, quizID int64, raw string,
) (*int64, string) {
	return resolveQuestionMediaOfType(
		ctx, mediaStore, quizID, raw, media.TypeImage,
		errMediaPickInvalid, errMediaNotInLibrary, errMediaVerifyFailed,
	)
}

// resolveQuestionAudio interprets the optional audio_media_id picker input
// (#1059). It mirrors resolveQuestionImage for the audio picker: blank or "0"
// means "no audio attached" (NULL); a non-empty value must name audio
// (type=audio) in quizID's own library, else a field-error message.
func resolveQuestionAudio(
	ctx context.Context, mediaStore QuestionMediaStore, quizID int64, raw string,
) (*int64, string) {
	return resolveQuestionMediaOfType(
		ctx, mediaStore, quizID, raw, media.TypeAudio,
		errAudioPickInvalid, errAudioNotInLibrary, errAudioVerifyFailed,
	)
}

// resolveQuestionMediaOfType is the shared body of resolveQuestionImage and
// resolveQuestionAudio: it validates the picker input names a ready media row of
// the wanted type in the question's own quiz, returning (id, "") on success or
// (nil, message) on any rejection. wantType pins the media kind so the image
// picker cannot attach audio and vice versa.
func resolveQuestionMediaOfType(
	ctx context.Context, mediaStore QuestionMediaStore, quizID int64, raw, wantType string,
	errInvalid, errNotInLibrary, errVerifyFailed string,
) (*int64, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" {
		return nil, ""
	}
	id, err := handlers.IDFromString(raw)
	if err != nil || id == 0 || mediaStore == nil {
		return nil, errInvalid
	}
	m, err := mediaStore.GetMedia(ctx, id)
	if err != nil {
		if errors.Is(err, media.ErrMediaNotFound) {
			return nil, errNotInLibrary
		}

		return nil, errVerifyFailed
	}
	if m.QuizID != quizID || m.Type != wantType {
		return nil, errNotInLibrary
	}

	return &id, ""
}

// fillQuestionFromForm fills the question fields from the form values.
// On a parse error it renders a 400 page directly and returns
// (nil, false); the caller should just return. On a validation error
// it leaves the fields populated on qs so the caller can re-render the
// form, and returns (fieldErrors, true) with a non-empty map keyed by
// lowercased form-field name (text, options). On success it returns
// (nil, true).
func fillQuestionFromForm(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	mediaStore QuestionMediaStore,
	qs *quiz.Question,
) (map[string]string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
	err := r.ParseForm()
	if err != nil {
		msg := "error parsing form"
		logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
		render400(w, r, logger, csrfMgr, msg)

		return nil, false
	}

	qs.Text = r.PostFormValue("text")
	// Image picker (#937). An empty/absent image_media_id means "no image"
	// (NULL); a non-empty value must name an image in this question's own
	// quiz library, validated below.
	mediaID, mediaErr := resolveQuestionImage(r.Context(), mediaStore, qs.QuizID, r.PostFormValue("image_media_id"))
	if mediaErr != "" {
		return map[string]string{"media": mediaErr}, true
	}
	qs.ImageMediaID = mediaID
	// Audio picker (#1059). An empty/absent audio_media_id means "no audio"
	// (NULL); a non-empty value must name audio in this question's own quiz
	// library, validated below.
	audioID, audioErr := resolveQuestionAudio(r.Context(), mediaStore, qs.QuizID, r.PostFormValue("audio_media_id"))
	if audioErr != "" {
		return map[string]string{"audio": audioErr}, true
	}
	qs.AudioMediaID = audioID
	// An unchecked HTML checkbox sends no value; checked sends its value (#1073).
	qs.AudioRepeat = r.PostFormValue("audio_repeat") != ""
	// Optional per-question override (#99). Blank input clears any
	// previous override (NULL -> inherit the quiz default); a parse
	// failure lands a zero, which Question.Valid rejects with an
	// inline range error rather than silently saving a bad value.
	qs.TimeLimitSeconds = parseOptionalTimeLimit(r.PostFormValue("time_limit_seconds"))

	newOptions := make([]*quiz.Option, 0, maxOptions)

	for i := range maxOptions {
		var op *quiz.Option
		if i < len(qs.Options) {
			op = qs.Options[i]
		} else {
			op = &quiz.Option{
				QuestionID: qs.ID,
			}
		}
		if r.PostForm.Has(fmt.Sprintf("option[%d].text", i)) {
			op.ID, err = handlers.IDFromString(r.PostFormValue(fmt.Sprintf("option[%d].id", i)))
			if err != nil {
				msg := "error parsing optionID"
				logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
				render400(w, r, logger, csrfMgr, msg)

				return nil, false
			}
			op.Text = r.PostFormValue(fmt.Sprintf("option[%d].text", i))
			op.Correct = r.PostFormValue(fmt.Sprintf("option[%d].correct", i)) == "on"

			newOptions = append(newOptions, op)
		}
	}
	qs.Options = newOptions

	if problems := (&questionForm{question: qs}).Valid(r.Context()); len(problems) > 0 {
		return problems, true
	}

	return nil, true
}

// storeQuiz persists qz via the appropriate Create/Update path. It does
// no rendering; callers branch on the returned error so they can pick
// the right user-facing response - in particular [quiz.ErrSlugTaken],
// which both HandleQuizSave and HandleQuizImportSave translate into a
// 409 + form re-render with an inline message (#293) rather than the
// generic 500 the wrapped SQL error used to produce.
func storeQuiz(ctx context.Context, quizStore quiz.Store, qz *quiz.Quiz) error {
	if qz.ID == 0 {
		if err := quizStore.CreateQuiz(ctx, qz); err != nil {
			return fmt.Errorf("create quiz: %w", err)
		}

		return nil
	}
	if err := quizStore.UpdateQuiz(ctx, qz); err != nil {
		return fmt.Errorf("update quiz: %w", err)
	}

	return nil
}

// storeQuestion creates or updates a question in the store. On a new
// question (ID == 0) it routes through CreateQuestionAtNextPosition so
// the position read + insert run inside a single transaction, killing
// the TOCTOU race that produced two questions at the same position
// under concurrent "Add question" clicks (#352).
func storeQuestion(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	qs *quiz.Question,
) bool {
	var err error
	if qs.ID == 0 {
		err = quizStore.CreateQuestionAtNextPosition(r.Context(), qs)
		if err != nil {
			logger.ErrorContext(r.Context(), "error creating question", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return false
		}
	} else {
		err = quizStore.UpdateQuestion(r.Context(), qs)
		if err != nil {
			logger.ErrorContext(r.Context(), "error updating question", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return false
		}
	}

	return true
}

// ActiveSessionLookup is the slice of the live-session service the dashboard
// needs: resolve the signed-in host's current non-finished room so the page can
// offer a "Resume session" link back to it (#836, #850). Kept narrow so the
// admin package does not depend on the whole live-session service.
type ActiveSessionLookup interface {
	GetActiveSessionForHost(ctx context.Context, hostPlayerID int64) (*livesession.Session, error)
}

// RunningGameLookup is the slice of the live-session service the quiz view needs
// to gate the "Host live" confirm-and-restart prompt (#853): report whether the
// signed-in host already has a game in flight. Kept narrow so the admin package
// does not depend on the whole live-session service.
type RunningGameLookup interface {
	HostHasRunningGame(ctx context.Context, hostPlayerID int64) (bool, error)
}

// MediaLister is the slice of the media store the quiz view needs to render the
// per-quiz image library (#936 slice 3): the quiz's images, newest first. It is
// defined consumer-side so the admin package depends only on the read it makes;
// the concrete media store satisfies it.
type MediaLister interface {
	ListMediaByQuiz(ctx context.Context, quizID int64) ([]*media.Media, error)
}

// QuestionMediaStore is the slice of the media store the question editor needs
// (#937): list a quiz's library to render the image picker, and get a single
// media row to validate that an attached image belongs to the question's own
// quiz before persisting it. Defined consumer-side; the concrete media store
// satisfies it.
type QuestionMediaStore interface {
	ListMediaByQuiz(ctx context.Context, quizID int64) ([]*media.Media, error)
	GetMedia(ctx context.Context, id int64) (*media.Media, error)
}

// indexData feeds the admin dashboard. ResumeCode is the join code of the
// host's current active room, empty when they have none. The single adaptive
// host control reflects it: a "Resume session" link when set, the "Host a
// session" entry otherwise (#836, #850).
type indexData struct {
	Title      string
	ResumeCode string
}

// HandleIndex returns the index page. Its single adaptive host control is the
// "Host a session" entry (an empty-room POST to /host) when the signed-in host
// has no active room, or a "Resume session" link back to it when they do (#836,
// #850). sessions resolves that active room; it may be nil for callers that do
// not wire the live-session service, in which case the resume link is never
// shown.
func HandleIndex(logger *slog.Logger, csrfMgr *csrf.Manager, sessions ActiveSessionLookup) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/index.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := indexData{Title: "Admin Dashboard"}
		data.ResumeCode = activeRoomCode(r, logger, sessions)
		renderer.Render(w, r, http.StatusOK, data)
	})
}

// activeRoomCode resolves the join code of the signed-in host's current active
// room, or "" when they have none, the service is not wired, or the lookup
// fails. A lookup failure is logged and degraded to "no resume link" rather than
// failing the whole dashboard render - the link is a convenience, not the page.
func activeRoomCode(r *http.Request, logger *slog.Logger, sessions ActiveSessionLookup) string {
	if sessions == nil {
		return ""
	}
	player, ok := auth.PlayerFromContext(r.Context())
	if !ok {
		return ""
	}
	sess, err := sessions.GetActiveSessionForHost(r.Context(), player.ID)
	if err != nil {
		logger.ErrorContext(r.Context(), "error looking up active host session", slog.Any("err", err))

		return ""
	}
	if sess == nil {
		return ""
	}

	return sess.JoinCode
}

// HandleQuizList returns the quiz list page. The optional mode query param
// filters the list by play mode (#851): "solo" or "live" keeps only quizzes of
// that mode; anything else (including absent) shows all. The chosen mode is
// passed to the template so it can mark the active Solo / Live / All filter tab.
func HandleQuizList(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizlist.gohtml")

	type quizListData struct {
		Title   string
		Quizzes []*QuizData
		Mode    string
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		quizzes, err := quizStore.ListQuizzes(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "error retrieving quizzes from store", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		// Counts come from a separate aggregate query so the Quiz domain
		// type doesn't have to carry a list-only field. A quiz with no
		// questions is absent from the map; the lookup yields 0.
		// A question added or deleted between this call and ListQuizzes
		// above can produce a count that's off by one for a single render
		// - acceptable for a read view; eventual consistency is fine.
		counts, err := quizStore.QuestionCountsByQuiz(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "error retrieving question counts from store", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}
		roundCounts, err := quizStore.RoundCountsByQuiz(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "error retrieving round counts from store", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		// Filter by play mode in Go from the single ListQuizzes read so the
		// same handler serves solo / live / all without a second query path
		// (#851). Only the recognised modes filter; anything else shows all.
		mode := r.URL.Query().Get("mode")
		quizzes = filterQuizzesByMode(quizzes, mode)

		qzd := quizDataFromQuizzes(quizzes)
		for _, qd := range qzd {
			qd.QuestionCount = counts[qd.ID]
			qd.RoundCount = roundCounts[qd.ID]
			attachCanEdit(r, qd)
		}

		data := quizListData{
			Title:   "Admin Dashboard - Quiz List",
			Quizzes: qzd,
			Mode:    mode,
		}

		renderer.Render(w, r, http.StatusOK, data)
	})
}

// filterQuizzesByMode keeps only quizzes whose Mode matches the requested play
// mode (#851). Only [quiz.ModeSolo] and [quiz.ModeLive] filter; any other value
// (including "") returns the list unchanged so the "All" tab shows everything.
// A quiz with an empty Mode is treated as solo, matching the store-layer default.
func filterQuizzesByMode(quizzes []*quiz.Quiz, mode string) []*quiz.Quiz {
	if mode != quiz.ModeSolo && mode != quiz.ModeLive {
		return quizzes
	}
	filtered := make([]*quiz.Quiz, 0, len(quizzes))
	for _, qz := range quizzes {
		qm := qz.Mode
		if qm == "" {
			qm = quiz.ModeSolo
		}
		if qm == mode {
			filtered = append(filtered, qz)
		}
	}

	return filtered
}

// PlayerScoreData represents one row of the "Played by" table on the quiz
// view page: a player who has finished every quiz question, alongside
// their accumulated score (computed by the game service in the same way
// the public leaderboard computes its scores). HandleQuizView filters out
// in-progress and pre-answer participants (#244/#335) so the admin's
// Reset button is only offered for games the host can safely wipe.
type PlayerScoreData struct {
	PlayerID    int64
	DisplayName string
	Score       int
}

// HandleQuizView returns the quiz view page. It also fetches the per-quiz
// leaderboard so the admin can see who has played and reset their attempt
// from the same screen. We reuse the leaderboard service with a high limit
// rather than spinning up a dedicated "list participants" service method -
// see #145 for the rationale (and #141 for the performance ceilings).
func HandleQuizView(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	gameService *game.Service,
	runningGames RunningGameLookup,
	mediaLister MediaLister,
	uploadLimits MediaUploadLimits,
) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizview.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var id int64
		if id, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		var qz *quiz.Quiz
		if qz, ok = quizByID(w, r, logger, csrfMgr, quizStore, id); !ok {
			return
		}

		players, ok := loadCompletedPlayers(w, r, logger, csrfMgr, gameService, id)
		if !ok {
			return
		}

		rounds, ok := loadRounds(w, r, logger, csrfMgr, quizStore, id)
		if !ok {
			return
		}

		images, sounds, ok := loadQuizMedia(w, r, logger, csrfMgr, mediaLister, id)
		if !ok {
			return
		}

		quizData := quizDataFromQuiz(qz)
		attachCanEdit(r, quizData)
		if quizData.Published {
			// A published quiz can be unpublished only until a real (non-preview)
			// game has started (#1192); the view gates the Unpublish control on it.
			hasPlays, err := quizStore.QuizHasRealPlays(r.Context(), id)
			if err != nil {
				logger.ErrorContext(r.Context(), "error checking quiz real plays", slog.Any("err", err))
				render500(w, r, logger, csrfMgr)

				return
			}
			quizData.CanUnpublish = !hasPlays
		}
		data := newQuizViewData(quizData, players, rounds)
		data.Images = images
		data.Sounds = sounds
		data.UploadLimits = uploadLimits
		data.HostHasRunningGame = hostHasRunningGame(r, logger, runningGames)
		data.UploadedCount, data.FailedCount, data.CancelledCount = parseUploadCounts(r)
		renderer.Render(w, r, http.StatusOK, data)
	})
}

// hostHasRunningGame reports whether the signed-in host already has a game in
// flight, so the quiz view can gate the "Host live" confirm-and-restart prompt
// (#853). A lookup failure is logged and degraded to false rather than failing
// the whole render: the page still serves, and the #851 in-flight no-op still
// protects the running game server-side. Returns false when the service is not
// wired or no player is on the context.
func hostHasRunningGame(r *http.Request, logger *slog.Logger, runningGames RunningGameLookup) bool {
	if runningGames == nil {
		return false
	}
	player, ok := auth.PlayerFromContext(r.Context())
	if !ok {
		return false
	}
	running, err := runningGames.HostHasRunningGame(r.Context(), player.ID)
	if err != nil {
		logger.ErrorContext(r.Context(), "error looking up running host game", slog.Any("err", err))

		return false
	}

	return running
}

// QuizViewData is the data passed to the quiz view template. Questions
// are grouped into rounds in play order; the template ranges over
// Rounds instead of a flat question list (#444).
type QuizViewData struct {
	Title   string
	Quiz    *QuizData
	Players []PlayerScoreData
	// Rounds is the position-ordered round list, each carrying its own
	// questions, for the grouped quiz view.
	Rounds []RoundViewData
	// Images is the quiz's image library, newest first, for the thumbnail
	// grid (#936 slice 3). The upload control and grid are gated on CanEdit
	// in the template; the data loads regardless so an owner sees their
	// library.
	Images []MediaCardData
	// Sounds is the quiz's sound library, newest first, for the audio section
	// (#1059). Each tile shows a duration label and an inline audio preview.
	// Gated on CanEdit in the template, like Images.
	Sounds []MediaCardData
	// UploadLimits are the media caps shown as helper text near the upload
	// pickers and fed to the client-side pre-upload size guard (#1139), so a
	// host does not pick a file the server would reject.
	UploadLimits MediaUploadLimits
	// HostHasRunningGame gates the "Host live" confirm-and-restart prompt
	// (#853): true when the signed-in host already has a game in flight, so the
	// control opens a modal that ends the running session before hosting this
	// quiz instead of submitting straight away.
	HostHasRunningGame bool
	// UploadedCount / FailedCount / CancelledCount drive the post-upload
	// banner. The upload flow redirects with ?uploaded=N&failed=M&cancelled=K
	// (#951) so the page can show what just happened without a session-flash
	// mechanism. All three are 0 on a plain visit; clamped to a small ceiling
	// so a tampered query can't paint a misleading number.
	UploadedCount  int
	FailedCount    int
	CancelledCount int
}

// RoundViewData is one round section on the quiz view: the round itself
// and its questions in quiz-wide position order.
type RoundViewData struct {
	Round     *RoundData
	Questions []*QuestionData
}

// buildRoundView groups the quiz's questions under their rounds in
// position order. Questions keep their quiz-wide position order within a
// round; a round with no questions still renders its section. Questions
// whose round_id matches no round (a defensive case) are dropped from
// the grouped view rather than duplicated.
func buildRoundView(rounds []*quiz.Round, questions []*QuestionData) []RoundViewData {
	byRound := make(map[int64][]*QuestionData, len(rounds))
	for _, q := range questions {
		byRound[q.RoundID] = append(byRound[q.RoundID], q)
	}

	views := make([]RoundViewData, 0, len(rounds))
	for _, rnd := range rounds {
		views = append(views, RoundViewData{
			Round:     roundDataFromRound(rnd),
			Questions: byRound[rnd.ID],
		})
	}

	return views
}

// loadRounds fetches the quiz's rounds in position order. Errors are
// 500s because the section is part of the same admin view that already
// loaded the quiz tree; surfacing an empty list would hide the
// failure.
func loadRounds(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	quizID int64,
) ([]*quiz.Round, bool) {
	rounds, err := quizStore.ListRoundsByQuiz(r.Context(), quizID)
	if err != nil {
		logger.ErrorContext(r.Context(), "error listing rounds for quiz view", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return nil, false
	}

	return rounds, true
}

// MediaCardData is one tile in the quiz view's image library grid (#936 slice
// 3). It carries only what the template renders: the media id, used to build the
// /media/{id} and /media/{id}/thumb URLs, plus the stored dimensions so the
// thumbnail reserves its aspect ratio. The full presentation type keeps the
// template free of the domain media.Media struct.
type MediaCardData struct {
	ID     int64
	Width  int
	Height int
	// DurationMs is the clip length for a sound tile, or nil when unknown / not
	// applicable (an image leaves it nil) (#1059). DurationLabel renders it.
	DurationMs *int
	// QuizID is the owning quiz, used by a sound tile to build the
	// description-edit action URL (#1072).
	QuizID int64
	// Description is the host-supplied label for a sound tile (#1072), shown in
	// the audio library and the question picker. Empty for an unlabelled clip and
	// for image tiles, which do not surface it.
	Description string
	// OriginalFilename is the file's original upload name (#1137), surfaced in the
	// library so a host can match a stored file back to its source. Empty when the
	// upload carried no usable name.
	OriginalFilename string
}

// DurationLabel renders DurationMs as an "M:SS" clip-length label, or "" when
// the duration is unknown (#1059). Seconds are zero-padded; minutes are not.
func (d MediaCardData) DurationLabel() string {
	if d.DurationMs == nil || *d.DurationMs <= 0 {
		return ""
	}
	totalSeconds := *d.DurationMs / millisPerSecond
	minutes := totalSeconds / secondsPerMinute
	seconds := totalSeconds % secondsPerMinute

	return fmt.Sprintf("%d:%02d", minutes, seconds)
}

const (
	millisPerSecond  = 1000
	secondsPerMinute = 60
)

func mediaCardDataFromMedia(items []*media.Media) []MediaCardData {
	cards := make([]MediaCardData, 0, len(items))
	for _, m := range items {
		cards = append(cards, MediaCardData{
			ID:               m.ID,
			Width:            m.Width,
			Height:           m.Height,
			DurationMs:       m.DurationMs,
			QuizID:           m.QuizID,
			Description:      m.Description,
			OriginalFilename: m.OriginalFilename,
		})
	}

	return cards
}

// filterMediaByType returns the subset of items whose media Type matches
// mediaType, preserving order. The quiz view and the question pickers list
// images and sounds separately, so the loaders split one ListMediaByQuiz read
// (which returns every ready row) by type (#1059).
func filterMediaByType(items []*media.Media, mediaType string) []*media.Media {
	filtered := make([]*media.Media, 0, len(items))
	for _, m := range items {
		if m.Type == mediaType {
			filtered = append(filtered, m)
		}
	}

	return filtered
}

// loadQuizMedia fetches the quiz's media library, newest first, split into
// image and sound cards (#936 slice 3, #1059). A nil lister (callers that do not
// wire the media store) yields empty grids rather than a failure. A lookup error
// is a 500: the library is part of the same admin view that already loaded the
// quiz tree, so hiding the failure behind an empty grid would mask it.
func loadQuizMedia(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	mediaLister MediaLister,
	quizID int64,
) (images, sounds []MediaCardData, ok bool) {
	if mediaLister == nil {
		return nil, nil, true
	}
	items, err := mediaLister.ListMediaByQuiz(r.Context(), quizID)
	if err != nil {
		logger.ErrorContext(r.Context(), "error listing media for quiz view", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return nil, nil, false
	}

	return mediaCardDataFromMedia(filterMediaByType(items, media.TypeImage)),
		mediaCardDataFromMedia(filterMediaByType(items, media.TypeAudio)),
		true
}

func newQuizViewData(quizData *QuizData, players []PlayerScoreData, rounds []*quiz.Round) QuizViewData {
	return QuizViewData{
		Title:   "Admin Dashboard - View Quiz",
		Quiz:    quizData,
		Players: players,
		Rounds:  buildRoundView(rounds, quizData.Questions),
	}
}

// roundsPartialData mirrors the subset of QuizViewData the
// questions_list partial actually ranges over. Shared by the question
// and round move handlers so an HTMX swap keeps the page's scroll
// position instead of bouncing through a 303.
type roundsPartialData struct {
	Quiz   *QuizData
	Rounds []RoundViewData
}

// renderRoundsPartial refetches the quiz tree and emits the
// questions_list partial. Used by the HTMX paths of HandleQuestionMove
// and HandleRoundMove so a successful (or knowingly-impossible) move
// updates only the grouped block instead of a full page reload.
func renderRoundsPartial(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	renderer *render.Renderer,
	quizStore quiz.Store,
	quizID int64,
) {
	qz, ok := quizByID(w, r, logger, csrfMgr, quizStore, quizID)
	if !ok {
		return
	}
	rounds, ok := loadRounds(w, r, logger, csrfMgr, quizStore, quizID)
	if !ok {
		return
	}
	quizData := quizDataFromQuiz(qz)
	attachCanEdit(r, quizData)
	renderer.RenderPartial(w, r, "questions_list", roundsPartialData{
		Quiz:   quizData,
		Rounds: buildRoundView(rounds, quizData.Questions),
	})
}

// quizViewPlayersLimit is the upper bound on rows in the "Played by"
// section. Set high enough that real-world quiz playthroughs fit; #141
// covers pagination for genuinely large rosters.
const quizViewPlayersLimit = 1000

// loadCompletedPlayers pulls the leaderboard for the given quiz and
// returns only the entries that finished. Mid-quiz / pre-answer
// entries are skipped (#244/#335) so the admin's Reset button never
// pulls the rug from a live session. Writes a 500 page and returns
// ok=false on a service failure.
func loadCompletedPlayers(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	gameService *game.Service,
	quizID int64,
) ([]PlayerScoreData, bool) {
	// Admin "Played by" doesn't highlight a current player - the
	// template ignores IsCurrentPlayer - so pass 0 to flag nothing,
	// per Service.GetQuizLeaderboard's documented sentinel.
	result, err := gameService.GetQuizLeaderboard(r.Context(), quizID, 0, quizViewPlayersLimit)
	if err != nil {
		logger.ErrorContext(r.Context(), "error fetching players for quiz view", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return nil, false
	}

	players := make([]PlayerScoreData, 0, len(result.Entries))
	for _, e := range result.Entries {
		if !e.Completed {
			continue
		}
		players = append(players, PlayerScoreData{
			PlayerID:    e.PlayerID,
			DisplayName: e.DisplayName,
			Score:       e.Score,
		})
	}

	return players, true
}

// HandleResetGameForPlayer hard-deletes the games (and dependent rows) that
// the given player has on the given quiz. Idempotent: if the player has no
// games, it is a 303-redirect no-op. The admin reset button on the quiz
// view page POSTs here.
func HandleResetGameForPlayer(
	logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store, gameService *game.Service,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		// Owner gate (#281): only the quiz's creator can reset another
		// player's attempt on it. Same rule as every other mutating
		// admin route.
		if _, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		var playerID int64
		if playerID, ok = handlers.ParseIDFromPath(w, r, logger, "playerID"); !ok {
			return
		}

		if err := gameService.ResetGamesForPlayerOnQuiz(r.Context(), playerID, quizID); err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) {
				render404(w, r, logger, csrfMgr)

				return
			}
			logger.ErrorContext(r.Context(), "error resetting games for player on quiz", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		// htmx removes the player row in place via an outerHTML swap (a
		// reset deletes the player's game on this quiz, so they drop off
		// the played-by list); a plain form post falls back to the 303
		// reload of the quiz view.
		if htmx.IsRequest(r) {
			w.WriteHeader(http.StatusOK)

			return
		}

		// quizID came from ParseIDFromPath, which only returns an int64
		// once the path value parses cleanly - formatting it back via
		// strconv.FormatInt avoids gosec's open-redirect taint heuristic
		// for fmt.Sprintf with a path argument.
		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
	})
}

// HandleQuizCreate creates a quiz.
func HandleQuizCreate(logger *slog.Logger, csrfMgr *csrf.Manager) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Pre-fill the time-limit input with the project-wide default
		// so the form is a valid submission without the author having
		// to touch the new field; the HTML5 number input with
		// min=1/max=600 would otherwise reject the zero-value (#99).
		renderer.Render(w, r, http.StatusOK, quizFormData{
			Title: quizFormCreateTitle,
			Quiz: &QuizData{
				TimeLimitSeconds:  quiz.DefaultTimeLimitSeconds,
				Visibility:        quiz.VisibilityPublic,
				VisibilityOptions: quiz.VisibilityValues(),
				Mode:              quiz.ModeSolo,
				ModeOptions:       quiz.ModeValues(),
			},
		})
	})
}

// HandleQuizEdit handles the display of the quiz edit page in the admin dashboard.
func HandleQuizEdit(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		// Owner gate on the edit form itself so non-owners get a 403
		// up front instead of opening an editor they can't submit.
		var qz *quiz.Quiz
		if qz, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}
		renderer.Render(w, r, http.StatusOK, quizFormData{
			Title: quizFormEditTitle,
			Quiz:  quizDataFromQuiz(qz),
		})
	})
}

// quizFormData backs the quizform.gohtml template. Error is non-empty
// when the POST handler re-renders the form after a recoverable
// banner-level failure (currently the slug-collision 409 from #293).
// FieldErrors is non-empty when domain-level validation fails (#32) and
// surfaces the per-input message under each invalid field. Either path
// preserves the submitted Title/Description on Quiz so the admin can
// fix and retry without re-typing.
type quizFormData struct {
	Title       string
	Quiz        *QuizData
	Error       string
	FieldErrors map[string]string
}

// HandleQuizSave saves the quiz to the database.
func HandleQuizSave(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	formRenderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		newQuiz := quizID == 0
		var qz *quiz.Quiz
		if newQuiz {
			// CREATE: stamp the session admin as the creator so the
			// owner-gated mutating routes downstream can match (#281).
			qz = &quiz.Quiz{}
			if p, present := auth.PlayerFromContext(r.Context()); present {
				qz.CreatedByPlayerID = p.ID
			}
		} else {
			// UPDATE: only the creator may save, and only while the quiz is
			// still a draft. requireEditableQuizOwner loads the quiz, 403s
			// anyone else (#281), and 409s a published (locked) quiz (#1192).
			if qz, ok = requireEditableQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
				return
			}
		}

		fieldErrors, ok := fillQuizFromForm(w, r, logger, csrfMgr, qz)
		if !ok {
			return
		}
		title := quizFormEditTitle
		if newQuiz {
			title = quizFormCreateTitle
		}
		if len(fieldErrors) > 0 {
			// Domain-level validation failed. Re-render the same form
			// at 400 with FieldErrors set; the template uses them to
			// decorate each invalid input and show the per-field
			// message. Submitted values are preserved on qz.
			formRenderer.Render(w, r, http.StatusBadRequest, quizFormData{
				Title:       title,
				Quiz:        quizDataFromQuiz(qz),
				FieldErrors: fieldErrors,
			})

			return
		}

		if err := storeQuiz(r.Context(), quizStore, qz); err != nil {
			renderQuizSaveError(w, r, logger, csrfMgr, formRenderer, qz, title, err)

			return
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/quizzes/%d", qz.ID), http.StatusSeeOther)
	})
}

// renderQuizSaveError handles the storeQuiz failure paths for
// HandleQuizSave. Split out so HandleQuizSave's main flow keeps a single
// happy-path return. [quiz.ErrSlugTaken] re-renders the form at 409
// with the submitted Title/Description preserved (#293); anything else
// is treated as a genuine 500. pageTitle is the rendered <title> - the
// caller picks it from quizFormCreateTitle / quizFormEditTitle based on
// whether the POST landed on create or edit.
func renderQuizSaveError(
	w http.ResponseWriter, r *http.Request,
	logger *slog.Logger, csrfMgr *csrf.Manager,
	formRenderer *render.Renderer,
	qz *quiz.Quiz, pageTitle string, err error,
) {
	if errors.Is(err, quiz.ErrSlugTaken) {
		formRenderer.Render(w, r, http.StatusConflict, quizFormData{
			Title: pageTitle,
			Quiz:  quizDataFromQuiz(qz),
			Error: "A quiz with this title already exists - pick a different title (or rename the existing quiz).",
		})

		return
	}
	logger.ErrorContext(r.Context(), "error storing quiz", slog.Any("err", err))
	render500(w, r, logger, csrfMgr)
}

// Page <title> strings for the quiz create/edit form. Exposed as
// package-level constants so the GET (HandleQuizCreate / HandleQuizEdit)
// and the slug-conflict re-render path (HandleQuizSave) share one
// source of truth - a rename has to touch both renders together (#293).
const (
	quizFormCreateTitle = "Admin Dashboard - Create Quiz"
	quizFormEditTitle   = "Admin Dashboard - Edit Quiz"
)

// questionFormData backs questionform.gohtml. FieldErrors is set when
// HandleQuestionSave re-renders the form after a domain-level
// validation failure (#32); the per-input error message lives under
// the lowercased form-field name (text, options). Round is the round a
// new question will be created in (#929) - it backs the form's hidden
// round_id field and the breadcrumb, and is nil on the edit path where
// the question keeps its existing round.
type questionFormData struct {
	Title    string
	Quiz     *QuizData
	Question *QuestionData
	Round    *RoundData
	// Library is the question's own quiz image library, newest first, for
	// the image-picker grid (#937). Empty when the quiz has no images yet,
	// which the template renders as an upload-first hint instead of a
	// picker.
	Library []MediaCardData
	// AudioLibrary is the question's own quiz sound library, newest first, for
	// the audio-picker list (#1059). Empty when the quiz has no sounds yet.
	AudioLibrary []MediaCardData
	FieldErrors  map[string]string
}

// HandleQuestionCreate creates a question. The round the question lands
// in comes from the round_id query parameter set by the per-round "Add
// question" button (#929); it must name a round of this quiz. The form
// carries it forward as a hidden field so the POST creates the question
// in that round rather than the quiz default.
func HandleQuestionCreate(
	logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store, mediaStore QuestionMediaStore,
) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/questionform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		// Owner gate on the question-create form: non-owners 403
		// instead of seeing a form whose POST would fail anyway.
		var qz *quiz.Quiz
		if qz, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		rnd, ok := roundFromQuery(w, r, logger, csrfMgr, quizStore, quizID)
		if !ok {
			return
		}

		library, audioLibrary, ok := loadQuestionLibrary(w, r, logger, csrfMgr, mediaStore, quizID)
		if !ok {
			return
		}

		renderer.Render(w, r, http.StatusOK, questionFormData{
			Title:        "Admin Dashboard - Question Create",
			Quiz:         quizDataFromQuiz(qz),
			Question:     &QuestionData{},
			Round:        roundDataFromRound(rnd),
			Library:      library,
			AudioLibrary: audioLibrary,
		})
	})
}

// loadQuestionLibrary fetches the quiz's media library for the question
// editor's pickers (#937, #1059), newest first, split into image and sound
// cards. A nil store (callers that do not wire media) yields empty pickers
// rather than a failure; a lookup error is a 500, matching loadQuizMedia, since
// the library is part of the same editor page.
func loadQuestionLibrary(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	mediaStore QuestionMediaStore,
	quizID int64,
) (images, sounds []MediaCardData, ok bool) {
	if mediaStore == nil {
		return nil, nil, true
	}
	items, err := mediaStore.ListMediaByQuiz(r.Context(), quizID)
	if err != nil {
		logger.ErrorContext(r.Context(), "error listing media for question editor", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return nil, nil, false
	}

	return mediaCardDataFromMedia(filterMediaByType(items, media.TypeImage)),
		mediaCardDataFromMedia(filterMediaByType(items, media.TypeAudio)),
		true
}

// roundFromQuery reads the round_id query parameter and loads the named
// round, gated on it belonging to quizID. A missing, unparseable, or
// foreign round id renders the established 4xx (400 for a bad id, 404
// for a foreign one via roundByID) and returns ok=false - the create
// flow never falls back to a default round (#929).
func roundFromQuery(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	quizID int64,
) (*quiz.Round, bool) {
	roundID, err := handlers.IDFromString(r.URL.Query().Get("round_id"))
	if err != nil || roundID == 0 {
		render400(w, r, logger, csrfMgr, "invalid round id")

		return nil, false
	}

	return roundByID(w, r, logger, csrfMgr, quizStore, quizID, roundID)
}

// HandleQuestionEdit handles the display of the question edit page in the admin dashboard.
func HandleQuestionEdit(
	logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store, mediaStore QuestionMediaStore,
) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/questionform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		var questionID int64
		if questionID, ok = handlers.ParseIDFromPath(w, r, logger, "questionID"); !ok {
			return
		}
		newQuestion := questionID == 0

		qz, ok := requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID)
		if !ok {
			return
		}

		var qs *quiz.Question

		if newQuestion {
			qs = &quiz.Question{
				QuizID: quizID,
			}
		} else {
			qs, ok = questionByID(w, r, logger, csrfMgr, quizStore, quizID, questionID)
			if !ok {
				return
			}
		}

		library, audioLibrary, ok := loadQuestionLibrary(w, r, logger, csrfMgr, mediaStore, quizID)
		if !ok {
			return
		}

		renderer.Render(w, r, http.StatusOK, questionFormData{
			Title:        "Admin Dashboard - Question Edit",
			Quiz:         quizDataFromQuiz(qz),
			Question:     questionDataFromQuestion(qs),
			Library:      library,
			AudioLibrary: audioLibrary,
		})
	})
}

// HandleQuizSetMode flips a quiz between solo and live without going through
// the edit form (#830). The target mode is the {mode} path segment, mirroring
// the {direction} segment on the question-move route; only the quiz owner (or
// an admin) may change it. On success it redirects back to the quiz view so
// the re-rendered page reflects the new mode.
func HandleQuizSetMode(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		if _, ok = requireEditableQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		mode := r.PathValue("mode")
		if !quiz.IsValidMode(mode) {
			render400(w, r, logger, csrfMgr, "invalid play mode")

			return
		}

		if err := quizStore.SetQuizMode(r.Context(), quizID, mode); err != nil {
			if errors.Is(err, quiz.ErrQuizNotFound) {
				render404(w, r, logger, csrfMgr)

				return
			}
			logger.ErrorContext(r.Context(), "error setting quiz mode", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
	})
}

// QuizMediaRemover drops a quiz's on-disk media directory (#1174).
type QuizMediaRemover interface {
	RemoveQuizDir(quizID int64) error
}

// HandleQuizDelete deletes a quiz and all its questions and options.
func HandleQuizDelete(
	logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store, mediaSvc QuizMediaRemover,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		// Deleting a quiz is removal, not a content edit, so the publish
		// edit-lock does not apply: an owner/admin can delete a published quiz.
		if _, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		if err := quizStore.DeleteQuiz(r.Context(), quizID); err != nil {
			if errors.Is(err, quiz.ErrDeletingQuizNoRowsAffected) {
				render404(w, r, logger, csrfMgr)

				return
			}
			logger.ErrorContext(r.Context(), "error deleting quiz", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		// The cascade drops the media rows but not their files; unlink them
		// best-effort without failing the already-committed delete.
		if err := mediaSvc.RemoveQuizDir(quizID); err != nil {
			logger.WarnContext(r.Context(), "failed to remove quiz media directory after delete",
				slog.Int64("quiz_id", quizID), slog.Any("err", err))
		}

		// htmx removes the card in place via an outerHTML swap; a plain
		// form post falls back to the 303 reload of the quiz list.
		if htmx.IsRequest(r) {
			w.WriteHeader(http.StatusOK)

			return
		}

		http.Redirect(w, r, "/admin/quizzes", http.StatusSeeOther)
	})
}

// renderQuestionMoveError translates a SwapQuestionPositions failure
// into the right HTTP response. In HX-Request mode, boundary errors
// return 204 so the existing DOM stays in place; classic form posts
// redirect back to the quiz view.
//
//nolint:revive // htmxResponder is a wire-format selector, not a flag-as-mode toggle; splitting the function in two would duplicate the switch rather than clarify it.
func renderQuestionMoveError(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizID int64,
	err error,
	htmxResponder bool,
) {
	switch {
	case errors.Is(err, quiz.ErrInvalidDirection):
		render400(w, r, logger, csrfMgr, "invalid direction")
	case errors.Is(err, quiz.ErrQuestionAtTop),
		errors.Is(err, quiz.ErrQuestionAtBottom):
		// Boundary case: the button should have been disabled in
		// the UI, so a request here is unusual but harmless. For
		// HTMX, 204 leaves the existing DOM untouched; for the
		// classic form post, redirect back to the view.
		if htmxResponder {
			w.WriteHeader(http.StatusNoContent)
		} else {
			http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
		}
	case errors.Is(err, quiz.ErrQuestionNotFound):
		render404(w, r, logger, csrfMgr)
	default:
		logger.ErrorContext(r.Context(), "error swapping question positions", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)
	}
}

// HandleQuestionMove handles the per-row Up/Down reorder buttons on the
// quiz view (#16). The {direction} path segment must be "up" or "down";
// the underlying store handles the swap atomically and returns sentinel
// errors for boundary conditions (already at top/bottom) which we map
// to 400 here so the operator sees the cause rather than a generic
// 500. After a successful swap we redirect back to the quiz view; the
// re-rendered page reflects the new order from the database.
func HandleQuestionMove(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	// The HX-Request path renders only the questions_list partial. Reuse
	// the quiz-view template tree because parseTemplate loads every
	// admin/partials/*.gohtml alongside any page template, so the partial
	// is in scope for ExecuteTemplate by name.
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizview.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		if _, ok = requireEditableQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		var questionID int64
		if questionID, ok = handlers.ParseIDFromPath(w, r, logger, "questionID"); !ok {
			return
		}

		direction := r.PathValue("direction")
		isHX := htmx.IsRequest(r)

		if err := quizStore.SwapQuestionPositions(r.Context(), quizID, questionID, direction); err != nil {
			renderQuestionMoveError(w, r, logger, csrfMgr, quizID, err, isHX)

			return
		}

		if isHX {
			renderRoundsPartial(w, r, logger, csrfMgr, renderer, quizStore, quizID)

			return
		}

		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
	})
}

// HandleQuestionDelete deletes a question and all its options.
func HandleQuestionDelete(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

		if _, ok = requireEditableQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		var questionID int64
		if questionID, ok = handlers.ParseIDFromPath(w, r, logger, "questionID"); !ok {
			return
		}

		// Reject cross-quiz deletes (#339); without this gate an admin
		// who owns quizID could delete a question on a different quiz
		// by mounting it on this URL.
		if _, ok = questionByID(w, r, logger, csrfMgr, quizStore, quizID, questionID); !ok {
			return
		}

		if err := quizStore.DeleteQuestion(r.Context(), questionID); err != nil {
			logger.ErrorContext(r.Context(), "error deleting question", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		// htmx removes the question row in place via an outerHTML swap; a
		// plain form post falls back to the 303 reload of the quiz view.
		if htmx.IsRequest(r) {
			w.WriteHeader(http.StatusOK)

			return
		}

		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
	})
}

// questionSaveCtx is the artefact set loadQuestionForSave returns -
// bundled into a struct so HandleQuestionSave's signature stays under
// revive's function-result-limit and the call site stays readable.
// Round is the resolved target round on the create path (#929), carried
// so a validation-failure re-render can repopulate the form's hidden
// round_id field and breadcrumb; it is nil on the edit path.
type questionSaveCtx struct {
	Quiz     *quiz.Quiz
	Question *quiz.Question
	Round    *quiz.Round
	IsNew    bool
}

// HandleQuestionSave saves a question.
func HandleQuestionSave(
	logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store, mediaStore QuestionMediaStore,
) http.Handler {
	formRenderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/questionform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qctx, ok := loadQuestionForSave(w, r, logger, csrfMgr, quizStore)
		if !ok {
			return
		}

		fieldErrors, ok := fillQuestionFromForm(w, r, logger, csrfMgr, mediaStore, qctx.Question)
		if !ok {
			return
		}
		if len(fieldErrors) > 0 {
			renderQuestionForm(w, r, logger, csrfMgr, formRenderer, mediaStore, qctx, fieldErrors)

			return
		}

		// New questions get their position assigned inside the store's
		// txn-wrapped CreateQuestionAtNextPosition (#352) so the
		// max+1 read can't race with a concurrent insert. The handler
		// just passes the question through; storeQuestion picks the
		// right store method based on qs.ID.
		if !storeQuestion(w, r, logger, csrfMgr, quizStore, qctx.Question) {
			return
		}

		// strconv.FormatInt dodges gosec G710's open-redirect heuristic
		// - the qz.ID came from a request parameter through
		// requireQuizOwner so gosec flags fmt.Sprintf's %d as tainted.
		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(qctx.Quiz.ID, 10), http.StatusSeeOther)
	})
}

// loadQuestionForSave parses the quizID + questionID off the path,
// applies the owner gate, and loads the existing question for an edit
// (or stamps a fresh struct for a create). ok=false when any step
// failed and already wrote a response. Split out so
// HandleQuestionSave's main flow stays under gocognit's threshold
// while the participant + ownership gates remain consolidated.
//

func loadQuestionForSave(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
) (*questionSaveCtx, bool) {
	quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
	if !ok {
		return nil, false
	}
	questionID, ok := handlers.ParseIDFromPath(w, r, logger, "questionID")
	if !ok {
		return nil, false
	}
	qz, ok := requireEditableQuizOwner(w, r, logger, csrfMgr, quizStore, quizID)
	if !ok {
		return nil, false
	}
	if questionID == 0 {
		// A new question takes the round from the form's hidden round_id
		// field, set by the per-round "Add question" button (#929). The
		// round must belong to this quiz; a missing or foreign id 4xxs
		// rather than silently defaulting.
		rnd, roundOK := roundFromForm(w, r, logger, csrfMgr, quizStore, qz.ID)
		if !roundOK {
			return nil, false
		}

		return &questionSaveCtx{
			Quiz:     qz,
			Question: &quiz.Question{QuizID: qz.ID, RoundID: rnd.ID},
			Round:    rnd,
			IsNew:    true,
		}, true
	}
	qs, ok := questionByID(w, r, logger, csrfMgr, quizStore, qz.ID, questionID)
	if !ok {
		return nil, false
	}

	return &questionSaveCtx{Quiz: qz, Question: qs, IsNew: false}, true
}

// roundFromForm reads the round_id POST field and loads the named round,
// gated on it belonging to quizID. A missing, unparseable, or foreign
// round id renders the established 4xx and returns ok=false (#929).
// Mirrors roundFromQuery for the POST path; both go through roundByID so
// a cross-quiz id surfaces as 404, matching HandleQuestionMoveToRound.
func roundFromForm(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	quizID int64,
) (*quiz.Round, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
	if err := r.ParseForm(); err != nil {
		render400(w, r, logger, csrfMgr, "error parsing form")

		return nil, false
	}
	roundID, err := handlers.IDFromString(r.PostFormValue("round_id"))
	if err != nil || roundID == 0 {
		render400(w, r, logger, csrfMgr, "invalid round id")

		return nil, false
	}

	return roundByID(w, r, logger, csrfMgr, quizStore, quizID, roundID)
}

// renderQuestionForm re-renders the question form after a validation
// failure on save. The submitted Question + FieldErrors are preserved
// so the admin can fix the offending fields without re-typing.
func renderQuestionForm(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	renderer *render.Renderer,
	mediaStore QuestionMediaStore,
	qctx *questionSaveCtx,
	fieldErrors map[string]string,
) {
	title := "Admin Dashboard - Question Edit"
	if qctx.IsNew {
		title = "Admin Dashboard - Question Create"
	}
	var roundData *RoundData
	if qctx.Round != nil {
		roundData = roundDataFromRound(qctx.Round)
	}
	// Reload the picker libraries so the re-rendered form still shows the
	// thumbnails and sounds. A 500 here already wrote the response.
	library, audioLibrary, ok := loadQuestionLibrary(w, r, logger, csrfMgr, mediaStore, qctx.Quiz.ID)
	if !ok {
		return
	}
	renderer.Render(w, r, http.StatusBadRequest, questionFormData{
		Title:        title,
		Quiz:         quizDataFromQuiz(qctx.Quiz),
		Question:     questionDataFromQuestion(qctx.Question),
		Round:        roundData,
		Library:      library,
		AudioLibrary: audioLibrary,
		FieldErrors:  fieldErrors,
	})
}
