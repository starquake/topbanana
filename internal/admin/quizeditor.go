package admin

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/render"
)

// QuizEditorData backs the two-pane question editor (#1244). The rail on the
// left ranges over the same round view the quiz view renders, so the two share
// the questions_list partial; the right pane is filled in by htmx.
//
// InEditor switches that partial's rows from plain links into htmx triggers.
// It is a field on every struct the partial is rendered with rather than a
// lookup inside the template, because a template cannot reference a field the
// other callers' structs do not carry - it fails to evaluate for them.
type QuizEditorData struct {
	Title    string
	Quiz     *QuizData
	Rounds   []RoundViewData
	InEditor bool
	// SelectedID is the question the pane opens on, from ?q=. Zero means the
	// editor opens with nothing selected.
	SelectedID int64
}

// HandleQuizEditor renders the question editor for a quiz. Owner-only, via the
// same guard as the quiz view: requireQuizViewAccess 404s anyone who cannot
// edit, so there is no read-only rendering of this page.
func HandleQuizEditor(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	quizStore quiz.Store,
) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizeditor.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := handlers.ParseIDFromPath(w, r, logger, "quizID")
		if !ok {
			return
		}

		qz, ok := requireQuizViewAccess(w, r, logger, csrfMgr, quizStore, id)
		if !ok {
			return
		}

		rounds, ok := loadRounds(w, r, logger, csrfMgr, quizStore, id)
		if !ok {
			return
		}

		quizData := quizDataFromQuiz(qz)
		attachCanEdit(r, quizData)

		renderer.Render(w, r, http.StatusOK, QuizEditorData{
			Title:      "Admin Dashboard - Edit Questions",
			Quiz:       quizData,
			Rounds:     buildRoundView(rounds, quizData.Questions),
			InEditor:   true,
			SelectedID: selectedQuestionID(r),
		})
	})
}

// selectedQuestionID reads the ?q= deep link. A missing or unparseable value
// is not an error: the editor simply opens with nothing selected, which is
// also the state after deleting the selected question.
func selectedQuestionID(r *http.Request) int64 {
	raw := r.URL.Query().Get("q")
	if raw == "" {
		return 0
	}

	id, err := strconv.Atoi(raw)
	if err != nil || id < 1 {
		return 0
	}

	return int64(id)
}

// renderSavedQuestion writes the editor's post-save response: the re-rendered
// form for the pane, followed by the question's rail row marked
// hx-swap-oob so htmx grafts it over the stale one.
//
// Both fragments go out in a single response because htmx applies
// out-of-band elements from the same body it swaps into the target.
func renderSavedQuestion(
	w http.ResponseWriter,
	r *http.Request,
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	renderer *render.Renderer,
	mediaStore QuestionMediaStore,
	qctx *questionSaveCtx,
) {
	library, audioLibrary, ok := loadQuestionLibrary(w, r, logger, csrfMgr, mediaStore, qctx.Quiz.ID)
	if !ok {
		return
	}

	questionData := questionDataFromQuestion(qctx.Question)

	renderer.RenderPartials(w, r,
		render.Fragment{Name: "question_form", Data: questionFormData{
			Title:        "Admin Dashboard - Question Edit",
			Quiz:         quizDataFromQuiz(qctx.Quiz),
			Question:     questionData,
			Library:      library,
			AudioLibrary: audioLibrary,
		}},
		render.Fragment{Name: "question_row", Data: QuestionRowData{
			Question: questionData,
			CanEdit:  true,
			InEditor: true,
			OOB:      true,
		}},
	)
}
