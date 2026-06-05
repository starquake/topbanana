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

// newTwoRoundLiveQuiz seeds a live quiz with two rounds (two questions then
// one), first option of each correct, loaded so round/question ids are
// resolved.
func newTwoRoundLiveQuiz(t *testing.T, qs *QuizStore) *quiz.Quiz {
	t.Helper()

	rw := func(pos int) *quiz.Question {
		return &quiz.Question{
			Text: "Q", Position: pos,
			Options: []*quiz.Option{{Text: "right", Correct: true}, {Text: "wrong"}},
		}
	}
	qz := &quiz.Quiz{
		Title:             "Standings Quiz",
		Slug:              "standings-quiz",
		Description:       "fixture",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Mode:              quiz.ModeLive,
		Rounds: []*quiz.Round{
			{Title: "R1", Questions: []*quiz.Question{rw(1), rw(2)}},
			{Title: "R2", Questions: []*quiz.Question{rw(3)}},
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

// seedFinishedSession opens a session for qz, joins playerID, records one
// answer to the quiz's first question, and finishes it - producing the
// finished-roster-row + recorded-pick shape the replay gate and the admin
// live reset operate on.
func seedFinishedSession(
	t *testing.T, sessionStore *LiveSessionStore, qz *quiz.Quiz, joinCode string, playerID int64,
) {
	t.Helper()

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: joinCode}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	if _, err := sessionStore.AddPlayer(t.Context(), sess.ID, playerID, "Player"); err != nil {
		t.Fatalf("AddPlayer err = %v, want nil", err)
	}
	q := qz.Questions[0]
	answeredAt := time.Date(2026, time.June, 5, 12, 0, 5, 0, time.UTC)
	if err := sessionStore.RecordAnswer(t.Context(), sess.ID, q.ID, playerID, q.Options[0].ID, answeredAt); err != nil {
		t.Fatalf("RecordAnswer err = %v, want nil", err)
	}
	if err := sessionStore.Finish(t.Context(), sess.ID); err != nil {
		t.Fatalf("Finish err = %v, want nil", err)
	}
}

// TestLiveSessionStore_Standings seeds two players who score across a
// two-round quiz and asserts the per-round and final standings: round_score
// scopes to the round, total_score is cumulative, non-answerers appear at 0,
// and rows come back best-first.
func TestLiveSessionStore_Standings(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newTwoRoundLiveQuiz(t, quizStore)
	r1q1, r1q2, r2q1 := qz.Questions[0], qz.Questions[1], qz.Questions[2]
	round1, round2 := r1q1.RoundID, r2q1.RoundID

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "STND23"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	at := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)

	winner, err := playerStore.CreateAnonymousPlayer(t.Context(), "stnd-win")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer winner err = %v, want nil", err)
	}
	loser, err := playerStore.CreateAnonymousPlayer(t.Context(), "stnd-los")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer loser err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, winner.ID, "Win"); err != nil {
		t.Fatalf("AddPlayer winner err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, loser.ID, "Los"); err != nil {
		t.Fatalf("AddPlayer loser err = %v, want nil", err)
	}

	// Winner scores 100 in each round-1 question and 50 in round 2; loser only
	// answers (and never scores) round-1 question 1.
	scoreAnswer(t, sessionStore, sess.ID, r1q1.ID, winner.ID, r1q1.Options[0].ID, at, 100)
	scoreAnswer(t, sessionStore, sess.ID, r1q2.ID, winner.ID, r1q2.Options[0].ID, at, 100)
	scoreAnswer(t, sessionStore, sess.ID, r2q1.ID, winner.ID, r2q1.Options[0].ID, at, 50)
	scoreAnswer(t, sessionStore, sess.ID, r1q1.ID, loser.ID, r1q1.Options[1].ID, at, 0)

	round1Standings, err := sessionStore.ListRoundStandings(t.Context(), sess.ID, round1)
	if err != nil {
		t.Fatalf("ListRoundStandings r1 err = %v, want nil", err)
	}
	if got, want := len(round1Standings), 2; got != want {
		t.Fatalf("round 1 standings = %d, want %d (full roster)", got, want)
	}
	// Best-first: winner leads.
	if got, want := round1Standings[0].PlayerID, winner.ID; got != want {
		t.Errorf("round 1 leader = %d, want %d", got, want)
	}
	if got, want := round1Standings[0].RoundScore, 200; got != want {
		t.Errorf("winner round 1 score = %d, want %d", got, want)
	}
	// All answers are seeded up front, so total_score is the cumulative session
	// total at query time (round 1's 200 plus round 2's 50); round_score is what
	// scopes to the round.
	if got, want := round1Standings[0].TotalScore, 250; got != want {
		t.Errorf("winner round 1 total = %d, want %d (cumulative across all rounds)", got, want)
	}
	if got, want := round1Standings[1].RoundScore, 0; got != want {
		t.Errorf("loser round 1 score = %d, want %d", got, want)
	}

	round2Standings, err := sessionStore.ListRoundStandings(t.Context(), sess.ID, round2)
	if err != nil {
		t.Fatalf("ListRoundStandings r2 err = %v, want nil", err)
	}
	// In round 2 the winner earned only 50, but the cumulative total carries
	// round 1 too.
	if got, want := round2Standings[0].RoundScore, 50; got != want {
		t.Errorf("winner round 2 score = %d, want %d", got, want)
	}
	if got, want := round2Standings[0].TotalScore, 250; got != want {
		t.Errorf("winner round 2 total = %d, want %d", got, want)
	}

	finalStandings, err := sessionStore.ListFinalStandings(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("ListFinalStandings err = %v, want nil", err)
	}
	if got, want := finalStandings[0].TotalScore, 250; got != want {
		t.Errorf("winner final total = %d, want %d", got, want)
	}
	if got, want := finalStandings[0].RoundScore, 0; got != want {
		t.Errorf("final RoundScore = %d, want 0 (no single round in focus)", got)
	}
	if got, want := finalStandings[1].TotalScore, 0; got != want {
		t.Errorf("loser final total = %d, want %d", got, want)
	}
}

