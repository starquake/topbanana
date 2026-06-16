// Package admin contains handlers for the admin dashboard
package admin

import (
	"context"
	"encoding/json"
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

// navSection maps a request path to the admin nav section it belongs to,
// so the shared top bar can mark the active section. The empty string
// means the overview at /admin (no inline link is active).
func navSection(path string) string {
	switch {
	case strings.HasPrefix(path, "/admin/quizzes"):
		return "quizzes"
	case strings.HasPrefix(path, "/admin/players"):
		return "players"
	case strings.HasPrefix(path, "/admin/invites"):
		return "invites"
	case strings.HasPrefix(path, "/admin/email"):
		return "email"
	case strings.HasPrefix(path, "/admin/settings"):
		return "settings"
	default:
		return ""
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
	// MediaID is the id of the attached library image, or 0 when none is
	// attached (#937). The picker pre-checks the radio whose value equals
	// it; 0 leaves the "None" radio checked.
	MediaID               int64
	Position              int
	TimeLimitSecondsValue string
	Options               []*OptionData
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

// canEditQuiz is the single source of truth for the creator-or-Admin edit rule
// (#281/#538): the session player must be present and must either be the quiz's
// creator OR an Admin (who may edit, delete, and reset scores on any quiz). A
// Host is NOT granted rights over another Host's games - own-game checks still
// go through createdByPlayerID. Both [attachCanEdit] (read paths) and
// [requireQuizOwner] (mutating paths) call this so the policy lives in one
// place.
func canEditQuiz(r *http.Request, createdByPlayerID int64) bool {
	p, ok := auth.PlayerFromContext(r.Context())
	if !ok {
		return false
	}

	return p.IsAdmin() || p.ID == createdByPlayerID
}

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
	if q.MediaID != nil {
		mediaID = *q.MediaID
	}

	return &QuestionData{
		ID:                    q.ID,
		QuizID:                q.QuizID,
		RoundID:               q.RoundID,
		Text:                  q.Text,
		MediaID:               mediaID,
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

// resolveQuestionMedia interprets the optional media_id picker input (#937).
// Blank or "0" -> (nil, "") meaning "no image attached" (NULL). A non-empty
// value must parse and must name an image in quizID's own library; a missing,
// foreign, or unparseable id yields a field-error message so the save handler
// re-renders the form rather than persisting a cross-quiz reference. A store
// failure also surfaces as a message so the caller never silently drops the
// attachment.
func resolveQuestionMedia(
	ctx context.Context, mediaStore QuestionMediaStore, quizID int64, raw string,
) (*int64, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" {
		return nil, ""
	}
	id, err := handlers.IDFromString(raw)
	if err != nil || id == 0 || mediaStore == nil {
		return nil, errMediaPickInvalid
	}
	m, err := mediaStore.GetMedia(ctx, id)
	if err != nil {
		if errors.Is(err, media.ErrMediaNotFound) {
			return nil, errMediaNotInLibrary
		}

		return nil, errMediaVerifyFailed
	}
	if m.QuizID != quizID {
		return nil, errMediaNotInLibrary
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
	// Image picker (#937). An empty/absent media_id means "no image"
	// (NULL); a non-empty value must name an image in this question's own
	// quiz library, validated below.
	mediaID, mediaErr := resolveQuestionMedia(r.Context(), mediaStore, qs.QuizID, r.PostFormValue("media_id"))
	if mediaErr != "" {
		return map[string]string{"media": mediaErr}, true
	}
	qs.MediaID = mediaID
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

		images, ok := loadQuizMedia(w, r, logger, csrfMgr, mediaLister, id)
		if !ok {
			return
		}

		quizData := quizDataFromQuiz(qz)
		attachCanEdit(r, quizData)
		data := newQuizViewData(quizData, players, rounds)
		data.Images = images
		data.HostHasRunningGame = hostHasRunningGame(r, logger, runningGames)
		data.UploadedCount, data.FailedCount = parseUploadCounts(r)
		renderer.Render(w, r, http.StatusOK, data)
	})
}

// uploadCountCeiling clamps the banner counts so a tampered URL can't paint
// an outrageous number (#951).
const uploadCountCeiling = 100

// parseUploadCounts pulls the post-upload banner counts out of the URL query.
// Both default to 0 and are clamped to uploadCountCeiling; a non-numeric or
// negative value is treated as 0 so a tampered query cannot paint a banner
// with a misleading number.
func parseUploadCounts(r *http.Request) (uploaded, failed int) {
	return parseUploadCount(r, "uploaded"), parseUploadCount(r, "failed")
}

func parseUploadCount(r *http.Request, name string) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	if n > uploadCountCeiling {
		return uploadCountCeiling
	}

	return n
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
	// Images is the quiz's media library, newest first, for the thumbnail
	// grid (#936 slice 3). The upload control and grid are gated on CanEdit
	// in the template; the data loads regardless so an owner sees their
	// library.
	Images []MediaCardData
	// HostHasRunningGame gates the "Host live" confirm-and-restart prompt
	// (#853): true when the signed-in host already has a game in flight, so the
	// control opens a modal that ends the running session before hosting this
	// quiz instead of submitting straight away.
	HostHasRunningGame bool
	// UploadedCount and FailedCount drive the post-upload banner. The upload
	// handler redirects with ?uploaded=N&failed=M (#951) so the page can show
	// what just happened without a session-flash mechanism. Both are 0 on a
	// plain visit; clamped to a small ceiling so a tampered query string can
	// not paint a misleading number.
	UploadedCount int
	FailedCount   int
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
}

func mediaCardDataFromMedia(items []*media.Media) []MediaCardData {
	cards := make([]MediaCardData, 0, len(items))
	for _, m := range items {
		cards = append(cards, MediaCardData{
			ID:     m.ID,
			Width:  m.Width,
			Height: m.Height,
		})
	}

	return cards
}

// loadQuizMedia fetches the quiz's image library, newest first. A nil lister
// (callers that do not wire the media store) yields an empty grid rather than a
// failure. A lookup error is a 500: the library is part of the same admin view
// that already loaded the quiz tree, so hiding the failure behind an empty grid
// would mask it.
func loadQuizMedia(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	mediaLister MediaLister,
	quizID int64,
) ([]MediaCardData, bool) {
	if mediaLister == nil {
		return nil, true
	}
	items, err := mediaLister.ListMediaByQuiz(r.Context(), quizID)
	if err != nil {
		logger.ErrorContext(r.Context(), "error listing media for quiz view", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return nil, false
	}

	return mediaCardDataFromMedia(items), true
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

// quizImportPayload mirrors the JSON shape an admin pastes into the import
// textarea. Decoupled from quiz.Quiz so the wire shape stays small and
// LLM-friendly (no IDs, timestamps, position fields, or slugs - the slug
// is derived server-side from the title). The handler translates this
// into the full domain model before validation.
type quizImportPayload struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	// TimeLimitSeconds is the per-quiz default answer window (#99).
	// Optional in the payload - omitted maps to
	// [quiz.DefaultTimeLimitSeconds], matching the admin form's
	// new-quiz default.
	TimeLimitSeconds *int `json:"timeLimitSeconds,omitempty"`
	// Questions and Rounds are mutually exclusive (#546). Supply
	// Questions for a flat quiz (every question lands in the default
	// round, the original behaviour) or Rounds to author named rounds
	// with their own questions - never both, never neither.
	Questions []quizImportQuestionPayload `json:"questions,omitempty"`
	Rounds    []quizImportRoundPayload    `json:"rounds,omitempty"`
}

type quizImportRoundPayload struct {
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
	// BoundaryDurationSeconds overrides the quiz default for this round's
	// boundary auto-advance window (#554). Optional - omitted means
	// "inherit the quiz value at game time", same as leaving the admin
	// form's field blank.
	BoundaryDurationSeconds *int `json:"boundaryDurationSeconds,omitempty"`
	// Questions for this round, in play order. Required and non-empty;
	// quiz-wide positions are assigned 1..N across all rounds (#546).
	Questions []quizImportQuestionPayload `json:"questions"`
}

type quizImportQuestionPayload struct {
	Text string `json:"text"`
	// TimeLimitSeconds overrides the quiz default for this question
	// (#99). Optional - omitted means "inherit the quiz value at
	// game time", same as leaving the admin form's field blank.
	TimeLimitSeconds *int                      `json:"timeLimitSeconds,omitempty"`
	Options          []quizImportOptionPayload `json:"options"`
}

type quizImportOptionPayload struct {
	Text    string `json:"text"`
	Correct bool   `json:"correct"`
}

// quizImportExample is the JSON block rendered on the import page so the
// admin can copy it into a chat with Claude (or any LLM), have it generate
// a quiz, and paste the result back. Kept here as a const string rather
// than in the template so the rendered example stays byte-identical to
// what the handler will actually accept.
const quizImportExample = `{
  "title": "European Capitals",
  "description": "A quick tour of EU capitals.",
  "timeLimitSeconds": 10,
  "rounds": [
    {
      "title": "Warm-up",
      "summary": "An easy start before things speed up.",
      "questions": [
        {
          "text": "Which city sits on the river Vltava?",
          "options": [
            { "text": "Bratislava", "correct": false },
            { "text": "Budapest",   "correct": false },
            { "text": "Prague",     "correct": true  },
            { "text": "Warsaw",     "correct": false }
          ]
        },
        {
          "text": "Which of these is a capital city?",
          "options": [
            { "text": "Lisbon",   "correct": true  },
            { "text": "Porto",    "correct": false },
            { "text": "Helsinki", "correct": true  },
            { "text": "Tampere",  "correct": false }
          ]
        }
      ]
    },
    {
      "title": "Final stretch",
      "summary": "One harder question to finish.",
      "boundaryDurationSeconds": 15,
      "questions": [
        {
          "text": "Which capital is furthest north?",
          "timeLimitSeconds": 20,
          "options": [
            { "text": "Reykjavik",  "correct": true  },
            { "text": "Oslo",       "correct": false },
            { "text": "Stockholm",  "correct": false },
            { "text": "Copenhagen", "correct": false }
          ]
        }
      ]
    }
  ]
}`

// quizImportPageData is the render-time data for quizimport.gohtml. Both
// the form (GET) and save (POST) handlers populate it, so the type is
// declared once at package scope rather than re-declared per handler.
//
// Mode holds the play mode the admin picked; it has no default so the
// selector forces an explicit choice (#752). ModeOptions feeds the
// selector with the recognised play modes.
type quizImportPageData struct {
	Title       string
	JSON        string
	Example     string
	Error       string
	Mode        string
	ModeOptions []string
}

// HandleQuizImportForm renders the JSON-import page. The textarea is empty
// on a fresh GET; the POST handler re-renders this template with the
// submitted JSON intact when validation fails.
func HandleQuizImportForm(logger *slog.Logger, csrfMgr *csrf.Manager) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizimport.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		renderer.Render(w, r, http.StatusOK, quizImportPageData{
			Title:       "Admin Dashboard - Import Quiz",
			Example:     quizImportExample,
			ModeOptions: quiz.ModeValues(),
		})
	})
}

