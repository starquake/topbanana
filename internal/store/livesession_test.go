package store_test

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/quiz"
	. "github.com/starquake/topbanana/internal/store"
)

// newLiveQuiz seeds a mode='live' quiz so a session has a valid quiz_id
// FK, returning it. The session create/join paths only need the quiz to
// exist; questions are optional for these store tests.
func newLiveQuiz(t *testing.T, qs *QuizStore) *quiz.Quiz {
	t.Helper()

	qz := &quiz.Quiz{
		Title:             "Live Session Quiz",
		Slug:              "live-session-quiz",
		Description:       "fixture",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeLive,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "A", Correct: true}, {Text: "B"}}},
		},
	}
	if err := qs.CreateQuiz(t.Context(), qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	return qz
}

func TestLiveSessionStore_CreateAndGetByJoinCode(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuiz(t, quizStore)

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "ABC234"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	if sess.ID == "" {
		t.Error("CreateSession did not populate ID")
	}
	if got, want := sess.Phase, livesession.PhaseLobby; got != want {
		t.Errorf("Phase = %q, want %q", got, want)
	}

	got, err := sessionStore.GetSessionByJoinCode(t.Context(), "ABC234")
	if err != nil {
		t.Fatalf("GetSessionByJoinCode err = %v, want nil", err)
	}
	if got, want := got.ID, sess.ID; got != want {
		t.Errorf("session ID = %q, want %q", got, want)
	}
	if got, want := got.QuizID, qz.ID; got != want {
		t.Errorf("session QuizID = %d, want %d", got, want)
	}
}

func TestLiveSessionStore_GetByJoinCode_NotFound(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	sessionStore := NewLiveSessionStore(db, slog.Default())

	_, err := sessionStore.GetSessionByJoinCode(t.Context(), "NOPE99")
	if got, want := err, livesession.ErrSessionNotFound; !errors.Is(got, want) {
		t.Errorf("err = %v, want %v", got, want)
	}
}

func TestLiveSessionStore_CreateSession_DuplicateJoinCode(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuiz(t, quizStore)

	first := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "DUP234"}
	if err := sessionStore.CreateSession(t.Context(), first); err != nil {
		t.Fatalf("first CreateSession err = %v, want nil", err)
	}

	second := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "DUP234"}
	if got, want := sessionStore.CreateSession(
		t.Context(),
		second,
	), livesession.ErrJoinCodeUnavailable; !errors.Is(
		got,
		want,
	) {
		t.Errorf("duplicate CreateSession err = %v, want %v", got, want)
	}
}

func TestLiveSessionStore_AddPlayer_AndRoster(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuiz(t, quizStore)

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "ROST23"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}

	p1, err := playerStore.CreateAnonymousPlayer(t.Context(), "roster-p1")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer p1 err = %v, want nil", err)
	}
	p2, err := playerStore.CreateAnonymousPlayer(t.Context(), "roster-p2")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer p2 err = %v, want nil", err)
	}

	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, p1.ID, "Alice"); err != nil {
		t.Fatalf("AddPlayer p1 err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, p2.ID, "Bob"); err != nil {
		t.Fatalf("AddPlayer p2 err = %v, want nil", err)
	}

	loaded, err := sessionStore.GetSessionByJoinCode(t.Context(), "ROST23")
	if err != nil {
		t.Fatalf("GetSessionByJoinCode err = %v, want nil", err)
	}
	if got, want := len(loaded.Players), 2; got != want {
		t.Fatalf("len(Players) = %d, want %d", got, want)
	}
	if got, want := loaded.Players[0].DisplayName, "Alice"; got != want {
		t.Errorf("Players[0].DisplayName = %q, want %q (join order)", got, want)
	}
}

func TestLiveSessionStore_AddPlayer_DisplayNameTaken(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuiz(t, quizStore)

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "DUPN23"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	p1, err := playerStore.CreateAnonymousPlayer(t.Context(), "dupn-p1")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer p1 err = %v, want nil", err)
	}
	p2, err := playerStore.CreateAnonymousPlayer(t.Context(), "dupn-p2")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer p2 err = %v, want nil", err)
	}

	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, p1.ID, "Sam"); err != nil {
		t.Fatalf("AddPlayer p1 err = %v, want nil", err)
	}
	_, err = sessionStore.AddPlayer(t.Context(), sess.ID, p2.ID, "Sam")
	if got, want := err, livesession.ErrDisplayNameTaken; !errors.Is(got, want) {
		t.Errorf("AddPlayer collision err = %v, want %v", got, want)
	}
}

