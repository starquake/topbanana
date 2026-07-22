package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/htmx"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/render"
)

// roundFormData backs roundform.gohtml. FieldErrors is set when
// HandleRoundSave re-renders the form after a validation failure;
// FormError carries a banner-level message (currently the
// position-collision conflict, which a host should never trigger from
// the UI because positions are auto-assigned).
type roundFormData struct {
	Title       string
	Quiz        *QuizData
	Round       *RoundData
	FieldErrors map[string]string
	FormError   string
	// InEditor makes the form post through htmx into the editor pane instead
	// of navigating, and swaps Cancel for a Discard that stays put (#1244).
	InEditor bool
}

// HandleRoundCreate renders the new-round form. Owner-gated so a
// non-creator never sees the editor for a quiz they cannot save.
func HandleRoundCreate(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/roundform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}

		qz, ok := requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID)
		if !ok {
			return
		}

		renderer.Render(w, r, http.StatusOK, roundFormData{
			Title: "Admin Dashboard - Round Create",
			Quiz:  quizDataFromQuiz(qz),
			Round: &RoundData{QuizID: qz.ID},
		})
	})
}

// HandleRoundEdit renders the edit form for an existing round,
// pre-filling its title and round-summary text.
func HandleRoundEdit(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/roundform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}
		roundID, ok := handlers.ParseIDFromPath(w, r, logger, "roundID")
		if !ok {
			return
		}

		qz, ok := requireQuizOwner(w, r, logger, csrfMgr, quizStore, quizID)
		if !ok {
			return
		}

		g, ok := roundByID(w, r, logger, csrfMgr, quizStore, qz.ID, roundID)
		if !ok {
			return
		}

		data := roundFormData{
			Title: "Admin Dashboard - Round Edit",
			Quiz:  quizDataFromQuiz(qz),
			Round: roundDataFromRound(g),
		}

		// The editor pane asks for the form alone; a direct visit still gets
		// the full page, which is also the no-JS path.
		if htmx.IsRequest(r) {
			data.InEditor = true
			renderer.RenderPartial(w, r, "round_form", data)

			return
		}

		renderer.Render(w, r, http.StatusOK, data)
	})
}

// HandleRoundSave handles both create (roundID == 0) and update.
// Mirrors HandleQuestionSave's two-mode shape so the route table can
// register the same handler against POST /admin/quizzes/{quizID}/rounds
// and POST /admin/quizzes/{quizID}/rounds/{roundID}.
func HandleRoundSave(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	formRenderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/roundform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gctx, ok := loadRoundForSave(w, r, logger, csrfMgr, quizStore)
		if !ok {
			return
		}

		fieldErrors, ok := fillRoundFromForm(w, r, logger, csrfMgr, gctx.Round)
		if !ok {
			return
		}
		if len(fieldErrors) > 0 {
			renderRoundForm(w, r, formRenderer, gctx, fieldErrors, "")

			return
		}

		if err := storeRound(r.Context(), quizStore, gctx.Round); err != nil {
			if errors.Is(err, quiz.ErrRoundPositionTaken) {
				renderRoundForm(
					w, r, formRenderer, gctx, nil,
					"A round already occupies that slot - try again.",
				)

				return
			}
			logger.ErrorContext(r.Context(), "error saving round", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		// In the editor the save stays on the page: the form re-renders in the
		// pane and the round's header follows out of band. Only the header, not
		// the whole round section - that would replace the question list and
		// rebuild its SortableJS instance mid-session.
		if htmx.IsRequest(r) {
			formRenderer.RenderPartials(w, r,
				render.Fragment{Name: "round_form", Data: roundFormData{
					Title:    "Admin Dashboard - Round Edit",
					Quiz:     quizDataFromQuiz(gctx.Quiz),
					Round:    roundDataFromRound(gctx.Round),
					InEditor: true,
				}},
				render.Fragment{Name: "round_head", Data: RoundHeadData{
					Round:    roundDataFromRound(gctx.Round),
					QuizID:   gctx.Quiz.ID,
					CanEdit:  true,
					InEditor: true,
					OOB:      true,
				}},
			)

			return
		}

		// strconv.FormatInt dodges gosec G710's open-redirect heuristic
		// - gctx.Quiz.ID came from a request parameter so a fmt.Sprintf
		// with %d would taint the redirect path.
		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(gctx.Quiz.ID, 10), http.StatusSeeOther)
	})
}

