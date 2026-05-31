package main

// Test-only re-exports so the external main_test package can exercise
// the fixture->domain mapping without widening the production surface.
var (
	ExportQuizFromFixture              = quizFromFixture
	ErrExportFixtureQuestionsOrRounds  = errFixtureQuestionsOrRounds
	ErrExportFixtureRoundTitleRequired = errFixtureRoundTitleRequired
	ErrExportFixtureRoundNoQuestions   = errFixtureRoundNoQuestions
)

// ExportQuizFixture, ExportRoundFixture, ExportQuestionFixture, and
// ExportOptionFixture re-export the unexported fixture types so the
// external test can build inputs for ExportQuizFromFixture.
type (
	ExportQuizFixture     = quizFixture
	ExportRoundFixture    = roundFixture
	ExportQuestionFixture = questionFixture
	ExportOptionFixture   = optionFixture
)
