package admin

import (
	"log/slog"
	"net/http"

	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/render"
)

// renderEditorAfterDelete answers an htmx delete from the editor: the empty
// pane into #question-editor, plus the rail out of band with the deleted row
// (or round) gone (#1260). quiz-reorder.js rebinds SortableJS on the swap.
func renderEditorAfterDelete(
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

	renderer.RenderPartials(w, r,
		render.Fragment{Name: "editor_empty", Data: nil},
		render.Fragment{Name: "questions_list", Data: roundsPartialData{
			Quiz:     quizData,
			Rounds:   buildRoundView(rounds, quizData.Questions),
			InEditor: true,
			OOB:      true,
		}},
	)
}
