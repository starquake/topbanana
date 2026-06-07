package store_test

import (
	"database/sql"
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

	p1, err := playerStore.CreateAnonymousPlayer(t.Context(), "Alice")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer p1 err = %v, want nil", err)
	}
	p2, err := playerStore.CreateAnonymousPlayer(t.Context(), "Bob")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer p2 err = %v, want nil", err)
	}

	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, p1.ID); err != nil {
		t.Fatalf("AddPlayer p1 err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, p2.ID); err != nil {
		t.Fatalf("AddPlayer p2 err = %v, want nil", err)
	}

	loaded, err := sessionStore.GetSessionByJoinCode(t.Context(), "ROST23")
	if err != nil {
		t.Fatalf("GetSessionByJoinCode err = %v, want nil", err)
	}
	if got, want := len(loaded.Players), 2; got != want {
		t.Fatalf("len(Players) = %d, want %d", got, want)
	}
	// The roster's DisplayName is the player's current players.display_name
	// (#716), not a per-session snapshot.
	if got, want := loaded.Players[0].DisplayName, "Alice"; got != want {
		t.Errorf("Players[0].DisplayName = %q, want %q (join order, current player name)", got, want)
	}
}

// TestLiveSessionStore_Roster_ReflectsPlayerRename pins the #716 propagation
// guarantee at the store layer: renaming the player's players.display_name
// changes what the live roster read returns, because the roster joins players
// rather than storing a per-session snapshot.
func TestLiveSessionStore_Roster_ReflectsPlayerRename(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuiz(t, quizStore)

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "RNAM23"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	p, err := playerStore.CreateAnonymousPlayer(t.Context(), "Before")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, p.ID); err != nil {
		t.Fatalf("AddPlayer err = %v, want nil", err)
	}

	if _, err = playerStore.RenamePlayer(t.Context(), p.ID, "After"); err != nil {
		t.Fatalf("RenamePlayer err = %v, want nil", err)
	}

	loaded, err := sessionStore.GetSessionByJoinCode(t.Context(), "RNAM23")
	if err != nil {
		t.Fatalf("GetSessionByJoinCode err = %v, want nil", err)
	}
	if got, want := len(loaded.Players), 1; got != want {
		t.Fatalf("len(Players) = %d, want %d", got, want)
	}
	if got, want := loaded.Players[0].DisplayName, "After"; got != want {
		t.Errorf("roster DisplayName after rename = %q, want %q (rename must propagate)", got, want)
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
	p1, err := playerStore.CreateAnonymousPlayer(t.Context(), "Rejoiner")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, p1.ID); err != nil {
		t.Fatalf("AddPlayer err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, p1.ID); err != nil {
		t.Fatalf("AddPlayer rejoin err = %v, want nil", err)
	}

	loaded, err := sessionStore.GetSessionByJoinCode(t.Context(), "REJN23")
	if err != nil {
		t.Fatalf("GetSessionByJoinCode err = %v, want nil", err)
	}
	if got, want := len(loaded.Players), 1; got != want {
		t.Errorf("len(Players) after rejoin = %d, want %d", got, want)
	}
	if got, want := loaded.Players[0].DisplayName, "Rejoiner"; got != want {
		t.Errorf("rejoin DisplayName = %q, want %q (current player name)", got, want)
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
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, p1.ID); err != nil {
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

func TestLiveSessionStore_ArmAndCancelStart(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuizWithQuestion(t, quizStore)

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "ARM234"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}

	// Arm stamps the deadline; the read carries it back.
	deadline := time.Date(2026, time.June, 5, 12, 1, 0, 0, time.UTC)
	if err := sessionStore.ArmStart(t.Context(), sess.ID, deadline); err != nil {
		t.Fatalf("ArmStart err = %v, want nil", err)
	}
	armed, err := sessionStore.GetSessionByID(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSessionByID err = %v, want nil", err)
	}
	if armed.StartAt == nil {
		t.Fatal("StartAt after ArmStart = nil, want the deadline")
	}
	if got, want := *armed.StartAt, deadline; !got.Equal(want) {
		t.Errorf("StartAt = %v, want %v", got, want)
	}

	// Cancel clears it.
	if err = sessionStore.CancelStart(t.Context(), sess.ID); err != nil {
		t.Fatalf("CancelStart err = %v, want nil", err)
	}
	cancelled, err := sessionStore.GetSessionByID(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSessionByID err = %v, want nil", err)
	}
	if cancelled.StartAt != nil {
		t.Errorf("StartAt after CancelStart = %v, want nil", *cancelled.StartAt)
	}

	// Once the session has left the lobby, arm/cancel are no-ops mapped to
	// ErrNotInLobby.
	if _, err = sessionStore.MarkStarted(t.Context(), sess.ID); err != nil {
		t.Fatalf("MarkStarted err = %v, want nil", err)
	}
	if got, want := sessionStore.ArmStart(
		t.Context(),
		sess.ID,
		deadline,
	), livesession.ErrNotInLobby; !errors.Is(
		got,
		want,
	) {
		t.Errorf("ArmStart past lobby err = %v, want %v", got, want)
	}
	if got, want := sessionStore.CancelStart(t.Context(), sess.ID), livesession.ErrNotInLobby; !errors.Is(got, want) {
		t.Errorf("CancelStart past lobby err = %v, want %v", got, want)
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
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, p.ID); err != nil {
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

// TestLiveSessionStore_RecordAnswer_RefreshesLastSeen pins the answer-as-
// liveness write (#712): recording a pick advances the player's last_seen_at to
// the answer's timestamp, so a player who answered counts active even without a
// held SSE heartbeat. The roster row starts backdated well before the answer.
func TestLiveSessionStore_RecordAnswer_RefreshesLastSeen(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuizWithQuestion(t, quizStore)
	q := qz.Questions[0]

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "RFLS23"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	p, err := playerStore.CreateAnonymousPlayer(t.Context(), "rfls-p1")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, p.ID); err != nil {
		t.Fatalf("AddPlayer err = %v, want nil", err)
	}

	answeredAt := time.Date(2026, time.June, 5, 12, 0, 5, 0, time.UTC)
	setLastSeen(t, db, sess.ID, p.ID, answeredAt.Add(-time.Hour))

	if err = sessionStore.RecordAnswer(t.Context(), sess.ID, q.ID, p.ID, q.Options[0].ID, answeredAt); err != nil {
		t.Fatalf("RecordAnswer err = %v, want nil", err)
	}

	loaded, err := sessionStore.GetSessionByJoinCode(t.Context(), "RFLS23")
	if err != nil {
		t.Fatalf("GetSessionByJoinCode err = %v, want nil", err)
	}
	const sqliteLayout = "2006-01-02 15:04:05"
	if got, want := loaded.Players[0].LastSeenAt.UTC().Format(sqliteLayout),
		answeredAt.UTC().Format(sqliteLayout); got != want {
		t.Errorf("LastSeenAt after RecordAnswer = %q, want %q (advanced to the answer time)", got, want)
	}
}

// TestLiveSessionStore_TouchLastSeen pins the heartbeat write: it refreshes a
// participant's last_seen_at keyed on join code, and reports a non-participant
// when no roster row matches.
func TestLiveSessionStore_TouchLastSeen(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuiz(t, quizStore)

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "TCH234"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	p, err := playerStore.CreateAnonymousPlayer(t.Context(), "touch-p1")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, p.ID); err != nil {
		t.Fatalf("AddPlayer err = %v, want nil", err)
	}

	// Backdate the roster row so the touch has an observable effect.
	stale := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	setLastSeen(t, db, sess.ID, p.ID, stale)

	if err = sessionStore.TouchLastSeen(t.Context(), "TCH234", p.ID); err != nil {
		t.Fatalf("TouchLastSeen err = %v, want nil", err)
	}
	loaded, err := sessionStore.GetSessionByJoinCode(t.Context(), "TCH234")
	if err != nil {
		t.Fatalf("GetSessionByJoinCode err = %v, want nil", err)
	}
	if !loaded.Players[0].LastSeenAt.After(stale) {
		t.Errorf("LastSeenAt = %v, want refreshed past the backdated %v", loaded.Players[0].LastSeenAt, stale)
	}

	// A player with no roster row is a non-participant.
	if err = sessionStore.TouchLastSeen(t.Context(), "TCH234", seededAdminID); !errors.Is(
		err, livesession.ErrNotParticipant,
	) {
		t.Errorf("TouchLastSeen non-participant err = %v, want %v", err, livesession.ErrNotParticipant)
	}
}

// TestLiveSessionStore_TouchHostLastSeen pins the host-presence write: it
// stamps host_last_seen_at (NULL on a fresh session) keyed on join code, the
// session read surfaces it, and an unknown code reports ErrSessionNotFound.
func TestLiveSessionStore_TouchHostLastSeen(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuiz(t, quizStore)

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "HST234"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}

	// A fresh session has never seen its host.
	fresh, err := sessionStore.GetSessionByID(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSessionByID err = %v, want nil", err)
	}
	if fresh.HostLastSeenAt != nil {
		t.Errorf("HostLastSeenAt on a fresh session = %v, want nil", *fresh.HostLastSeenAt)
	}

	if err = sessionStore.TouchHostLastSeen(t.Context(), "HST234"); err != nil {
		t.Fatalf("TouchHostLastSeen err = %v, want nil", err)
	}
	loaded, err := sessionStore.GetSessionByID(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSessionByID after touch err = %v, want nil", err)
	}
	if loaded.HostLastSeenAt == nil {
		t.Fatal("HostLastSeenAt after touch = nil, want a timestamp")
	}

	// An unknown code matches no session.
	if err = sessionStore.TouchHostLastSeen(t.Context(), "NOPE99"); !errors.Is(
		err, livesession.ErrSessionNotFound,
	) {
		t.Errorf("TouchHostLastSeen unknown code err = %v, want %v", err, livesession.ErrSessionNotFound)
	}
}