// HandleQuizImportSave parses the JSON pasted into the import form, builds
// a fresh quiz.Quiz from it, and persists via the existing store path so
// the resulting row is indistinguishable from one created via the regular
// quiz form. Validation errors re-render the form with the submitted JSON
// preserved so the admin can fix the payload without re-pasting.
func HandleQuizImportSave(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizimport.gohtml")

	renderStatus := func(w http.ResponseWriter, r *http.Request, status int, jsonText, mode, msg string) {
		renderer.Render(w, r, status, quizImportPageData{
			Title:       "Admin Dashboard - Import Quiz",
			JSON:        jsonText,
			Example:     quizImportExample,
			Error:       msg,
			Mode:        mode,
			ModeOptions: quiz.ModeValues(),
		})
	}
	renderErr := func(w http.ResponseWriter, r *http.Request, jsonText, mode, msg string) {
		renderStatus(w, r, http.StatusBadRequest, jsonText, mode, msg)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parsed, ok := parseImportPayload(w, r, logger, renderErr)
		if !ok {
			return
		}

		// Stamp the session admin as the creator so the downstream
		// owner-gated mutating routes can match (#281).
		if p, present := auth.PlayerFromContext(r.Context()); present {
			parsed.Quiz.CreatedByPlayerID = p.ID
		}

		if err := storeQuiz(r.Context(), quizStore, parsed.Quiz); err != nil {
			if errors.Is(err, quiz.ErrSlugTaken) {
				// Same slug-derivation rule applies on the import path
				// (#293): re-render at 409 with the JSON intact so the
				// admin can rename and resubmit without re-pasting.
				renderStatus(
					w, r, http.StatusConflict, parsed.JSONText, parsed.Quiz.Mode,
					"A quiz with this title already exists - change the title in the JSON and resubmit.",
				)

				return
			}
			logger.ErrorContext(r.Context(), "error storing imported quiz", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/quizzes/%d", parsed.Quiz.ID), http.StatusSeeOther)
	})
}

// parsedImport holds the decoded + validated payload [parseImportPayload]
// returns to [HandleQuizImportSave]. Bundled so the parser can return a
// single struct (plus an ok flag) and stay under revive's
// function-result-limit while still surfacing the JSON text (for
// re-render on later failures) alongside the parsed quiz.
type parsedImport struct {
	JSONText string
	Quiz     *quiz.Quiz
}

// parseImportPayload reads + decodes + validates the request body for
// [HandleQuizImportSave]. On any failure it writes the form-rendered
// error response via renderErr and returns ok=false; the caller
// early-returns. Split out so [HandleQuizImportSave] stays under
// revive's function-length and gocognit limits.
func parseImportPayload(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger,
	renderErr func(http.ResponseWriter, *http.Request, string, string, string),
) (parsedImport, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
	if err := r.ParseForm(); err != nil {
		logger.ErrorContext(r.Context(), "error parsing import form", slog.Any("err", err))
		renderErr(w, r, "", "", "request body too large or malformed")

		return parsedImport{}, false
	}

	// Play mode has no default on the import form (#752): the admin must
	// pick solo or live before the import proceeds. Reject a missing or
	// unrecognised value here rather than silently defaulting to solo the
	// way the regular quiz form does.
	mode := r.PostFormValue("mode")
	jsonText := r.PostFormValue("json")
	if !quiz.IsValidMode(mode) {
		renderErr(w, r, stripCodeFences(jsonText), mode, "choose a play mode (solo or live) before importing")

		return parsedImport{}, false
	}

	if jsonText == "" {
		renderErr(w, r, "", mode, "json field is required")

		return parsedImport{}, false
	}
	// The prompt asks the LLM to return the JSON in a ```json code block, so
	// tolerate a pasted block by stripping the surrounding fences before decode.
	jsonText = stripCodeFences(jsonText)

	var payload quizImportPayload
	dec := json.NewDecoder(strings.NewReader(jsonText))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		renderErr(w, r, jsonText, mode, fmt.Sprintf("invalid JSON: %v", err))

		return parsedImport{}, false
	}

	qz, err := quizFromImportPayload(payload)
	if err != nil {
		renderErr(w, r, jsonText, mode, fmt.Sprintf("validation errors: %v", err))

		return parsedImport{}, false
	}
	qz.Mode = mode
	if problems := (&quizForm{quiz: qz}).Valid(r.Context()); len(problems) > 0 {
		renderErr(w, r, jsonText, mode, fmt.Sprintf("validation errors: %v", problems))

		return parsedImport{}, false
	}

	return parsedImport{JSONText: jsonText, Quiz: qz}, true
}