// HandleRoundMove handles the per-round up/down reorder buttons on the
// quiz view. Mirrors HandleQuestionMove's contract - HX-Request renders
// just the rounds partial so the page keeps its scroll position, plain
// POSTs fall back to the 303 redirect. The store layer enforces slot
// eligibility; the template hides the arrow in advance so a successful
// request is the common case.
func HandleRoundMove(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizview.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}
		if _, ok = requireEditableQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}
		roundID, ok := handlers.ParseIDFromPath(w, r, logger, "roundID")
		if !ok {
			return
		}
		direction := r.PathValue("direction")
		isHX := htmx.IsRequest(r)

		// ErrRoundMoveImpossible means the target slot is gone or out of
		// range. The arrow should already have been hidden in the UI, so
		// a request reaching here is a stale form or hand-crafted POST -
		// render the unchanged state through the same partial/redirect
		// path as a successful move. Other errors get a real HTTP status.
		switch err := quizStore.MoveRound(r.Context(), quizID, roundID, direction); {
		case errors.Is(err, quiz.ErrRoundMoveImpossible):
			logger.InfoContext(r.Context(), "round move skipped (target slot unavailable)", slog.Any("err", err))
		case err != nil:
			renderRoundMoveError(w, r, logger, csrfMgr, err)

			return
		default:
			// success - fall through to the partial / redirect path.
		}

		if isHX {
			renderRoundsPartial(w, r, logger, csrfMgr, renderer, quizStore, quizID)

			return
		}

		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
	})
}

// renderRoundMoveError translates a MoveRound failure into a 400 / 404 /
// 500 response. The ErrRoundMoveImpossible no-op is handled by the
// caller because it reuses the success-path renderer.
func renderRoundMoveError(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	err error,
) {
	switch {
	case errors.Is(err, quiz.ErrInvalidDirection):
		render400(w, r, logger, csrfMgr, "invalid direction")
	case errors.Is(err, quiz.ErrRoundNotFound):
		render404(w, r, logger, csrfMgr)
	default:
		logger.ErrorContext(r.Context(), "error moving round", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)
	}
}

// HandleRoundPosition handles a drag-and-drop round reorder (#199). The
// form body carries new_position (1-based). On success it re-renders the
// questions_list partial so the dragged DOM reconciles against server
// truth; the store clamps an out-of-range slot rather than erroring, so a
// stale drop still settles on a valid order.
func HandleRoundPosition(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizview.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}
		if _, ok = requireEditableQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}
		roundID, ok := handlers.ParseIDFromPath(w, r, logger, "roundID")
		if !ok {
			return
		}

		newPosition, ok := positionFromForm(w, r, logger, csrfMgr)
		if !ok {
			return
		}

		if err := quizStore.MoveRoundToPosition(r.Context(), quizID, roundID, newPosition); err != nil {
			if errors.Is(err, quiz.ErrRoundNotFound) {
				render404(w, r, logger, csrfMgr)

				return
			}
			logger.ErrorContext(r.Context(), "error moving round to position", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		renderRoundsPartial(w, r, logger, csrfMgr, renderer, quizStore, quizID)
	})
}

// HandleQuestionPosition handles a drag-and-drop question reorder (#199).
// The form body carries new_position (1-based, within the target round)
// and round_id (the round the question lands in, possibly its current
// one). On success it re-renders the questions_list partial. A cross-quiz
// question or round id surfaces as 404 to keep ids opaque (#339).
func HandleQuestionPosition(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizview.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}
		if _, ok = requireEditableQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}
		questionID, ok := handlers.ParseIDFromPath(w, r, logger, "questionID")
		if !ok {
			return
		}

		newPosition, ok := positionFromForm(w, r, logger, csrfMgr)
		if !ok {
			return
		}
		roundID, err := handlers.IDFromString(r.PostFormValue("round_id"))
		if err != nil || roundID == 0 {
			render400(w, r, logger, csrfMgr, "invalid round id")

			return
		}

		if moveErr := quizStore.MoveQuestionToPosition(
			r.Context(), quizID, questionID, roundID, newPosition,
		); moveErr != nil {
			switch {
			case errors.Is(moveErr, quiz.ErrQuestionNotFound), errors.Is(moveErr, quiz.ErrRoundNotFound):
				render404(w, r, logger, csrfMgr)
			default:
				logger.ErrorContext(r.Context(), "error moving question to position", slog.Any("err", moveErr))
				render500(w, r, logger, csrfMgr)
			}

			return
		}

		renderRoundsPartial(w, r, logger, csrfMgr, renderer, quizStore, quizID)
	})
}

// positionFromForm parses the bounded, form-encoded body and reads the
// 1-based new_position field shared by both drag-reorder handlers. A
// parse failure or a non-integer / missing value renders a 400 and
// returns ok=false.
func positionFromForm(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger, csrfMgr *csrf.Manager,
) (int, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
	if err := r.ParseForm(); err != nil {
		render400(w, r, logger, csrfMgr, "error parsing form")

		return 0, false
	}
	position, err := strconv.Atoi(r.PostFormValue("new_position"))
	if err != nil {
		render400(w, r, logger, csrfMgr, "invalid position")

		return 0, false
	}

	return position, true
}

