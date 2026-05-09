package quiz_test

import (
	"testing"

	. "github.com/starquake/topbanana/internal/quiz"
)

func TestQuiz_Valid(t *testing.T) {
	t.Parallel()

	t.Run("valid quiz", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			quiz Quiz
		}{
			{
				name: "valid quiz without questions",
				quiz: Quiz{
					Title:       "Quiz 1",
					Slug:        "quiz-1",
					Description: "Quiz 1 Description",
				},
			},
			{
				name: "valid quiz with questions",
				quiz: Quiz{
					Title:       "Quiz 2",
					Slug:        "quiz-2",
					Description: "Quiz 2 Description",
					Questions: []*Question{
						{
							Text: "Question 1",
							Options: []*Option{
								{Text: "Option 1"},
								{Text: "Option 2"},
							},
						},
						{
							Text: "Question 2",
							Options: []*Option{
								{Text: "Option 3"},
								{Text: "Option 4"},
							},
						},
					},
				},
			},
			{
				// Multi-correct, no-correct, and all-correct are all
				// allowed configurations — the admin UI offers a checkbox
				// per option and the player flow handles each.
				name: "valid quiz with multiple correct options on a question",
				quiz: Quiz{
					Title:       "Quiz multi-correct",
					Slug:        "quiz-multi-correct",
					Description: "Quiz description",
					Questions: []*Question{
						{
							Text: "Pick all primes",
							Options: []*Option{
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
				quiz: Quiz{
					Title:       "Quiz all-correct",
					Slug:        "quiz-all-correct",
					Description: "Quiz description",
					Questions: []*Question{
						{
							Text: "Pick a colour",
							Options: []*Option{
								{Text: "red", Correct: true},
								{Text: "blue", Correct: true},
								{Text: "green", Correct: true},
							},
						},
					},
				},
			},
			{
				name: "valid quiz with no correct options on a question",
				quiz: Quiz{
					Title:       "Quiz no-correct",
					Slug:        "quiz-no-correct",
					Description: "Quiz description",
					Questions: []*Question{
						{
							Text: "Trick question",
							Options: []*Option{
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
				if problems := tc.quiz.Valid(t.Context()); len(problems) > 0 {
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
			quiz Quiz
		}{
			{
				name: "quiz without title",
				quiz: Quiz{
					Slug:        "quiz-1",
					Description: "Quiz 1 Description",
				},
			},
			{
				name: "quiz without slug",
				quiz: Quiz{
					Title:       "Quiz 2",
					Description: "Quiz 2 Description",
				},
			},
			{
				name: "quiz without description",
				quiz: Quiz{
					Title: "Quiz 3",
					Slug:  "quiz-3",
				},
			},
			{
				name: "valid quiz with invalid questions (no options)",
				quiz: Quiz{
					Title:       "Quiz 2",
					Slug:        "quiz-2",
					Description: "Quiz 2 Description",
					Questions: []*Question{
						{Text: "Question 1"},
						{Text: "Question 2"},
					},
				},
			},
			{
				name: "quiz with invalid question (no text)",
				quiz: Quiz{
					Title:       "Quiz 4",
					Slug:        "quiz-4",
					Description: "Quiz 4 Description",
					Questions: []*Question{
						{Text: ""},
					},
				},
			},
			{
				name: "quiz with question with invalid position",
				quiz: Quiz{
					Title:       "Quiz 5",
					Slug:        "quiz-5",
					Description: "Quiz 5 Description",
					Questions: []*Question{
						{Text: "Question 1", Position: -1},
					},
				},
			},
			{
				name: "quiz with question with invalid options",
				quiz: Quiz{
					Title:       "Quiz 6",
					Slug:        "quiz-6",
					Description: "Quiz 6 Description",
					Questions: []*Question{
						{Text: "Question 1", Options: []*Option{{Text: ""}}},
					},
				},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				problems := tc.quiz.Valid(t.Context())
				if len(problems) == 0 {
					t.Errorf("quiz should not be valid: %v", tc.quiz)
				}
				t.Logf("quiz is invalid: %v", problems)
			})
		}
	})
}