// stripCodeFences removes a single surrounding Markdown fenced code block
// (```...``` or ```json...```) from s, so JSON pasted straight from an LLM's
// code block imports cleanly. It returns s unchanged when it is not fenced.
func stripCodeFences(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return s
	}
	nl := strings.IndexByte(t, '\n')
	if nl < 0 {
		return s
	}
	t = t[nl+1:]
	if i := strings.LastIndex(t, "```"); i >= 0 {
		t = t[:i]
	}

	return strings.TrimSpace(t)
}

var (
	// errImportQuestionsOrRounds is returned when the payload supplies
	// both a top-level questions[] and rounds[], or neither. The two are
	// mutually exclusive (#546): one flat list or named rounds, not a mix.
	errImportQuestionsOrRounds = errors.New(
		"provide either a top-level questions array or a rounds array, not both and not neither",
	)
	// errImportRoundTitleRequired is returned when an imported round
	// carries no title (#546).
	errImportRoundTitleRequired = errors.New("title is required")
	// errImportRoundNoQuestions is returned when an imported round
	// carries no questions (#546).
	errImportRoundNoQuestions = errors.New("at least one question is required")
)

// quizFromImportPayload converts the wire-shape payload into the domain
// model. The slug is always derived from the title - the payload doesn't
// carry one because LLMs are bad at picking a stable slug and the admin
// form does the same derivation. Question positions are assigned 1..N in
// payload order across all rounds.
//
// When the payload carries rounds[], the rounds are mapped onto
// Quiz.Rounds (each with its own questions) and the same questions are
// also flattened onto Quiz.Questions so the shared quizForm.Valid runs
// every per-question rule. With a top-level questions[] instead, the
// store drops everything in the quiz's default round, the original
// behaviour (#546).
func quizFromImportPayload(p quizImportPayload) (*quiz.Quiz, error) {
	if (len(p.Questions) == 0) == (len(p.Rounds) == 0) {
		return nil, errImportQuestionsOrRounds
	}

	// #99: honour the payload's per-quiz default when present; fall
	// back to the project value so authors who don't care can omit
	// the field entirely and still pass Quiz.Valid's range check.
	timeLimit := quiz.DefaultTimeLimitSeconds
	if p.TimeLimitSeconds != nil {
		timeLimit = *p.TimeLimitSeconds
	}
	qz := &quiz.Quiz{
		Title:            p.Title,
		Slug:             slug.Make(p.Title),
		Description:      p.Description,
		TimeLimitSeconds: timeLimit,
	}

	if len(p.Rounds) > 0 {
		if err := fillQuizFromRounds(qz, p.Rounds); err != nil {
			return nil, err
		}

		return qz, nil
	}

	qz.Questions = make([]*quiz.Question, 0, len(p.Questions))
	pos := 0
	for _, qIn := range p.Questions {
		pos++
		qz.Questions = append(qz.Questions, questionFromImportPayload(qIn, pos))
	}

	return qz, nil
}

