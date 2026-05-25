package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/quiz"
)

// BreakData backs the Breaks section on the quiz view template and
// the standalone break form. Mirrors the QuestionData/QuizData shape
// so the templates stay symmetric with their Questions equivalents
// (#167).
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

// breakFormData backs breakform.gohtml. FieldErrors is set when
// HandleBreakSave re-renders the form after a validation failure
// (mirrors questionFormData's contract). Text is the only
// admin-authored field in slice 1, but FieldErrors stays a map so
// future slices can attach errors to additional inputs without
// reshaping the template wiring.
type breakFormData struct {
	Title       string
	Quiz        *QuizData
	Break       *BreakData
	FieldErrors map[string]string
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

		render.Render(w, r, http.StatusOK, breakFormData{
			Title: "Admin Dashboard - Break Create",
			Quiz:  quizDataFromQuiz(qz),
			Break: &BreakData{QuizID: qz.ID},
		})
	})
}

// HandleBreakEdit renders the edit form for an existing break,
// pre-filling Text.
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
			Title: "Admin Dashboard - Break Edit",
			Quiz:  quizDataFromQuiz(qz),
			Break: breakDataFromBreak(b),
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

		fieldErrors, ok := fillBreakFromForm(w, r, logger, csrfMgr, bctx.Break)
		if !ok {
			return
		}
		if len(fieldErrors) > 0 {
			renderBreakForm(w, r, formRenderer, bctx, fieldErrors)

			return
		}

		if !storeBreak(w, r, logger, csrfMgr, quizStore, bctx.Break) {
			return
		}

		// strconv.FormatInt dodges gosec G710's open-redirect heuristic
		// — bctx.Quiz.ID came from a request parameter so a fmt.Sprintf
		// with %d would taint the redirect path.
		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(bctx.Quiz.ID, 10), http.StatusSeeOther)
	})
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
) {
	title := "Admin Dashboard - Break Edit"
	if bctx.IsNew {
		title = "Admin Dashboard - Break Create"
	}
	renderer.Render(w, r, http.StatusBadRequest, breakFormData{
		Title:       title,
		Quiz:        quizDataFromQuiz(bctx.Quiz),
		Break:       breakDataFromBreak(bctx.Break),
		FieldErrors: fieldErrors,
	})
}

// fillBreakFromForm reads the form into the supplied break struct.
// Mirrors fillQuestionFromForm's contract: a parse error renders a 400
// and returns (nil, false); a validation error returns a non-empty map
// + true; success returns (nil, true). Text is optional per #167 so
// the form has nothing to validate in slice 1, but the function still
// returns the map so future fields can be added without rewiring the
// caller.
func fillBreakFromForm(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
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

	problems := (&breakForm{}).Valid(r.Context())
	if len(problems) > 0 {
		return problems, true
	}

	return nil, true
}

func storeBreak(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	b *quiz.Break,
) bool {
	if b.ID == 0 {
		if err := quizStore.CreateBreakAtNextPosition(r.Context(), b); err != nil {
			logger.ErrorContext(r.Context(), "error creating break", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return false
		}

		return true
	}
	if err := quizStore.UpdateBreak(r.Context(), b); err != nil {
		logger.ErrorContext(r.Context(), "error updating break", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return false
	}

	return true
}

// breakByID loads a break and gates it on the URL-scoped quizID. A
// mismatch renders as 404 (not 403) so the route never leaks "this
// break exists on another quiz" — same IDOR rationale as
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

// breakForm is the validation hook for the admin break form. Text is
// optional per #167 so Valid is a no-op today; the type stays so
// future slices (image_url etc.) get a single place to wire field-
// level rules without reshaping the caller. The wrapped break will
// move onto the struct when there's something to validate against.
type breakForm struct{}

// Valid checks every form-level rule on the wrapped break. An empty
// map means the form is valid.
func (*breakForm) Valid(_ context.Context) map[string]string {
	return map[string]string{}
}
