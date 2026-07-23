package admin

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/htmx"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/render"
)

// HandleQuestionDuplicate copies a question and drops the copy directly after
// the original (#1246). Authoring several near-identical questions otherwise
// means retyping four options with one word changed.
//
// The copy is created at the end and then moved, rather than inserted mid-list:
// [quiz.Store.CreateQuestionAtNextPosition] already owns the max+1 race and its
// retry budget, and MoveQuestionToPosition already owns the renumbering. Two
// tested primitives beat a third transaction that has to get both right.
func HandleQuestionDuplicate(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
	mediaStore QuestionMediaStore,
) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/questionform.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quizID, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}

		questionID, ok := handlers.ParseIDFromPath(w, r, logger, "questionID")
		if !ok {
			return
		}

		// Editable-owner, not just owner: duplicating is a content edit, so it
		// is refused on a published quiz like every other one (#1192).
		qz, ok := requireEditableQuizOwner(w, r, logger, csrfMgr, quizStore, quizID)
		if !ok {
			return
		}

		source, ok := questionByID(w, r, logger, csrfMgr, quizStore, qz.ID, questionID)
		if !ok {
			return
		}

		copied := copyQuestion(source)
		if err := quizStore.CreateQuestionAtNextPosition(r.Context(), copied); err != nil {
			logger.ErrorContext(r.Context(), "error duplicating question", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		// Slot it in behind its source. A failure here leaves a valid question
		// at the end of the quiz rather than a broken one, so it is logged and
		// the admin still gets their copy.
		if err := quizStore.MoveQuestionToPosition(
			r.Context(), qz.ID, copied.ID, copied.RoundID, source.Position+1,
		); err != nil {
			logger.ErrorContext(r.Context(), "error positioning duplicated question", slog.Any("err", err))
		}

		if htmx.IsRequest(r) {
			renderDuplicatedQuestion(w, r, duplicateDeps{
				logger:   logger,
				csrfMgr:  csrfMgr,
				renderer: renderer,
				quizzes:  quizStore,
				media:    mediaStore,
			}, qz, copied.ID)

			return
		}

		http.Redirect(
			w, r,
			"/admin/quizzes/"+strconv.FormatInt(qz.ID, 10)+
				"/questions?q="+strconv.FormatInt(copied.ID, 10),
			http.StatusSeeOther,
		)
	})
}

// copyQuestion returns an unsaved duplicate of qs: same text, options, media,
// repeat flag and time limit, with the identity fields left for the store to
// assign. Media is referenced by id, so the copy points at the same library
// entries rather than duplicating the uploads.
func copyQuestion(qs *quiz.Question) *quiz.Question {
	copied := &quiz.Question{
		QuizID:           qs.QuizID,
		RoundID:          qs.RoundID,
		Text:             qs.Text,
		AudioRepeat:      qs.AudioRepeat,
		ImageMediaID:     copyInt64Ptr(qs.ImageMediaID),
		AudioMediaID:     copyInt64Ptr(qs.AudioMediaID),
		TimeLimitSeconds: copyIntPtr(qs.TimeLimitSeconds),
		Options:          make([]*quiz.Option, 0, len(qs.Options)),
	}

	for _, o := range qs.Options {
		copied.Options = append(copied.Options, &quiz.Option{
			Text:    o.Text,
			Correct: o.Correct,
		})
	}

	return copied
}

// copyInt64Ptr returns a fresh pointer to the same value, so the copy never
// shares a nullable field with its source.
func copyInt64Ptr(v *int64) *int64 {
	if v == nil {
		return nil
	}
	out := *v

	return &out
}

// copyIntPtr is [copyInt64Ptr] for the per-question time limit.
func copyIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	out := *v

	return &out
}

// renderDuplicatedQuestion answers the editor: the copy's own form for the
// pane, and the whole rail out of band. The rail re-renders wholesale because a
// brand new row has no element for htmx to graft onto - the same reason adding
// a round does (#1257). quiz-reorder.js rebinds SortableJS on the swap.
// duplicateDeps bundles the request-scoped plumbing renderDuplicatedQuestion
// needs. They always travel together, and passing them individually pushed the
// function past revive's argument limit.
type duplicateDeps struct {
	logger   *slog.Logger
	csrfMgr  *csrf.Manager
	renderer *render.Renderer
	quizzes  quiz.Store
	media    QuestionMediaStore
}

func renderDuplicatedQuestion(
	w http.ResponseWriter,
	r *http.Request,
	deps duplicateDeps,
	qz *quiz.Quiz,
	copiedID int64,
) {
	logger, csrfMgr, renderer := deps.logger, deps.csrfMgr, deps.renderer
	quizStore, mediaStore := deps.quizzes, deps.media

	// Re-read the copy rather than trusting the struct we just inserted: the
	// position move renumbered it, and its options came back with fresh ids.
	fresh, ok := questionByID(w, r, logger, csrfMgr, quizStore, qz.ID, copiedID)
	if !ok {
		return
	}

	library, audioLibrary, ok := loadQuestionLibrary(w, r, logger, csrfMgr, mediaStore, qz.ID)
	if !ok {
		return
	}

	rounds, ok := loadRounds(w, r, logger, csrfMgr, quizStore, qz.ID)
	if !ok {
		return
	}

	// Re-read the quiz: the one the owner gate loaded predates the copy, so
	// building the rail from it renders a list without the new row - the swap
	// lands correctly and changes nothing visible.
	refreshed, ok := quizByID(w, r, logger, csrfMgr, quizStore, qz.ID)
	if !ok {
		return
	}

	quizData := quizDataFromQuiz(refreshed)
	attachCanEdit(r, quizData)

	renderer.RenderPartials(w, r,
		render.Fragment{Name: "question_form", Data: questionFormData{
			Title:        "Admin Dashboard - Question Edit",
			Quiz:         quizData,
			Question:     questionDataFromQuestion(fresh),
			Library:      library,
			AudioLibrary: audioLibrary,
			InEditor:     true,
		}},
		render.Fragment{Name: "questions_list", Data: roundsPartialData{
			Quiz:     quizData,
			Rounds:   buildRoundView(rounds, quizData.Questions),
			InEditor: true,
			OOB:      true,
		}},
	)
}