// fillQuizFromRounds maps the authored rounds onto qz.Rounds and mirrors
// every question onto qz.Questions with a quiz-wide 1..N position in
// payload order, so the shared quizForm.Valid sees the full question set
// (#546). A round must carry a non-empty title and at least one question.
func fillQuizFromRounds(qz *quiz.Quiz, rounds []quizImportRoundPayload) error {
	qz.Rounds = make([]*quiz.Round, 0, len(rounds))
	pos := 0
	for i, rIn := range rounds {
		if rIn.Title == "" {
			return fmt.Errorf("round %d: %w", i+1, errImportRoundTitleRequired)
		}
		if len(rIn.Questions) == 0 {
			return fmt.Errorf("round %q: %w", rIn.Title, errImportRoundNoQuestions)
		}

		round := &quiz.Round{
			Position: i,
			Title:    rIn.Title,
			Summary:  rIn.Summary,
			// nil -> "inherit the quiz default", the same semantics the
			// admin form's blank input carries (#554).
			BoundaryDurationSeconds: rIn.BoundaryDurationSeconds,
			Questions:               make([]*quiz.Question, 0, len(rIn.Questions)),
		}
		for _, qIn := range rIn.Questions {
			pos++
			qs := questionFromImportPayload(qIn, pos)
			round.Questions = append(round.Questions, qs)
			qz.Questions = append(qz.Questions, qs)
		}
		qz.Rounds = append(qz.Rounds, round)
	}

	return nil
}

