package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/quiz"
)

// positionFieldKey is the form-error map key the break form uses to
// attach validation messages to the "Insert after" dropdown. Pulled
// out as a constant so the multiple validation paths cannot drift
// (revive add-constant).
const positionFieldKey = "position"

// BreakData backs the interleaved sequence on the quiz view template
// and the standalone break form. Mirrors the QuestionData/QuizData
// shape so the templates stay symmetric with their Questions
// equivalents (#167).
type BreakData struct {
	ID       int64
	QuizID   int64
	Text     string
	Position int
}

func breakDataFromBreak(b *quiz.Break) *BreakData {
	return &BreakData{
		ID:       b.ID,
		QuizID:   b.QuizID,
		Text:     b.Text,
		Position: b.Position,
	}
}

func breakDataFromBreaks(breaks []*quiz.Break) []*BreakData {
	out := make([]*BreakData, 0, len(breaks))
	for _, b := range breaks {
		out = append(out, breakDataFromBreak(b))
	}

	return out
}

// BreakSlotOption is one entry in the "Insert after" dropdown on the
// break form: a position value and the label rendered next to it.
// Position 0 is the "(Beginning)" slot; positive positions name the
// question they appear after.
type BreakSlotOption struct {
	Position int
	Label    string
}

// breakFormData backs breakform.gohtml. FieldErrors is set when
// HandleBreakSave re-renders the form after a validation failure
// (mirrors questionFormData's contract). FormError carries a
// banner-level message — used for slot-collision (#167), which is a
// form-wide condition rather than a per-field issue.
type breakFormData struct {
	Title       string
	Quiz        *QuizData
	Break       *BreakData
	SlotOptions []BreakSlotOption
	FieldErrors map[string]string
	FormError   string
}

// breakSlotMaxTextLen caps the per-question label in the "Insert after"
// dropdown so the option list stays readable even when a quiz has a
// long question. The first few words usually suffice for the admin to
// pick the right slot.
const breakSlotMaxTextLen = 60

// buildSlotOptions translates the quiz's questions into the dropdown
// entries the form renders. The list always starts with the
// "(Beginning)" slot at position 0; every question contributes one
// "Question {n}: {truncated text}" entry keyed by its own position.
//
// The truncation keeps the option list scannable on long-text quizzes.
// Position 0 is intentionally a sibling of the question slots, not a
// hidden default — the admin should be able to add a break before the
// first question without scrolling. Truncation counts runes, not
// bytes, so a multi-byte UTF-8 character cannot be sliced in half.
func buildSlotOptions(questions []*quiz.Question) []BreakSlotOption {
	opts := make([]BreakSlotOption, 0, len(questions)+1)
	opts = append(opts, BreakSlotOption{Position: 0, Label: "(Beginning)"})
	for _, q := range questions {
		text := q.Text
		if utf8.RuneCountInString(text) > breakSlotMaxTextLen {
			text = string([]rune(text)[:breakSlotMaxTextLen]) + "..."
		}
		opts = append(opts, BreakSlotOption{
			Position: q.Position,
			Label:    "Question " + strconv.Itoa(q.Position) + ": " + text,
		})
	}

	return opts
}

// defaultCreateSlot picks the position the create form pre-selects. An
// empty quiz selects (Beginning); otherwise we default to the last
// question's position so the admin can add a break at the end without
// scrolling through a long list.
func defaultCreateSlot(questions []*quiz.Question) int {
	if len(questions) == 0 {
		return 0
	}

	return questions[len(questions)-1].Position
}

// HandleBreakCreate renders the new-break form. Owner-gated so a
// non-creator never sees the editor for a quiz they cannot save.
func HandleBreakCreate(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/breakform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}

		qz, ok := requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID)
		if !ok {
			return
		}

		slots := buildSlotOptions(qz.Questions)
		render.Render(w, r, http.StatusOK, breakFormData{
			Title:       "Admin Dashboard - Break Create",
			Quiz:        quizDataFromQuiz(qz),
			Break:       &BreakData{QuizID: qz.ID, Position: defaultCreateSlot(qz.Questions)},
			SlotOptions: slots,
		})
	})
}