// TestLiveSessionStore_EnterRoundResults pins the round_results phase
// transition: it moves the session into round_results and keeps the current
// round in place while clearing the per-question columns.
func TestLiveSessionStore_EnterRoundResults(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuizWithQuestion(t, quizStore)
	q := qz.Questions[0]

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "ERRS23"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	started := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	if err := sessionStore.EnterQuestion(
		t.Context(), sess.ID, q.RoundID, q.ID, started, started.Add(10*time.Second),
	); err != nil {
		t.Fatalf("EnterQuestion err = %v, want nil", err)
	}

	if err := sessionStore.EnterRoundResults(t.Context(), sess.ID); err != nil {
		t.Fatalf("EnterRoundResults err = %v, want nil", err)
	}
	got, err := sessionStore.GetSessionByID(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSessionByID err = %v, want nil", err)
	}
	if want := livesession.PhaseRoundResults; got.Phase != want {
		t.Errorf("phase = %q, want %q", got.Phase, want)
	}
	if got.CurrentRoundID == nil || *got.CurrentRoundID != q.RoundID {
		t.Errorf("CurrentRoundID = %v, want %d", got.CurrentRoundID, q.RoundID)
	}
	if got.CurrentQuestionID != nil {
		t.Errorf("round_results CurrentQuestionID = %v, want nil", *got.CurrentQuestionID)
	}
}