// questionFromImportPayload maps one import question onto the domain
// type at the given quiz-wide position. Imported questions never carry an
// image: the JSON import cannot reference uploaded media (which is keyed by a
// per-quiz media id, not a URL), so MediaID stays nil (#937).
func questionFromImportPayload(qIn quizImportQuestionPayload, position int) *quiz.Question {
	qs := &quiz.Question{
		Text:     qIn.Text,
		Position: position,
		// nil -> "inherit the quiz default", the same semantics
		// the admin form's blank input carries (#99).
		TimeLimitSeconds: qIn.TimeLimitSeconds,
	}
	qs.Options = make([]*quiz.Option, 0, len(qIn.Options))
	for _, oIn := range qIn.Options {
		qs.Options = append(qs.Options, &quiz.Option{
			Text:    oIn.Text,
			Correct: oIn.Correct,
		})
	}

	return qs
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
			// UPDATE: only the creator may save. requireQuizOwner
			// loads the quiz and 403s anyone else (#281).
			if qz, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
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
	Library     []MediaCardData
	FieldErrors map[string]string
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

		library, ok := loadQuestionLibrary(w, r, logger, csrfMgr, mediaStore, quizID)
		if !ok {
			return
		}

		renderer.Render(w, r, http.StatusOK, questionFormData{
			Title:    "Admin Dashboard - Question Create",
			Quiz:     quizDataFromQuiz(qz),
			Question: &QuestionData{},
			Round:    roundDataFromRound(rnd),
			Library:  library,
		})
	})
}

