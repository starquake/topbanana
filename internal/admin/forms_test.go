package admin_test

import (
	"testing"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/quiz"
)

// TestQuizForm_Valid pins the form-level rules the admin quiz save
// surface depends on. Moved from internal/quiz/quiz_test.go in #36
// when the validation was lifted off the domain struct onto the
// admin form type - the rules and the cases that pin them are now
// colocated with the markup that renders them.
func TestQuizForm_Valid(t *testing.T) {
	t.Parallel()

	t.Run("valid quiz", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			quiz quiz.Quiz
		}{
			{
				name: "valid quiz without questions",
				quiz: quiz.Quiz{
					Title:       "Quiz 1",
					Slug:        "quiz-1",
					Description: "Quiz 1 Description",
				},
			},
			{
				name: "valid quiz with questions",
				quiz: quiz.Quiz{
					Title:       "Quiz 2",
					Slug:        "quiz-2",
					Description: "Quiz 2 Description",
					Questions: []*quiz.Question{
						{
							Text: "Question 1",
							Options: []*quiz.Option{
								{Text: "Option 1", Correct: true},
								{Text: "Option 2"},
							},
						},
						{
							Text: "Question 2",
							Options: []*quiz.Option{
								{Text: "Option 3", Correct: true},
								{Text: "Option 4"},
							},
						},
					},
				},
			},
			{
				// Multi-correct, all-correct, and no-correct are all
				// allowed - the admin UI offers a checkbox per option and a
				// question where the player is meant to pick none is a
				// legitimate shape (the "no correct option" valid case
				// below pins this).
				name: "valid quiz with multiple correct options on a question",
				quiz: quiz.Quiz{
					Title:       "Quiz multi-correct",
					Slug:        "quiz-multi-correct",
					Description: "Quiz description",
					Questions: []*quiz.Question{
						{
							Text: "Pick all primes",
							Options: []*quiz.Option{
								{Text: "2", Correct: true},
								{Text: "3", Correct: true},
								{Text: "4"},
								{Text: "5", Correct: true},
							},
						},
					},
				},
			},
			{
				name: "valid quiz with all options correct",
				quiz: quiz.Quiz{
					Title:       "Quiz all-correct",
					Slug:        "quiz-all-correct",
					Description: "Quiz description",
					Questions: []*quiz.Question{
						{
							Text: "Pick a colour",
							Options: []*quiz.Option{
								{Text: "red", Correct: true},
								{Text: "blue", Correct: true},
								{Text: "green", Correct: true},
							},
						},
					},
				},
			},
			{
				// A question with no correct option is a supported shape
				// (the player is meant to pick none); the admin quiz-import
				// and create flows both rely on it.
				name: "valid quiz with a no-correct-option question",
				quiz: quiz.Quiz{
					Title:       "Quiz no-correct",
					Slug:        "quiz-no-correct",
					Description: "Quiz description",
					Questions: []*quiz.Question{
						{
							Text: "Pick none",
							Options: []*quiz.Option{
								{Text: "wrong"},
								{Text: "also wrong"},
							},
						},
					},
				},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				if problems := ValidateQuizForm(t.Context(), &tc.quiz); len(problems) > 0 {
					t.Errorf("quiz is not valid: %v", tc.quiz)
					for k, v := range problems {
						t.Errorf("  %s: %s", k, v)
					}
				}
			})
		}
	})

	t.Run("invalid quiz", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			quiz quiz.Quiz
		}{
			{
				name: "quiz without title",
				quiz: quiz.Quiz{
					Slug:        "quiz-1",
					Description: "Quiz 1 Description",
				},
			},
			{
				name: "quiz without slug",
				quiz: quiz.Quiz{
					Title:       "Quiz 2",
					Description: "Quiz 2 Description",
				},
			},
			{
				name: "quiz without description",
				quiz: quiz.Quiz{
					Title: "Quiz 3",
					Slug:  "quiz-3",
				},
			},
			{
				name: "valid quiz with invalid questions (no options)",
				quiz: quiz.Quiz{
					Title:       "Quiz 2",
					Slug:        "quiz-2",
					Description: "Quiz 2 Description",
					Questions: []*quiz.Question{
						{Text: "Question 1"},
						{Text: "Question 2"},
					},
				},
			},
			{
				name: "quiz with invalid question (no text)",
				quiz: quiz.Quiz{
					Title:       "Quiz 4",
					Slug:        "quiz-4",
					Description: "Quiz 4 Description",
					Questions: []*quiz.Question{
						{Text: ""},
					},
				},
			},
			{
				name: "quiz with question with invalid position",
				quiz: quiz.Quiz{
					Title:       "Quiz 5",
					Slug:        "quiz-5",
					Description: "Quiz 5 Description",
					Questions: []*quiz.Question{
						{Text: "Question 1", Position: -1},
					},
				},
			},
			{
				name: "quiz with question with invalid options",
				quiz: quiz.Quiz{
					Title:       "Quiz 6",
					Slug:        "quiz-6",
					Description: "Quiz 6 Description",
					Questions: []*quiz.Question{
						{Text: "Question 1", Options: []*quiz.Option{{Text: "", Correct: true}}},
					},
				},
			},
			{
				name: "quiz with question with too many options",
				quiz: quiz.Quiz{
					Title:       "Quiz too-many",
					Slug:        "quiz-too-many",
					Description: "Quiz description",
					Questions: []*quiz.Question{
						{
							Text: "Pick one",
							Options: []*quiz.Option{
								{Text: "a", Correct: true},
								{Text: "b"},
								{Text: "c"},
								{Text: "d"},
								{Text: "e"},
							},
						},
					},
				},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				problems := ValidateQuizForm(t.Context(), &tc.quiz)
				if len(problems) == 0 {
					t.Errorf("quiz should not be valid: %v", tc.quiz)
				}
				t.Logf("quiz is invalid: %v", problems)
			})
		}
	})
}

// TestQuestionForm_Valid_OptionRules pins the per-question option rules
// directly: a question needs 1..MaxOptions options. Having no correct
// option is allowed (the player is meant to pick none).
func TestQuestionForm_Valid_OptionRules(t *testing.T) {
	t.Parallel()

	tooMany := make([]*quiz.Option, 0, MaxOptions+1)
	tooMany = append(tooMany, &quiz.Option{Text: "a", Correct: true})
	for range MaxOptions {
		tooMany = append(tooMany, &quiz.Option{Text: "x"})
	}

	tests := []struct {
		name      string
		question  quiz.Question
		wantValid bool
	}{
		{
			name:      "no options",
			question:  quiz.Question{Text: "Q", Options: nil},
			wantValid: false,
		},
		{
			name: "no correct option is allowed",
			question: quiz.Question{Text: "Q", Options: []*quiz.Option{
				{Text: "a"}, {Text: "b"},
			}},
			wantValid: true,
		},
		{
			name:      "too many options",
			question:  quiz.Question{Text: "Q", Options: tooMany},
			wantValid: false,
		},
		{
			name: "one correct within cap",
			question: quiz.Question{Text: "Q", Options: []*quiz.Option{
				{Text: "a", Correct: true}, {Text: "b"},
			}},
			wantValid: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			problems := ValidateQuestionForm(t.Context(), &tc.question)
			_, hasOptionProblem := problems["options"]
			if got, want := !hasOptionProblem, tc.wantValid; got != want {
				t.Errorf("options problem absent = %v, want %v (problems=%v)", got, want, problems)
			}
		})
	}
}