// HandleRoundDelete removes a round. Owner-gated so only the quiz's
// creator can drop one of its rounds. Deleting a round cascades to its
// questions via the ON DELETE CASCADE on questions.round_id.
func HandleRoundDelete(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}

		if _, ok = requireEditableQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}

		roundID, ok := handlers.ParseIDFromPath(w, r, logger, "roundID")
		if !ok {
			return
		}

		// Reject cross-quiz deletes: a creator who owns quizID could
		// otherwise delete a round belonging to a different quiz by
		// mounting the round's id on this URL. Mirrors the question
		// IDOR gate (#339).
		if _, ok = roundByID(w, r, logger, csrfMgr, quizStore, quizID, roundID); !ok {
			return
		}

		if err := quizStore.DeleteRound(r.Context(), roundID); err != nil {
			if errors.Is(err, quiz.ErrDeletingRoundNoRowsAffected) {
				render404(w, r, logger, csrfMgr)

				return
			}
			logger.ErrorContext(r.Context(), "error deleting round", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		// htmx removes the round section in place via an outerHTML swap;
		// its questions ride along inside the swapped fragment because the
		// FK cascade drops them too. A plain form post falls back to the
		// 303 reload of the quiz view.
		if htmx.IsRequest(r) {
			w.WriteHeader(http.StatusOK)

			return
		}

		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
	})
}

// HandleQuestionMoveToRound reassigns a question to a different round.
// Owner-gated; a cross-quiz question or round id surfaces as 404 to
// keep ids opaque (#339 / #444).
func HandleQuestionMoveToRound(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}
		if _, ok = requireEditableQuizOwner(w, r, logger, csrfMgr, quizStore, quizID); !ok {
			return
		}
		questionID, ok := handlers.ParseIDFromPath(w, r, logger, "questionID")
		if !ok {
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
		if err := r.ParseForm(); err != nil {
			render400(w, r, logger, csrfMgr, "error parsing form")

			return
		}
		roundID, err := handlers.IDFromString(r.PostFormValue("round_id"))
		if err != nil {
			render400(w, r, logger, csrfMgr, "invalid round id")

			return
		}

		if moveErr := quizStore.MoveQuestionToRound(r.Context(), quizID, questionID, roundID); moveErr != nil {
			switch {
			case errors.Is(moveErr, quiz.ErrQuestionNotFound), errors.Is(moveErr, quiz.ErrRoundNotFound):
				render404(w, r, logger, csrfMgr)
			default:
				logger.ErrorContext(r.Context(), "error moving question to round", slog.Any("err", moveErr))
				render500(w, r, logger, csrfMgr)
			}

			return
		}

		http.Redirect(w, r, "/admin/quizzes/"+strconv.FormatInt(quizID, 10), http.StatusSeeOther)
	})
}

// roundSaveCtx is the artefact loadRoundForSave returns. Bundled in a
// struct so HandleRoundSave's signature stays under revive's
// function-result-limit, matching the question equivalent.
type roundSaveCtx struct {
	Quiz  *quiz.Quiz
	Round *quiz.Round
	IsNew bool
}

// loadRoundForSave parses the quizID + roundID off the path, applies
// the owner gate, and loads the existing round for an edit (or stamps a
// fresh struct positioned at the end of the round list for a create).
func loadRoundForSave(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
) (*roundSaveCtx, bool) {
	quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
	if !ok {
		return nil, false
	}
	roundID, ok := handlers.ParseIDFromPath(w, r, logger, "roundID")
	if !ok {
		return nil, false
	}
	qz, ok := requireEditableQuizOwner(w, r, logger, csrfMgr, quizStore, quizID)
	if !ok {
		return nil, false
	}
	if roundID == 0 {
		pos, posOK := nextRoundPosition(w, r, logger, csrfMgr, quizStore, qz.ID)
		if !posOK {
			return nil, false
		}

		return &roundSaveCtx{Quiz: qz, Round: &quiz.Round{QuizID: qz.ID, Position: pos}, IsNew: true}, true
	}
	g, ok := roundByID(w, r, logger, csrfMgr, quizStore, qz.ID, roundID)
	if !ok {
		return nil, false
	}

	return &roundSaveCtx{Quiz: qz, Round: g, IsNew: false}, true
}