// HandleBreakEdit renders the edit form for an existing break,
// pre-filling Text and the current insertion slot.
func HandleBreakEdit(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/breakform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}
		breakID, ok := handlers.ParseIDFromPath(w, r, logger, "breakID")
		if !ok {
			return
		}

		qz, ok := requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID)
		if !ok {
			return
		}

		b, ok := breakByID(w, r, logger, csrfMgr, quizStore, qz.ID, breakID)
		if !ok {
			return
		}

		render.Render(w, r, http.StatusOK, breakFormData{
			Title:       "Admin Dashboard - Break Edit",
			Quiz:        quizDataFromQuiz(qz),
			Break:       breakDataFromBreak(b),
			SlotOptions: buildSlotOptions(qz.Questions),
		})
	})
}

// HandleBreakSave handles both create (breakID == 0) and update.
// Mirrors HandleQuestionSave's two-mode shape so the route table can
// register the same handler against POST /admin/quizzes/{quizID}/breaks
// and POST /admin/quizzes/{quizID}/breaks/{breakID}.
func HandleBreakSave(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	formRenderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/breakform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bctx, ok := loadBreakForSave(w, r, logger, csrfMgr, quizStore)
		if !ok {
			return
		}

		fieldErrors, ok := fillBreakFromForm(w, r, logger, csrfMgr, bctx.Quiz, bctx.Break)
		if !ok {
			return
		}
		if len(fieldErrors) > 0 {
			renderBreakForm(w, r, formRenderer, bctx, fieldErrors, "")

			return
		}

		if err := storeBreak(r.Context(), quizStore, bctx.Break); err != nil {
			if errors.Is(err, quiz.ErrBreakPositionTaken) {
				renderBreakForm(
					w, r, formRenderer, bctx, nil,
					"A break already exists at that slot - pick a different one.",
				)

				return
			}
			logger.ErrorContext(r.Context(), "error saving break", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		// strconv.FormatInt dodges gosec G710's open-redirect heuristic
		// - bctx.Quiz.ID came from a request parameter so a fmt.Sprintf
		// with %d would taint the redirect path.
		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(bctx.Quiz.ID, 10), http.StatusSeeOther)
	})
}

// HandleBreakMove handles the per-row up/down reorder buttons on the
// quiz view's break rows (#167). Mirrors HandleQuestionMove's contract
// - HX-Request renders just the questions_list partial so the page
// keeps its scroll position, plain POSTs fall back to the 303 redirect.
// The store layer enforces slot eligibility (a break can only land on
// the (Beginning) slot or after a question, never on a slot another
// break already occupies); the template hides the arrow in advance via
// CanMoveUp / CanMoveDown so a successful request is the common case.
func HandleBreakMove(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	render := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizview.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}
		if _, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}
		breakID, ok := handlers.ParseIDFromPath(w, r, logger, "breakID")
		if !ok {
			return
		}
		direction := r.PathValue("direction")
		isHX := r.Header.Get("Hx-Request") == "true"

		// ErrBreakMoveImpossible means the target slot is gone,
		// occupied, or out of range. The arrow should already have
		// been hidden in the UI, so a request reaching here is a
		// stale form or hand-crafted POST - render the unchanged
		// state through the same partial/redirect path as a
		// successful move. Other errors get a real HTTP status.
		switch err := quizStore.MoveBreak(r.Context(), quizID, breakID, direction); {
		case errors.Is(err, quiz.ErrBreakMoveImpossible):
			logger.InfoContext(r.Context(), "break move skipped (target slot unavailable)", slog.Any("err", err))
		case err != nil:
			renderBreakMoveError(w, r, logger, csrfMgr, err)

			return
		default:
			// success - fall through to the partial / redirect path.
		}

		if isHX {
			renderSequencePartial(w, r, logger, csrfMgr, render, quizStore, quizID)

			return
		}

		// strconv.FormatInt dodges gosec G710's open-redirect heuristic
		// - quizID came from a request parameter so a fmt.Sprintf with
		// %d would taint the redirect path.
		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
	})
}

