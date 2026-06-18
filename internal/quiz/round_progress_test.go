package quiz_test

import (
	"testing"

	"github.com/starquake/topbanana/internal/quiz"
)

func TestQuestionRoundProgress(t *testing.T) {
	t.Parallel()

	// Two rounds: round 10 holds questions 1,2; round 20 holds questions 3,4,5.
	questions := []*quiz.Question{
		{ID: 1, RoundID: 10},
		{ID: 2, RoundID: 10},
		{ID: 3, RoundID: 20},
		{ID: 4, RoundID: 20},
		{ID: 5, RoundID: 20},
	}

	tests := []struct {
		name       string
		questionID int64
		want       quiz.RoundProgress
	}{
		{
			name:       "first question of first round",
			questionID: 1,
			want:       quiz.RoundProgress{RoundNumber: 1, RoundTotal: 2, RoundPosition: 1, RoundQuestions: 2},
		},
		{
			name:       "second question of first round",
			questionID: 2,
			want:       quiz.RoundProgress{RoundNumber: 1, RoundTotal: 2, RoundPosition: 2, RoundQuestions: 2},
		},
		{
			name:       "first question of second round",
			questionID: 3,
			want:       quiz.RoundProgress{RoundNumber: 2, RoundTotal: 2, RoundPosition: 1, RoundQuestions: 3},
		},
		{
			name:       "last question of second round",
			questionID: 5,
			want:       quiz.RoundProgress{RoundNumber: 2, RoundTotal: 2, RoundPosition: 3, RoundQuestions: 3},
		},
		{
			name:       "unknown question returns zero value",
			questionID: 99,
			want:       quiz.RoundProgress{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, want := quiz.QuestionRoundProgress(questions, tt.questionID), tt.want; got != want {
				t.Errorf("QuestionRoundProgress(%d) = %+v, want %+v", tt.questionID, got, want)
			}
		})
	}
}

func TestQuestionRoundProgressSingleRound(t *testing.T) {
	t.Parallel()

	questions := []*quiz.Question{
		{ID: 1, RoundID: 7},
		{ID: 2, RoundID: 7},
		{ID: 3, RoundID: 7},
	}

	if got, want := quiz.QuestionRoundProgress(questions, 2),
		(quiz.RoundProgress{RoundNumber: 1, RoundTotal: 1, RoundPosition: 2, RoundQuestions: 3}); got != want {
		t.Errorf("QuestionRoundProgress(2) = %+v, want %+v", got, want)
	}
}