// nextRoundPosition returns the position a newly created round should
// take: one past the current highest round position, so a new round
// lands at the end. Renders a 500 and returns ok=false on a store
// failure.
func nextRoundPosition(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	quizID int64,
) (int, bool) {
	rounds, err := quizStore.ListRoundsByQuiz(r.Context(), quizID)
	if err != nil {
		logger.ErrorContext(r.Context(), "error listing rounds for new round position", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return 0, false
	}
	pos := 0
	for _, g := range rounds {
		if g.Position >= pos {
			pos = g.Position + 1
		}
	}

	return pos, true
}

func renderRoundForm(
	w http.ResponseWriter,
	r *http.Request,
	renderer *render.Renderer,
	gctx *roundSaveCtx,
	fieldErrors map[string]string,
	formError string,
) {
	title := "Admin Dashboard - Round Edit"
	if gctx.IsNew {
		title = "Admin Dashboard - Round Create"
	}
	status := http.StatusBadRequest
	if formError != "" && len(fieldErrors) == 0 {
		status = http.StatusConflict
	}
	renderer.Render(w, r, status, roundFormData{
		Title:       title,
		Quiz:        quizDataFromQuiz(gctx.Quiz),
		Round:       roundDataFromRound(gctx.Round),
		FieldErrors: fieldErrors,
		FormError:   formError,
	})
}

// fillRoundFromForm reads the form into the supplied round struct. A
// parse error renders a 400 and returns (nil, false); a validation
// error returns a non-empty map + true; success returns (nil, true).
func fillRoundFromForm(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	g *quiz.Round,
) (map[string]string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
	if err := r.ParseForm(); err != nil {
		msg := "error parsing form"
		logger.ErrorContext(r.Context(), msg, slog.Any("err", err))
		render400(w, r, logger, csrfMgr, msg)

		return nil, false
	}
	g.Title = r.PostFormValue("title")
	g.Summary = r.PostFormValue("summary")
	// Optional per-round override (#554). Blank input clears any previous
	// override (NULL -> inherit the quiz default); a parse failure lands a
	// zero, which roundForm.Valid rejects with an inline range error
	// rather than silently saving a bad value.
	g.BoundaryDurationSeconds = parseOptionalTimeLimit(r.PostFormValue("boundary_duration_seconds"))

	if problems := (&roundForm{round: g}).Valid(r.Context()); len(problems) > 0 {
		return problems, true
	}

	return nil, true
}

func storeRound(ctx context.Context, quizStore quiz.Store, g *quiz.Round) error {
	if g.ID == 0 {
		if err := quizStore.CreateRound(ctx, g); err != nil {
			return fmt.Errorf("create round: %w", err)
		}

		return nil
	}
	if err := quizStore.UpdateRound(ctx, g); err != nil {
		return fmt.Errorf("update round: %w", err)
	}

	return nil
}

// roundByID loads a round and gates it on the URL-scoped quizID. A
// mismatch renders as 404 (not 403) so the route never leaks "this
// round exists on another quiz" - same IDOR rationale as questionByID.
func roundByID(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	quizID, roundID int64,
) (*quiz.Round, bool) {
	g, err := quizStore.GetRound(r.Context(), roundID)
	if err != nil {
		if errors.Is(err, quiz.ErrRoundNotFound) {
			logger.InfoContext(r.Context(), "round not found", slog.Any("err", err))
			render404(w, r, logger, csrfMgr)

			return nil, false
		}
		logger.ErrorContext(r.Context(), "error fetching round", slog.Any("err", err))
		render500(w, r, logger, csrfMgr)

		return nil, false
	}

	if g.QuizID != quizID {
		logger.InfoContext(
			r.Context(),
			"round belongs to a different quiz",
			slog.Int64("round_id", roundID),
			slog.Int64("group_quiz_id", g.QuizID),
			slog.Int64("url_quiz_id", quizID),
		)
		render404(w, r, logger, csrfMgr)

		return nil, false
	}

	return g, true
}

// roundForm is the validation hook for the admin round form. The title
// is required; the round-summary text is optional.
type roundForm struct {
	round *quiz.Round
}

// Valid checks every form-level rule on the wrapped round. An empty map
// means the form is valid.
func (f *roundForm) Valid(_ context.Context) map[string]string {
	problems := map[string]string{}
	if f.round.Title == "" {
		problems["title"] = "Give the round a name."
	}
	if f.round.BoundaryDurationSeconds != nil {
		v := *f.round.BoundaryDurationSeconds
		if v < quiz.MinTimeLimitSeconds || v > quiz.MaxTimeLimitSeconds {
			problems["boundarydurationseconds"] = fmt.Sprintf(
				"Round-boundary duration must be between %d and %d seconds, or blank to inherit the quiz default",
				quiz.MinTimeLimitSeconds, quiz.MaxTimeLimitSeconds,
			)
		}
	}

	return problems
}
