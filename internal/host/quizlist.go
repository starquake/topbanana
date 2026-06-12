package host

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/starquake/topbanana/internal/auth"
)

// quizCardData carries exactly the fields the shared quiz_card partial reads on
// its host path. It mirrors the admin QuizData shape for the card's body, but
// is defined here so the host surface does not import internal/admin.
type quizCardData struct {
	ID            int64
	Title         string
	Slug          string
	Description   string
	UpdatedAt     time.Time
	QuestionCount int
	PlayCount     int64
	Mode          string
	ActionVariant string
}

// quizListData feeds the host quiz-list page.
type quizListData struct {
	Title   string
	Quizzes []quizCardData
}

// QuizList handles GET /host/quizzes: the host picks a live quiz to run. The
// list is the runnable subset - live mode (ListLiveQuizzes) with at least one
// question - so the host never lands on a quiz that cannot start. Each card's
// only action posts quiz_id to /host, which arms the quiz in the host's room.
func (h *Handlers) QuizList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if _, ok := auth.PlayerFromContext(ctx); !ok {
		h.logger.ErrorContext(ctx, "missing player on context for host quiz list")
		http.Error(w, msgInternalError, http.StatusInternalServerError)

		return
	}

	quizzes, err := h.quizzes.ListLiveQuizzes(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "error listing live quizzes", slog.Any("err", err))
		http.Error(w, msgInternalError, http.StatusInternalServerError)

		return
	}

	counts, err := h.quizzes.QuestionCountsByQuiz(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "error loading question counts", slog.Any("err", err))
		http.Error(w, msgInternalError, http.StatusInternalServerError)

		return
	}

	cards := make([]quizCardData, 0, len(quizzes))
	for _, qz := range quizzes {
		count := counts[qz.ID]
		if count == 0 {
			continue
		}
		cards = append(cards, quizCardData{
			ID:            qz.ID,
			Title:         qz.Title,
			Slug:          qz.Slug,
			Description:   qz.Description,
			UpdatedAt:     qz.UpdatedAt,
			QuestionCount: count,
			PlayCount:     qz.PlayCount,
			Mode:          qz.Mode,
			ActionVariant: "host",
		})
	}

	h.render(w, r, h.quizListTmpl, "page.gohtml", quizListData{
		Title:   "Host a quiz",
		Quizzes: cards,
	})
}