// TestGameStore_ListSessionResultsForQuizLeaderboard pins that only FINISHED
// sessions contribute to the quiz leaderboard results, with per-player scores
// summed.
func TestGameStore_ListSessionResultsForQuizLeaderboard(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	gameStore := NewGameStore(db, slog.Default())
	qz := newLiveQuizWithQuestion(t, quizStore)
	q := qz.Questions[0]
	at := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)

	player, err := playerStore.CreateAnonymousPlayer(t.Context(), "lb-sess-p1")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "LBSS23"}
	if err = sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, player.ID, "Pat"); err != nil {
		t.Fatalf("AddPlayer err = %v, want nil", err)
	}
	scoreAnswer(t, sessionStore, sess.ID, q.ID, player.ID, q.Options[0].ID, at, 700)

	// Not finished yet: no leaderboard contribution.
	before, err := gameStore.ListSessionResultsForQuizLeaderboard(t.Context(), qz.ID)
	if err != nil {
		t.Fatalf("ListSessionResultsForQuizLeaderboard before finish err = %v, want nil", err)
	}
	if got, want := len(before), 0; got != want {
		t.Errorf("session results before finish = %d, want %d (only finished sessions count)", got, want)
	}

	if err = sessionStore.Finish(t.Context(), sess.ID); err != nil {
		t.Fatalf("Finish err = %v, want nil", err)
	}

	after, err := gameStore.ListSessionResultsForQuizLeaderboard(t.Context(), qz.ID)
	if err != nil {
		t.Fatalf("ListSessionResultsForQuizLeaderboard after finish err = %v, want nil", err)
	}
	if got, want := len(after), 1; got != want {
		t.Fatalf("session results after finish = %d, want %d", got, want)
	}
	if got, want := after[0].PlayerID, player.ID; got != want {
		t.Errorf("result PlayerID = %d, want %d", got, want)
	}
	if got, want := after[0].Score, 700; got != want {
		t.Errorf("result Score = %d, want %d", got, want)
	}
	if got, want := after[0].DisplayName, "Pat"; got != want {
		t.Errorf("result DisplayName = %q, want %q", got, want)
	}
}

// scoreAnswer records a pick and immediately writes its score, the
// record-then-score-at-close sequence the runner performs.
func scoreAnswer(
	t *testing.T, s *LiveSessionStore, sessionID string, questionID, playerID, optionID int64,
	answeredAt time.Time, score int,
) {
	t.Helper()
	if err := s.RecordAnswer(t.Context(), sessionID, questionID, playerID, optionID, answeredAt); err != nil {
		t.Fatalf("RecordAnswer err = %v, want nil", err)
	}
	if err := s.SetAnswerScore(t.Context(), sessionID, questionID, playerID, score); err != nil {
		t.Fatalf("SetAnswerScore err = %v, want nil", err)
	}
}

func TestLiveSessionStore_PlayerFinishedSessionForQuiz(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuizWithQuestion(t, quizStore)

	player, err := playerStore.CreateAnonymousPlayer(t.Context(), "finished-p")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	// No participation yet: the gate is open.
	played, err := sessionStore.PlayerFinishedSessionForQuiz(t.Context(), player.ID, qz.ID)
	if err != nil {
		t.Fatalf("PlayerFinishedSessionForQuiz err = %v, want nil", err)
	}
	if got, want := played, false; got != want {
		t.Errorf("played (before) = %v, want %v", got, want)
	}

	seedFinishedSession(t, sessionStore, qz, "FINI23", player.ID)

	played, err = sessionStore.PlayerFinishedSessionForQuiz(t.Context(), player.ID, qz.ID)
	if err != nil {
		t.Fatalf("PlayerFinishedSessionForQuiz err = %v, want nil", err)
	}
	if got, want := played, true; got != want {
		t.Errorf("played (after finish) = %v, want %v", got, want)
	}
}