func TestLiveSessionStore_AddPlayer_RejoinIsIdempotent(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuiz(t, quizStore)

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "REJN23"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	p1, err := playerStore.CreateAnonymousPlayer(t.Context(), "rejn-p1")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, p1.ID, "First"); err != nil {
		t.Fatalf("AddPlayer err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, p1.ID, "Second"); err != nil {
		t.Fatalf("AddPlayer rejoin err = %v, want nil", err)
	}

	loaded, err := sessionStore.GetSessionByJoinCode(t.Context(), "REJN23")
	if err != nil {
		t.Fatalf("GetSessionByJoinCode err = %v, want nil", err)
	}
	if got, want := len(loaded.Players), 1; got != want {
		t.Errorf("len(Players) after rejoin = %d, want %d", got, want)
	}
	if got, want := loaded.Players[0].DisplayName, "Second"; got != want {
		t.Errorf("rejoin DisplayName = %q, want %q", got, want)
	}
}

func TestLiveSessionStore_SetReady(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuiz(t, quizStore)

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "RDY234"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	p1, err := playerStore.CreateAnonymousPlayer(t.Context(), "rdy-p1")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, p1.ID, "Ready Player"); err != nil {
		t.Fatalf("AddPlayer err = %v, want nil", err)
	}

	if err = sessionStore.SetReady(t.Context(), sess.ID, p1.ID, true); err != nil {
		t.Fatalf("SetReady err = %v, want nil", err)
	}

	loaded, err := sessionStore.GetSessionByJoinCode(t.Context(), "RDY234")
	if err != nil {
		t.Fatalf("GetSessionByJoinCode err = %v, want nil", err)
	}
	if got, want := loaded.Players[0].IsReady, true; got != want {
		t.Errorf("IsReady = %v, want %v", got, want)
	}
}

func TestLiveSessionStore_SetReady_NotParticipant(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuiz(t, quizStore)

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "NPRT23"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}

	err := sessionStore.SetReady(t.Context(), sess.ID, seededAdminID, true)
	if got, want := err, livesession.ErrNotParticipant; !errors.Is(got, want) {
		t.Errorf("SetReady non-participant err = %v, want %v", got, want)
	}
}

// newLiveQuizWithQuestion seeds a live quiz carrying one question with a
// correct + wrong option, returning the quiz with its rounds/questions
// loaded, so the runner-facing store tests have real round/question/option
// ids to point the session at.
func newLiveQuizWithQuestion(t *testing.T, qs *QuizStore) *quiz.Quiz {
	t.Helper()

	qz := &quiz.Quiz{
		Title:             "Runner Store Quiz",
		Slug:              "runner-store-quiz",
		Description:       "fixture",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeLive,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "right", Correct: true}, {Text: "wrong"}}},
		},
	}
	if err := qs.CreateQuiz(t.Context(), qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}
	loaded, err := qs.GetQuiz(t.Context(), qz.ID)
	if err != nil {
		t.Fatalf("GetQuiz err = %v, want nil", err)
	}

	return loaded
}