// loadQuestionLibrary fetches the quiz's image library for the question
// editor's picker (#937), newest first. A nil store (callers that do not wire
// media) yields an empty picker rather than a failure; a lookup error is a 500,
// matching loadQuizMedia, since the library is part of the same editor page.
func loadQuestionLibrary(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	mediaStore QuestionMediaStore,
	quizID int64,
) ([]MediaCardData, bool) {
	if mediaStore == nil {
		return nil, true
	}
	items, err := mediaStore.ListMediaByQuiz(r.Context(), quizID)
	if err != nil {
		logger.ErrorContext(r.Context(), "error listing media for question editor", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return nil, false
	}

	return mediaCardDataFromMedia(items), true
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

		library, ok := loadQuestionLibrary(w, r, logger, csrfMgr, mediaStore, quizID)
		if !ok {
			return
		}

		renderer.Render(w, r, http.StatusOK, questionFormData{
			Title:    "Admin Dashboard - Question Edit",
			Quiz:     quizDataFromQuiz(qz),
			Question: questionDataFromQuestion(qs),
			Library:  library,
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

		if _, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
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

// HandleQuizDelete deletes a quiz and all its questions and options.
func HandleQuizDelete(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool

		var quizID int64
		if quizID, ok = handlers.ParseIDFromPath(w, r, logger, "quizID"); !ok {
			return
		}

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

		if _, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
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

		if _, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
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
	qz, ok := requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID)
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
	// Reload the picker library so the re-rendered form still shows the
	// thumbnails. A 500 here already wrote the response.
	library, ok := loadQuestionLibrary(w, r, logger, csrfMgr, mediaStore, qctx.Quiz.ID)
	if !ok {
		return
	}
	renderer.Render(w, r, http.StatusBadRequest, questionFormData{
		Title:       title,
		Quiz:        quizDataFromQuiz(qctx.Quiz),
		Question:    questionDataFromQuestion(qctx.Question),
		Round:       roundData,
		Library:     library,
		FieldErrors: fieldErrors,
	})
}