func TestLiveSessionStore_PlayerFinishedSessionForQuiz_UnfinishedDoesNotCount(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuizWithQuestion(t, quizStore)

	player, err := playerStore.CreateAnonymousPlayer(t.Context(), "lobby-p")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	// A roster row in a still-open (lobby) session must not trip the gate -
	// only a finished session blocks a replay.
	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "OPEN23"}
	if err = sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, player.ID, "Open"); err != nil {
		t.Fatalf("AddPlayer err = %v, want nil", err)
	}

	played, err := sessionStore.PlayerFinishedSessionForQuiz(t.Context(), player.ID, qz.ID)
	if err != nil {
		t.Fatalf("PlayerFinishedSessionForQuiz err = %v, want nil", err)
	}
	if got, want := played, false; got != want {
		t.Errorf("played = %v, want %v (unfinished session must not gate)", got, want)
	}
}

func TestPlayerStore_ListFinishedSessionPlaysForPlayer(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuizWithQuestion(t, quizStore)

	player, err := playerStore.CreateAnonymousPlayer(t.Context(), "plays-p")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	// Two finished sessions of the same quiz collapse to one play row.
	seedFinishedSession(t, sessionStore, qz, "PLAY01", player.ID)
	seedFinishedSession(t, sessionStore, qz, "PLAY02", player.ID)

	plays, err := playerStore.ListFinishedSessionPlaysForPlayer(t.Context(), player.ID, 20)
	if err != nil {
		t.Fatalf("ListFinishedSessionPlaysForPlayer err = %v, want nil", err)
	}
	if got, want := len(plays), 1; got != want {
		t.Fatalf("len(plays) = %d, want %d (one row per quiz)", got, want)
	}
	if got, want := plays[0].QuizID, qz.ID; got != want {
		t.Errorf("plays[0].QuizID = %d, want %d", got, want)
	}
	if got, want := plays[0].QuizTitle, qz.Title; got != want {
		t.Errorf("plays[0].QuizTitle = %q, want %q", got, want)
	}
	if plays[0].FinishedAt.IsZero() {
		t.Error("plays[0].FinishedAt is zero, want a timestamp")
	}
}

func TestPlayerStore_ResetLiveSessionPlaysForPlayerOnQuiz(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuizWithQuestion(t, quizStore)

	player, err := playerStore.CreateAnonymousPlayer(t.Context(), "reset-p")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	other, err := playerStore.CreateAnonymousPlayer(t.Context(), "reset-other")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer other err = %v, want nil", err)
	}

	seedFinishedSession(t, sessionStore, qz, "RES101", player.ID)
	seedFinishedSession(t, sessionStore, qz, "RES102", other.ID)

	if err = playerStore.ResetLiveSessionPlaysForPlayerOnQuiz(t.Context(), player.ID, qz.ID); err != nil {
		t.Fatalf("ResetLiveSessionPlaysForPlayerOnQuiz err = %v, want nil", err)
	}

	// The reset player's gate is clear and their rows are gone.
	played, err := sessionStore.PlayerFinishedSessionForQuiz(t.Context(), player.ID, qz.ID)
	if err != nil {
		t.Fatalf("PlayerFinishedSessionForQuiz err = %v, want nil", err)
	}
	if got, want := played, false; got != want {
		t.Errorf("played after reset = %v, want %v", got, want)
	}
	plays, err := playerStore.ListFinishedSessionPlaysForPlayer(t.Context(), player.ID, 20)
	if err != nil {
		t.Fatalf("ListFinishedSessionPlaysForPlayer err = %v, want nil", err)
	}
	if got, want := len(plays), 0; got != want {
		t.Errorf("len(plays) after reset = %d, want %d", got, want)
	}

	// Another player's participation in the same quiz is untouched.
	otherPlayed, err := sessionStore.PlayerFinishedSessionForQuiz(t.Context(), other.ID, qz.ID)
	if err != nil {
		t.Fatalf("PlayerFinishedSessionForQuiz other err = %v, want nil", err)
	}
	if got, want := otherPlayed, true; got != want {
		t.Errorf("other played after reset = %v, want %v (must be unaffected)", got, want)
	}
}