// TestLiveSessionStore_ActiveCounts pins the active-player counts across the
// last_seen_at window boundary: a fresh player is counted active and counted
// unanswered until they pick, while a player whose last_seen_at is before the
// cutoff is excluded from both counts. This is what lets the runner early-close
// once every still-active player has answered, ignoring a dropped player.
func TestLiveSessionStore_ActiveCounts(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuizWithQuestion(t, quizStore)
	q := qz.Questions[0]

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "ACTV23"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}

	fresh, err := playerStore.CreateAnonymousPlayer(t.Context(), "actv-fresh")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer fresh err = %v, want nil", err)
	}
	stalePlayer, err := playerStore.CreateAnonymousPlayer(t.Context(), "actv-stale")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer stale err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, fresh.ID); err != nil {
		t.Fatalf("AddPlayer fresh err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, stalePlayer.ID); err != nil {
		t.Fatalf("AddPlayer stale err = %v, want nil", err)
	}

	now := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	since := now.Add(-30 * time.Second)
	// Fresh player beats just inside the window; stale player last beat well
	// before the cutoff.
	setLastSeen(t, db, sess.ID, fresh.ID, now.Add(-5*time.Second))
	setLastSeen(t, db, sess.ID, stalePlayer.ID, now.Add(-10*time.Minute))

	active, err := sessionStore.CountActive(t.Context(), sess.ID, since)
	if err != nil {
		t.Fatalf("CountActive err = %v, want nil", err)
	}
	if got, want := active, 1; got != want {
		t.Errorf("CountActive = %d, want %d (only the fresh player)", got, want)
	}

	unanswered, err := sessionStore.CountActiveUnanswered(t.Context(), sess.ID, q.ID, since)
	if err != nil {
		t.Fatalf("CountActiveUnanswered err = %v, want nil", err)
	}
	if got, want := unanswered, 1; got != want {
		t.Errorf("CountActiveUnanswered = %d, want %d (fresh, not yet answered)", got, want)
	}

	// Once the fresh player answers, no active player is unanswered.
	if err = sessionStore.RecordAnswer(t.Context(), sess.ID, q.ID, fresh.ID, q.Options[0].ID, now); err != nil {
		t.Fatalf("RecordAnswer err = %v, want nil", err)
	}
	unanswered, err = sessionStore.CountActiveUnanswered(t.Context(), sess.ID, q.ID, since)
	if err != nil {
		t.Fatalf("CountActiveUnanswered after answer err = %v, want nil", err)
	}
	if got, want := unanswered, 0; got != want {
		t.Errorf("CountActiveUnanswered after answer = %d, want %d (early-close trigger)", got, want)
	}
}