func TestLiveSessionStore_PhaseTransitions(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuizWithQuestion(t, quizStore)
	q := qz.Questions[0]

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "PHAS23"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}

	won, err := sessionStore.MarkStarted(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("MarkStarted err = %v, want nil", err)
	}
	if !won {
		t.Fatal("first MarkStarted won = false, want true")
	}
	// A second MarkStarted on the already-started session loses the race.
	again, err := sessionStore.MarkStarted(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("second MarkStarted err = %v, want nil", err)
	}
	if again {
		t.Error("second MarkStarted won = true, want false")
	}

	if err = sessionStore.EnterRoundIntro(t.Context(), sess.ID, q.RoundID); err != nil {
		t.Fatalf("EnterRoundIntro err = %v, want nil", err)
	}
	intro, err := sessionStore.GetSessionByID(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSessionByID err = %v, want nil", err)
	}
	if got, want := intro.Phase, livesession.PhaseRoundIntro; got != want {
		t.Errorf("phase = %q, want %q", got, want)
	}
	if intro.CurrentRoundID == nil || *intro.CurrentRoundID != q.RoundID {
		t.Errorf("CurrentRoundID = %v, want %d", intro.CurrentRoundID, q.RoundID)
	}

	started := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	expires := started.Add(10 * time.Second)
	if err = sessionStore.EnterQuestion(t.Context(), sess.ID, q.RoundID, q.ID, started, expires); err != nil {
		t.Fatalf("EnterQuestion err = %v, want nil", err)
	}
	question, err := sessionStore.GetSessionByID(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSessionByID err = %v, want nil", err)
	}
	if got, want := question.Phase, livesession.PhaseQuestion; got != want {
		t.Errorf("phase = %q, want %q", got, want)
	}
	if question.CurrentQuestionID == nil || *question.CurrentQuestionID != q.ID {
		t.Errorf("CurrentQuestionID = %v, want %d", question.CurrentQuestionID, q.ID)
	}
	if question.QuestionExpiresAt == nil || !question.QuestionExpiresAt.Equal(expires) {
		t.Errorf("QuestionExpiresAt = %v, want %v", question.QuestionExpiresAt, expires)
	}

	if err = sessionStore.EnterReveal(t.Context(), sess.ID); err != nil {
		t.Fatalf("EnterReveal err = %v, want nil", err)
	}
	reveal, err := sessionStore.GetSessionByID(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSessionByID err = %v, want nil", err)
	}
	if got, want := reveal.Phase, livesession.PhaseReveal; got != want {
		t.Errorf("phase = %q, want %q", got, want)
	}
	// The reveal keeps the current question in place so a reader still sees it.
	if reveal.CurrentQuestionID == nil || *reveal.CurrentQuestionID != q.ID {
		t.Errorf("reveal CurrentQuestionID = %v, want %d", reveal.CurrentQuestionID, q.ID)
	}

	if err = sessionStore.Finish(t.Context(), sess.ID); err != nil {
		t.Fatalf("Finish err = %v, want nil", err)
	}
	final, err := sessionStore.GetSessionByID(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSessionByID err = %v, want nil", err)
	}
	if got, want := final.Phase, livesession.PhaseFinished; got != want {
		t.Errorf("phase = %q, want %q", got, want)
	}
	if final.FinishedAt == nil {
		t.Error("FinishedAt = nil, want a timestamp")
	}
	if final.CurrentQuestionID != nil {
		t.Errorf("finished CurrentQuestionID = %v, want nil", *final.CurrentQuestionID)
	}
}

func TestLiveSessionStore_AnswersRoundTrip(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuizWithQuestion(t, quizStore)
	q := qz.Questions[0]
	correctOpt := q.Options[0]

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "ANSW23"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	p, err := playerStore.CreateAnonymousPlayer(t.Context(), "answ-p1")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, p.ID, "Ann"); err != nil {
		t.Fatalf("AddPlayer err = %v, want nil", err)
	}

	answeredAt := time.Date(2026, time.June, 5, 12, 0, 5, 0, time.UTC)
	if err = sessionStore.RecordAnswer(t.Context(), sess.ID, q.ID, p.ID, correctOpt.ID, answeredAt); err != nil {
		t.Fatalf("RecordAnswer err = %v, want nil", err)
	}

	count, err := sessionStore.CountAnswers(t.Context(), sess.ID, q.ID)
	if err != nil {
		t.Fatalf("CountAnswers err = %v, want nil", err)
	}
	if got, want := count, 1; got != want {
		t.Errorf("CountAnswers = %d, want %d", got, want)
	}

	answers, err := sessionStore.ListAnswers(t.Context(), sess.ID, q.ID)
	if err != nil {
		t.Fatalf("ListAnswers err = %v, want nil", err)
	}
	if got, want := len(answers), 1; got != want {
		t.Fatalf("len(answers) = %d, want %d", got, want)
	}
	if got, want := answers[0].Correct, true; got != want {
		t.Errorf("answer Correct = %v, want %v", got, want)
	}
	if answers[0].Score != nil {
		t.Errorf("answer Score = %v, want nil before scoring", *answers[0].Score)
	}

	if err = sessionStore.SetAnswerScore(t.Context(), sess.ID, q.ID, p.ID, 800); err != nil {
		t.Fatalf("SetAnswerScore err = %v, want nil", err)
	}
	scored, err := sessionStore.ListAnswers(t.Context(), sess.ID, q.ID)
	if err != nil {
		t.Fatalf("ListAnswers err = %v, want nil", err)
	}
	if scored[0].Score == nil || *scored[0].Score != 800 {
		t.Errorf("scored answer Score = %v, want 800", scored[0].Score)
	}
}