// renderBreakMoveError translates a MoveBreak failure into a 400 /
// 404 / 500 response. The ErrBreakMoveImpossible no-op is handled by
// the caller because it reuses the success-path renderer.
func renderBreakMoveError(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	err error,
) {
	switch {
	case errors.Is(err, quiz.ErrInvalidDirection):
		render400(w, r, logger, csrfMgr, "invalid direction")
	case errors.Is(err, quiz.ErrBreakNotFound):
		render404(w, r, logger, csrfMgr)
	default:
		logger.ErrorContext(r.Context(), "error moving break", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)
	}
}

// HandleBreakDelete removes a break. Owner-gated so only the quiz's
// creator can drop one of its breaks.
func HandleBreakDelete(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}

		if _, ok = requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		breakID, ok := handlers.ParseIDFromPath(w, r, logger, "breakID")
		if !ok {
			return
		}

		// Reject cross-quiz deletes: a creator who owns quizID could
		// otherwise delete a break belonging to a different quiz by
		// mounting the break's id on this URL. Mirrors the question
		// IDOR gate (#339).
		if _, ok = breakByID(w, r, logger, csrfMgr, quizStore, quizID, breakID); !ok {
			return
		}

		if err := quizStore.DeleteBreak(r.Context(), breakID); err != nil {
			if errors.Is(err, quiz.ErrDeletingBreakNoRowsAffected) {
				render404(w, r, logger, csrfMgr)

				return
			}
			logger.ErrorContext(r.Context(), "error deleting break", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
	})
}

// breakSaveCtx is the artefact loadBreakForSave returns. Bundled in a
// struct so HandleBreakSave's signature stays under revive's
// function-result-limit, matching the question equivalent.
type breakSaveCtx struct {
	Quiz  *quiz.Quiz
	Break *quiz.Break
	IsNew bool
}

// loadBreakForSave mirrors loadQuestionForSave structurally; dupl
// flags the overlap, but a shared helper would have to abstract over
// two different domain types and lose its readability. The two are
// allowed to drift independently.
//
//nolint:dupl // mirrors loadQuestionForSave; abstracting both behind generics or a domain-shaped interface would obscure the per-entity sentinel-error wiring without removing any logic.
func loadBreakForSave(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
) (*breakSaveCtx, bool) {
	quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
	if !ok {
		return nil, false
	}
	breakID, ok := handlers.ParseIDFromPath(w, r, logger, "breakID")
	if !ok {
		return nil, false
	}
	qz, ok := requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID)
	if !ok {
		return nil, false
	}
	if breakID == 0 {
		return &breakSaveCtx{Quiz: qz, Break: &quiz.Break{QuizID: qz.ID}, IsNew: true}, true
	}
	b, ok := breakByID(w, r, logger, csrfMgr, quizStore, qz.ID, breakID)
	if !ok {
		return nil, false
	}

	return &breakSaveCtx{Quiz: qz, Break: b, IsNew: false}, true
}

func renderBreakForm(
	w http.ResponseWriter,
	r *http.Request,
	renderer *TemplateRenderer,
	bctx *breakSaveCtx,
	fieldErrors map[string]string,
	formError string,
) {
	title := "Admin Dashboard - Break Edit"
	if bctx.IsNew {
		title = "Admin Dashboard - Break Create"
	}
	status := http.StatusBadRequest
	if formError != "" && len(fieldErrors) == 0 {
		status = http.StatusConflict
	}
	renderer.Render(w, r, status, breakFormData{
		Title:       title,
		Quiz:        quizDataFromQuiz(bctx.Quiz),
		Break:       breakDataFromBreak(bctx.Break),
		SlotOptions: buildSlotOptions(bctx.Quiz.Questions),
		FieldErrors: fieldErrors,
		FormError:   formError,
	})
}