// TestLiveSessionStore_ActiveCounts_RealTimestampEncoding guards the
// cross-format comparison trap: a roster row stamped by the production write
// path (TouchLastSeen -> SQLite CURRENT_TIMESTAMP text) must be counted active
// against a cutoff derived from a Go time.Time. The store formats the cutoff in
// the CURRENT_TIMESTAMP encoding so the comparison is a same-format string
// compare; binding the time.Time directly would arrive in a different format
// and silently exclude every real row.
func TestLiveSessionStore_ActiveCounts_RealTimestampEncoding(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuiz(t, quizStore)

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "RENC23"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	p, err := playerStore.CreateAnonymousPlayer(t.Context(), "renc-p1")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, p.ID); err != nil {
		t.Fatalf("AddPlayer err = %v, want nil", err)
	}
	// Stamp last_seen_at via the production heartbeat path (CURRENT_TIMESTAMP).
	if err = sessionStore.TouchLastSeen(t.Context(), "RENC23", p.ID); err != nil {
		t.Fatalf("TouchLastSeen err = %v, want nil", err)
	}

	// A cutoff one window in the real past must still count the just-touched
	// player as active. time.Now() (not a fixed literal) keeps it aligned with
	// the wall-clock CURRENT_TIMESTAMP the heartbeat just wrote.
	since := time.Now().UTC().Add(-30 * time.Second)
	active, err := sessionStore.CountActive(t.Context(), sess.ID, since)
	if err != nil {
		t.Fatalf("CountActive err = %v, want nil", err)
	}
	if got, want := active, 1; got != want {
		t.Errorf("CountActive = %d, want %d (real CURRENT_TIMESTAMP row must compare active)", got, want)
	}
}

