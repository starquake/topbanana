package main

// Test-only re-exports so the external main_test package can exercise
// the fixture->domain mapping, the audio-seeding path, the demo-archive
// restore path, and the play-seeding path without widening the production
// surface.
var (
	ExportQuizFromFixture              = quizFromFixture
	ExportSeedQuizzes                  = seedQuizzes
	ExportSeedDemoQuiz                 = seedDemoQuiz
	ExportOpenDemoArchive              = openDemoArchive
	ExportSeedPlayerName               = seedPlayerName
	ExportSeedPlayerNames              = seedPlayerNames
	ExportSeedPlays                    = seedPlays
	ErrExportFixtureQuestionsOrRounds  = errFixtureQuestionsOrRounds
	ErrExportFixtureRoundTitleRequired = errFixtureRoundTitleRequired
	ErrExportFixtureRoundNoQuestions   = errFixtureRoundNoQuestions
)

// ExportSampleAudio re-exports the bundled sample clip so a test can assert the
// stored file's bytes match what the seeder embedded.
var ExportSampleAudio = sampleAudio

// ExportQuizFixture, ExportRoundFixture, ExportQuestionFixture, and
// ExportOptionFixture re-export the unexported fixture types so the
// external test can build inputs for ExportQuizFromFixture.
type (
	ExportQuizFixture     = quizFixture
	ExportRoundFixture    = roundFixture
	ExportQuestionFixture = questionFixture
	ExportOptionFixture   = optionFixture
)