// fillBreakFromForm reads the form into the supplied break struct.
// Mirrors fillQuestionFromForm's contract: a parse error renders a 400
// and returns (nil, false); a validation error returns a non-empty map
// + true; success returns (nil, true). The supplied quiz is used to
// validate the "Insert after" position against the quiz's questions
// (#167).
func fillBreakFromForm(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	qz *quiz.Quiz,
	b *quiz.Break,
) (map[string]string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
	if err := r.ParseForm(); err != nil {
		msg := "error parsing form"
		logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
		render400(w, r, logger, csrfMgr, msg)

		return nil, false
	}
	b.Text = r.PostFormValue("text")

	// Position 0 is a valid value (the "before-first-question" slot),
	// so the form always submits a numeric position; a missing or
	// unparseable value is a programmer error that surfaces as the
	// "invalid position" field message rather than silently defaulting.
	posRaw := r.PostFormValue("position")
	pos, parseErr := strconv.Atoi(posRaw)
	if parseErr != nil {
		return map[string]string{positionFieldKey: "Pick a slot to insert the break at."}, true
	}
	b.Position = pos

	problems := (&breakForm{quiz: qz, brk: b}).Valid(r.Context())
	if len(problems) > 0 {
		return problems, true
	}

	return nil, true
}

func storeBreak(
	ctx context.Context,
	quizStore quiz.Store,
	b *quiz.Break,
) error {
	if b.ID == 0 {
		if err := quizStore.CreateBreak(ctx, b); err != nil {
			return fmt.Errorf("create break: %w", err)
		}

		return nil
	}
	if err := quizStore.UpdateBreak(ctx, b); err != nil {
		return fmt.Errorf("update break: %w", err)
	}

	return nil
}

// breakByID loads a break and gates it on the URL-scoped quizID. A
// mismatch renders as 404 (not 403) so the route never leaks "this
// break exists on another quiz" - same IDOR rationale as
// questionByID.
func breakByID(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	quizID, breakID int64,
) (*quiz.Break, bool) {
	b, err := quizStore.GetBreak(r.Context(), breakID)
	if err != nil {
		if errors.Is(err, quiz.ErrBreakNotFound) {
			logger.InfoContext(r.Context(), "break not found", slog.Any("err", err))
			render404(w, r, logger, csrfMgr)

			return nil, false
		}
		logger.ErrorContext(r.Context(), "error fetching break", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return nil, false
	}

	if b.QuizID != quizID {
		logger.InfoContext(
			r.Context(),
			"break belongs to a different quiz",
			slog.Int64("break_id", breakID),
			slog.Int64("break_quiz_id", b.QuizID),
			slog.Int64("url_quiz_id", quizID),
		)
		render404(w, r, logger, csrfMgr)

		return nil, false
	}

	return b, true
}

// breakForm is the validation hook for the admin break form. The
// position field has to match either the "(Beginning)" slot (0) or the
// position of one of the quiz's questions; anything else means a stale
// dropdown (question deleted between form load and submit) or a
// hand-crafted POST.
type breakForm struct {
	quiz *quiz.Quiz
	brk  *quiz.Break
}

// Valid checks every form-level rule on the wrapped break. An empty
// map means the form is valid.
func (f *breakForm) Valid(_ context.Context) map[string]string {
	problems := map[string]string{}
	if f.brk.Position < 0 {
		problems[positionFieldKey] = "Pick a slot to insert the break at."

		return problems
	}
	if f.brk.Position == 0 {
		return problems
	}
	for _, q := range f.quiz.Questions {
		if q.Position == f.brk.Position {
			return problems
		}
	}
	problems[positionFieldKey] = "That question no longer exists - pick a different slot."

	return problems
}