// setLastSeen backdates a roster row's last_seen_at so a test can place a
// player on either side of the active-window cutoff. Writes the timestamp in
// SQLite's CURRENT_TIMESTAMP text format ('YYYY-MM-DD HH:MM:SS') - the same
// encoding production rows are stamped in - so the store's string comparison
// against the formatted cutoff is exercised faithfully. Test-only fixture write.
func setLastSeen(t *testing.T, db *sql.DB, sessionID string, playerID int64, at time.Time) {
	t.Helper()
	if _, err := db.ExecContext(
		t.Context(),
		"UPDATE session_players SET last_seen_at = ? WHERE session_id = ? AND player_id = ?",
		at.UTC().Format("2006-01-02 15:04:05"), sessionID, playerID,
	); err != nil {
		t.Fatalf("setLastSeen err = %v, want nil", err)
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
	if _, err := sessionStore.AddPlayer(t.Context(), sess.ID, playerID); err != nil {
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
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, winner.ID); err != nil {
		t.Fatalf("AddPlayer winner err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, loser.ID); err != nil {
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
	// The standings carry the player's current players.display_name, set on
	// creation here.
	if got, want := finalStandings[0].DisplayName, "stnd-win"; got != want {
		t.Errorf("winner standings DisplayName = %q, want %q (current player name)", got, want)
	}

	// #716 propagation: renaming the winner updates what the standings reads
	// return, because they join players rather than store a per-session name.
	if _, err = playerStore.RenamePlayer(t.Context(), winner.ID, "Champion"); err != nil {
		t.Fatalf("RenamePlayer err = %v, want nil", err)
	}
	renamedRound, err := sessionStore.ListRoundStandings(t.Context(), sess.ID, round1)
	if err != nil {
		t.Fatalf("ListRoundStandings after rename err = %v, want nil", err)
	}
	if got, want := renamedRound[0].DisplayName, "Champion"; got != want {
		t.Errorf("round standings DisplayName after rename = %q, want %q (rename must propagate)", got, want)
	}
	renamedFinal, err := sessionStore.ListFinalStandings(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("ListFinalStandings after rename err = %v, want nil", err)
	}
	if got, want := renamedFinal[0].DisplayName, "Champion"; got != want {
		t.Errorf("final standings DisplayName after rename = %q, want %q (rename must propagate)", got, want)
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

// TestLiveSessionStore_MarkPlayerLeft_ExcludesFromRoster pins MP-10: after a
// player leaves, their roster row drops out of the lobby read, while a player
// who stayed remains. A second leave is a no-op that reports
// ErrNotParticipant (the active row is already gone).
func TestLiveSessionStore_MarkPlayerLeft_ExcludesFromRoster(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuiz(t, quizStore)

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "LEFT23"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}

	stayer, err := playerStore.CreateAnonymousPlayer(t.Context(), "leave-stay")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer stayer err = %v, want nil", err)
	}
	leaver, err := playerStore.CreateAnonymousPlayer(t.Context(), "leave-go")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer leaver err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, stayer.ID); err != nil {
		t.Fatalf("AddPlayer stayer err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, leaver.ID); err != nil {
		t.Fatalf("AddPlayer leaver err = %v, want nil", err)
	}

	if err = sessionStore.MarkPlayerLeft(t.Context(), "LEFT23", leaver.ID); err != nil {
		t.Fatalf("MarkPlayerLeft err = %v, want nil", err)
	}

	loaded, err := sessionStore.GetSessionByJoinCode(t.Context(), "LEFT23")
	if err != nil {
		t.Fatalf("GetSessionByJoinCode err = %v, want nil", err)
	}
	if got, want := len(loaded.Players), 1; got != want {
		t.Fatalf("len(Players) after leave = %d, want %d", got, want)
	}
	if got, want := loaded.Players[0].PlayerID, stayer.ID; got != want {
		t.Errorf("remaining player = %d, want %d (the stayer)", got, want)
	}

	// A repeat leave matches no active row and is a no-op.
	if got, want := sessionStore.MarkPlayerLeft(
		t.Context(),
		"LEFT23",
		leaver.ID,
	), livesession.ErrNotParticipant; !errors.Is(
		got,
		want,
	) {
		t.Errorf("repeat MarkPlayerLeft err = %v, want %v", got, want)
	}
}

// TestLiveSessionStore_MarkPlayerLeft_NotParticipant pins that a leave from a
// player who never joined reports ErrNotParticipant rather than silently
// succeeding.
func TestLiveSessionStore_MarkPlayerLeft_NotParticipant(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuiz(t, quizStore)

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "NLVE23"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}

	if got, want := sessionStore.MarkPlayerLeft(
		t.Context(),
		"NLVE23",
		seededAdminID,
	), livesession.ErrNotParticipant; !errors.Is(
		got,
		want,
	) {
		t.Errorf("MarkPlayerLeft non-member err = %v, want %v", got, want)
	}
}

// TestLiveSessionStore_MarkPlayerLeft_KeepsPlayedInStandings pins #766: the
// standings are a stable record of everyone who played, so a player who answered
// and then left (closed their browser) keeps their score on both the round and
// final boards across a TV refresh, while their score is unchanged. A player who
// left without ever answering never played and so drops off. ListAnswers, which
// also backs scoring at close, deliberately still returns the left player's pick
// so their score is recorded (MP-10 decision 3); the answered-order badges drop
// left players at the display layer instead.
func TestLiveSessionStore_MarkPlayerLeft_KeepsPlayedInStandings(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newTwoRoundLiveQuiz(t, quizStore)
	r1q1 := qz.Questions[0]
	round1 := r1q1.RoundID

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "LSTN23"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	at := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)

	stayer, err := playerStore.CreateAnonymousPlayer(t.Context(), "lstn-stay")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer stayer err = %v, want nil", err)
	}
	// playedLeaver answered before leaving; lobbyLeaver left without answering.
	playedLeaver, err := playerStore.CreateAnonymousPlayer(t.Context(), "lstn-played")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer playedLeaver err = %v, want nil", err)
	}
	lobbyLeaver, err := playerStore.CreateAnonymousPlayer(t.Context(), "lstn-lobby")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer lobbyLeaver err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, stayer.ID); err != nil {
		t.Fatalf("AddPlayer stayer err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, playedLeaver.ID); err != nil {
		t.Fatalf("AddPlayer playedLeaver err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, lobbyLeaver.ID); err != nil {
		t.Fatalf("AddPlayer lobbyLeaver err = %v, want nil", err)
	}

	// Stayer and playedLeaver both answer and score round-1 question 1; the
	// playedLeaver out-scores the stayer. lobbyLeaver never answers.
	scoreAnswer(t, sessionStore, sess.ID, r1q1.ID, stayer.ID, r1q1.Options[0].ID, at, 100)
	scoreAnswer(t, sessionStore, sess.ID, r1q1.ID, playedLeaver.ID, r1q1.Options[0].ID, at, 200)

	if err = sessionStore.MarkPlayerLeft(t.Context(), "LSTN23", playedLeaver.ID); err != nil {
		t.Fatalf("MarkPlayerLeft playedLeaver err = %v, want nil", err)
	}
	if err = sessionStore.MarkPlayerLeft(t.Context(), "LSTN23", lobbyLeaver.ID); err != nil {
		t.Fatalf("MarkPlayerLeft lobbyLeaver err = %v, want nil", err)
	}

	roundStandings, err := sessionStore.ListRoundStandings(t.Context(), sess.ID, round1)
	if err != nil {
		t.Fatalf("ListRoundStandings err = %v, want nil", err)
	}
	// The stayer and the player who answered before leaving both appear; the
	// player who left without answering does not.
	if got, want := len(roundStandings), 2; got != want {
		t.Fatalf("round standings after leave = %d, want %d (stayer + played leaver)", got, want)
	}
	if got, want := roundStandings[0].PlayerID, playedLeaver.ID; got != want {
		t.Errorf("round standings leader = %d, want %d (the played leaver, top score)", got, want)
	}
	if got, want := roundStandings[0].RoundScore, 200; got != want {
		t.Errorf("played leaver round score = %d, want %d (score survives the leave)", got, want)
	}
	if got, want := roundStandings[1].PlayerID, stayer.ID; got != want {
		t.Errorf("round standings runner-up = %d, want %d (the stayer)", got, want)
	}

	finalStandings, err := sessionStore.ListFinalStandings(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("ListFinalStandings err = %v, want nil", err)
	}
	if got, want := len(finalStandings), 2; got != want {
		t.Fatalf("final standings after leave = %d, want %d (stayer + played leaver)", got, want)
	}
	if got, want := finalStandings[0].PlayerID, playedLeaver.ID; got != want {
		t.Errorf("final standings leader = %d, want %d (the played leaver)", got, want)
	}
	if got, want := finalStandings[0].TotalScore, 200; got != want {
		t.Errorf("played leaver final total = %d, want %d (score survives the leave)", got, want)
	}

	// ListAnswers also backs scoring at close, so it must NOT drop the left
	// player: their recorded pick stays readable so the runner's score survives a
	// mid-question leave (the stayer's and the played leaver's, two picks).
	answers, err := sessionStore.ListAnswers(t.Context(), sess.ID, r1q1.ID)
	if err != nil {
		t.Fatalf("ListAnswers err = %v, want nil", err)
	}
	if got, want := len(answers), 2; got != want {
		t.Fatalf("answers after leave = %d, want %d (both pickers, scoring read is unfiltered)", got, want)
	}
}

// TestLiveSessionStore_SessionHasPlayer pins the reconnect/resume gate read: it
// reports true for a player who holds a roster row, stays true after that
// player is marked left (unlike the live roster, it is NOT filtered by
// left_at), and reports false for a player who never joined.
func TestLiveSessionStore_SessionHasPlayer(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	quizStore := NewQuizStore(db, slog.Default())
	playerStore := NewPlayerStore(db, slog.Default())
	sessionStore := NewLiveSessionStore(db, slog.Default())
	qz := newLiveQuiz(t, quizStore)

	sess := &livesession.Session{QuizID: qz.ID, HostPlayerID: seededAdminID, JoinCode: "HASP23"}
	if err := sessionStore.CreateSession(t.Context(), sess); err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}

	joined, err := playerStore.CreateAnonymousPlayer(t.Context(), "hasp-joined")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer joined err = %v, want nil", err)
	}
	stranger, err := playerStore.CreateAnonymousPlayer(t.Context(), "hasp-stranger")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer stranger err = %v, want nil", err)
	}
	if _, err = sessionStore.AddPlayer(t.Context(), sess.ID, joined.ID); err != nil {
		t.Fatalf("AddPlayer err = %v, want nil", err)
	}

	has, err := sessionStore.SessionHasPlayer(t.Context(), "HASP23", joined.ID)
	if err != nil {
		t.Fatalf("SessionHasPlayer joined err = %v, want nil", err)
	}
	if !has {
		t.Error("SessionHasPlayer joined = false, want true")
	}

	// After leaving, the row is still present (left_at stamped), so the resume
	// gate still sees the player even though the live roster excludes them.
	if err = sessionStore.MarkPlayerLeft(t.Context(), "HASP23", joined.ID); err != nil {
		t.Fatalf("MarkPlayerLeft err = %v, want nil", err)
	}
	has, err = sessionStore.SessionHasPlayer(t.Context(), "HASP23", joined.ID)
	if err != nil {
		t.Fatalf("SessionHasPlayer after leave err = %v, want nil", err)
	}
	if !has {
		t.Error("SessionHasPlayer after leave = false, want true (left_at is not filtered)")
	}

	// A player who never joined matches no row.
	has, err = sessionStore.SessionHasPlayer(t.Context(), "HASP23", stranger.ID)
	if err != nil {
		t.Fatalf("SessionHasPlayer stranger err = %v, want nil", err)
	}
	if has {
		t.Error("SessionHasPlayer stranger = true, want false (never joined)")
	}
}
