package livesession_test

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/quiz"
)

// joinCodePattern matches a code over the ambiguity-free alphabet: no
// 0/O/1/I/L, six characters. Pinned so the generator can't silently drift
// to a confusable alphabet.
var joinCodePattern = regexp.MustCompile(`^[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{6}$`)

func TestGenerateJoinCode_Shape(t *testing.T) {
	t.Parallel()

	for range 1000 {
		code := GenerateJoinCode()
		if !joinCodePattern.MatchString(code) {
			t.Fatalf("GenerateJoinCode() = %q, want match %s", code, joinCodePattern)
		}
	}
}

func TestGenerateJoinCode_NoAmbiguousCharacters(t *testing.T) {
	t.Parallel()

	const ambiguous = "01OIL"
	for range 1000 {
		code := GenerateJoinCode()
		for _, c := range ambiguous {
			for _, got := range code {
				if got == c {
					t.Fatalf("GenerateJoinCode() = %q contains ambiguous %q", code, c)
				}
			}
		}
	}
}

func TestGenerateJoinCode_Distinct(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, 1000)
	for range 1000 {
		seen[GenerateJoinCode()] = true
	}
	// 31^6 combinations make near-zero collisions across 1000 draws; a low
	// distinct count would mean a broken generator (e.g. a fixed seed).
	if got, want := len(seen), 990; got < want {
		t.Errorf("distinct codes over 1000 draws = %d, want >= %d", got, want)
	}
}

// fakeStore is a fault-injection double for the service tests: a real
// store cannot force a join-code probe collision on demand. It is not a
// tautological restatement of the store - it injects specific failure
// sequences the real store cannot reproduce deterministically.
type fakeStore struct {
	mu sync.Mutex

	existingCodes map[string]bool
	createErr     error
	createdCodes  []string

	// session returned by GetSessionByJoinCode; nil yields
	// ErrSessionNotFound.
	session *Session

	// addedPlayerIDs records the player ids passed to AddPlayer so a test can
	// assert a roster row was (or was not) written.
	addedPlayerIDs []int64

	setReadyErr error

	// markLeftErr is what MarkPlayerLeft reports, so a test can drive the
	// not-a-participant branch of Leave without a real roster row.
	markLeftErr error
}

func (*fakeStore) Ping(context.Context) error { return nil }

func (f *fakeStore) JoinCodeExists(_ context.Context, code string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.existingCodes[code], nil
}

func (f *fakeStore) CreateSession(_ context.Context, s *Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.createErr != nil {
		return f.createErr
	}
	f.createdCodes = append(f.createdCodes, s.JoinCode)
	s.ID = "sess-" + strconv.Itoa(len(f.createdCodes))
	s.Phase = PhaseLobby

	return nil
}

func (f *fakeStore) GetSessionByJoinCode(_ context.Context, _ string) (*Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.session == nil {
		return nil, ErrSessionNotFound
	}

	return f.session, nil
}

func (f *fakeStore) AddPlayer(_ context.Context, _ string, playerID int64) (*Player, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.addedPlayerIDs = append(f.addedPlayerIDs, playerID)

	return &Player{PlayerID: playerID}, nil
}

func (f *fakeStore) SetReady(context.Context, string, int64, bool) error {
	return f.setReadyErr
}

// The runner-facing Store methods below are exercised by the runner's
// integration tests against a real DB; this fault-injection double only
// covers the lobby service paths, so they return ErrUnsupported to fail
// loudly if a lobby test path ever reaches them unexpectedly.

func (f *fakeStore) GetSessionByID(_ context.Context, _ string) (*Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.session == nil {
		return nil, ErrSessionNotFound
	}

	return f.session, nil
}

func (*fakeStore) MarkStarted(context.Context, string) (bool, error) {
	return false, errors.ErrUnsupported
}

func (*fakeStore) ArmStart(context.Context, string, time.Time) error {
	return errors.ErrUnsupported
}

func (*fakeStore) CancelStart(context.Context, string) error {
	return errors.ErrUnsupported
}

func (*fakeStore) EnterRoundIntro(context.Context, string, int64) error {
	return errors.ErrUnsupported
}

func (*fakeStore) EnterQuestion(context.Context, string, int64, int64, time.Time, time.Time) error {
	return errors.ErrUnsupported
}

func (*fakeStore) EnterReveal(context.Context, string) error { return errors.ErrUnsupported }

func (*fakeStore) EnterRoundResults(context.Context, string) error { return errors.ErrUnsupported }

func (*fakeStore) Finish(context.Context, string) error { return errors.ErrUnsupported }

func (*fakeStore) Intermission(context.Context, string) error { return errors.ErrUnsupported }

func (*fakeStore) RearmSession(context.Context, string, int64) error { return errors.ErrUnsupported }

func (*fakeStore) RecordAnswer(context.Context, string, int64, int64, int64, time.Time) error {
	return errors.ErrUnsupported
}

func (*fakeStore) TouchLastSeen(context.Context, string, int64) error {
	return errors.ErrUnsupported
}

func (*fakeStore) TouchHostLastSeen(context.Context, string) error {
	return errors.ErrUnsupported
}

func (f *fakeStore) MarkPlayerLeft(context.Context, string, int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.markLeftErr
}

func (*fakeStore) CountActiveUnanswered(context.Context, string, int64, time.Time) (int, error) {
	return 0, errors.ErrUnsupported
}

func (*fakeStore) CountActive(context.Context, string, time.Time) (int, error) {
	return 0, errors.ErrUnsupported
}

func (*fakeStore) ListAnswers(context.Context, string, int64) ([]*SessionAnswer, error) {
	return nil, errors.ErrUnsupported
}

func (*fakeStore) SetAnswerScore(context.Context, string, int64, int64, int) error {
	return errors.ErrUnsupported
}

func (*fakeStore) ListLiveSessionIDs(context.Context) ([]string, error) {
	return nil, errors.ErrUnsupported
}

func (*fakeStore) ListRoundStandings(context.Context, string, int64) ([]*Standing, error) {
	return nil, errors.ErrUnsupported
}

func (*fakeStore) ListFinalStandings(context.Context, string) ([]*Standing, error) {
	return nil, errors.ErrUnsupported
}

// fakeQuiz returns the configured quiz or ErrQuizNotFound when nil, and the
// configured rounds (in position order) for the round_intro read.
type fakeQuiz struct {
	quiz   *quiz.Quiz
	rounds []*quiz.Round
}

func (f *fakeQuiz) GetQuiz(_ context.Context, _ int64) (*quiz.Quiz, error) {
	if f.quiz == nil {
		return nil, quiz.ErrQuizNotFound
	}

	return f.quiz, nil
}

func (f *fakeQuiz) ListRoundsByQuiz(_ context.Context, _ int64) ([]*quiz.Round, error) {
	return f.rounds, nil
}

func TestService_CreateSession_RegeneratesOnCodeCollision(t *testing.T) {
	t.Parallel()

	// First two generated codes already exist; the third is free, so the
	// service must regenerate past the collisions and create with the third.
	codes := []string{"TAKEN1", "TAKEN2", "FREE34"}
	var i int
	gen := func() string {
		c := codes[i]
		i++

		return c
	}
	store := &fakeStore{existingCodes: map[string]bool{"TAKEN1": true, "TAKEN2": true}}
	quizzes := &fakeQuiz{quiz: &quiz.Quiz{ID: 7, Mode: quiz.ModeLive}}
	svc := ExportNewServiceWithCodeGen(store, quizzes, slog.Default(), gen, 8)

	sess, err := svc.CreateSession(t.Context(), 7, 1)
	if err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}
	if got, want := sess.JoinCode, "FREE34"; got != want {
		t.Errorf("JoinCode = %q, want %q (regenerated past collisions)", got, want)
	}
}

func TestService_CreateSession_ExhaustsCodeBudget(t *testing.T) {
	t.Parallel()

	// Every candidate collides and the budget is small, so the service
	// gives up with ErrJoinCodeUnavailable rather than looping forever.
	gen := func() string { return "ALWAYS" }
	store := &fakeStore{existingCodes: map[string]bool{"ALWAYS": true}}
	quizzes := &fakeQuiz{quiz: &quiz.Quiz{ID: 7, Mode: quiz.ModeLive}}
	svc := ExportNewServiceWithCodeGen(store, quizzes, slog.Default(), gen, 3)

	_, err := svc.CreateSession(t.Context(), 7, 1)
	if got, want := err, ErrJoinCodeUnavailable; !errors.Is(got, want) {
		t.Errorf("CreateSession exhausted err = %v, want %v", got, want)
	}
}

// TestService_Join_AddsRosterRowWithoutName pins the nameless join contract
// (#716): Join carries no display name (the player is already named on their
// players row before joining), so it just adds the roster row for the player id.
func TestService_Join_AddsRosterRowWithoutName(t *testing.T) {
	t.Parallel()

	store := &fakeStore{session: &Session{ID: "s1", JoinCode: "ROOM12", Phase: PhaseLobby}}
	svc := NewService(store, &fakeQuiz{}, slog.Default())

	player, err := svc.Join(t.Context(), "ROOM12", 5)
	if err != nil {
		t.Fatalf("Join err = %v, want nil", err)
	}
	if got, want := player.PlayerID, int64(5); got != want {
		t.Errorf("Join PlayerID = %d, want %d", got, want)
	}
	if got, want := store.addedPlayerIDs, []int64{5}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("addedPlayerIDs = %v, want %v", got, want)
	}
}

// TestService_Join_AllowsLatecomerMidGame pins the open-room join (#836): a
// player who never held a roster row may still Join while a game is in flight
// (a non-lobby, non-finished phase) - they simply miss the questions already
// played. The v1 lobby-only gate is gone; AddPlayer adds (or, for a returning
// player whose row is marked left_at, revives) the roster row.
func TestService_Join_AllowsLatecomerMidGame(t *testing.T) {
	t.Parallel()

	store := &fakeStore{
		session: &Session{ID: "s1", QuizID: 7, JoinCode: "ROOM12", Phase: PhaseQuestion},
	}
	svc := NewService(store, &fakeQuiz{}, slog.Default())
	svc.SetPublisher(&spyPublisher{})

	if _, err := svc.Join(t.Context(), "ROOM12", 5); err != nil {
		t.Fatalf("Join (mid-game latecomer) err = %v, want nil", err)
	}
	if got, want := len(store.addedPlayerIDs), 1; got != want {
		t.Errorf("addedPlayerIDs len = %d, want %d (latecomer joins mid-game)", got, want)
	}
}

// TestService_Join_RejectsFinishedRoom pins that the only closed state is the
// terminal finished room (#836): a Join attempt there returns ErrLobbyClosed
// before touching the roster.
func TestService_Join_RejectsFinishedRoom(t *testing.T) {
	t.Parallel()

	store := &fakeStore{
		session: &Session{ID: "s1", QuizID: 7, JoinCode: "ROOM12", Phase: PhaseFinished},
	}
	svc := NewService(store, &fakeQuiz{}, slog.Default())

	_, err := svc.Join(t.Context(), "ROOM12", 5)
	if got, want := err, ErrLobbyClosed; !errors.Is(got, want) {
		t.Errorf("Join err = %v, want %v", got, want)
	}
	// The gate fires before the roster write, so no AddPlayer happened.
	if got, want := len(store.addedPlayerIDs), 0; got != want {
		t.Errorf("addedPlayerIDs len = %d, want %d (gate must precede AddPlayer)", got, want)
	}
}

// spyPublisher records the (code, phase) of each publish so a test can
// assert a lobby mutation fanned out exactly one tick. It is an outbound
// spy, not a tautological store double.
type spyPublisher struct {
	mu     sync.Mutex
	codes  []string
	phases []Phase
}

func (p *spyPublisher) Publish(code string, phase Phase) Tick {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.codes = append(p.codes, code)
	p.phases = append(p.phases, phase)

	return Tick{Version: uint64(len(p.codes)), Phase: phase}
}

// Forget satisfies Publisher; the service never calls it (only the runner does
// at finish), so the spy need only record nothing here.
func (*spyPublisher) Forget(_ string) {}

func TestService_Join_PublishesTick(t *testing.T) {
	t.Parallel()

	store := &fakeStore{session: &Session{ID: "s1", JoinCode: "ROOM12", Phase: PhaseLobby}}
	spy := &spyPublisher{}
	svc := NewService(store, &fakeQuiz{}, slog.Default())
	svc.SetPublisher(spy)

	if _, err := svc.Join(t.Context(), "room12", 5); err != nil {
		t.Fatalf("Join err = %v, want nil", err)
	}

	spy.mu.Lock()
	defer spy.mu.Unlock()
	if got, want := len(spy.codes), 1; got != want {
		t.Fatalf("publish count = %d, want %d (join must publish exactly one tick)", got, want)
	}
	// Publish uses the canonical code off the session, not the raw input.
	if got, want := spy.codes[0], "ROOM12"; got != want {
		t.Errorf("published code = %q, want %q (canonical join code)", got, want)
	}
	if got, want := spy.phases[0], PhaseLobby; got != want {
		t.Errorf("published phase = %q, want %q", got, want)
	}
}

func TestService_SetReady_PublishesTick(t *testing.T) {
	t.Parallel()

	store := &fakeStore{session: &Session{ID: "s1", JoinCode: "ROOM12", Phase: PhaseLobby}}
	spy := &spyPublisher{}
	svc := NewService(store, &fakeQuiz{}, slog.Default())
	svc.SetPublisher(spy)

	if err := svc.SetReady(t.Context(), "ROOM12", 5, true); err != nil {
		t.Fatalf("SetReady err = %v, want nil", err)
	}

	spy.mu.Lock()
	defer spy.mu.Unlock()
	if got, want := len(spy.codes), 1; got != want {
		t.Fatalf("publish count = %d, want %d (ready must publish exactly one tick)", got, want)
	}
	if got, want := spy.phases[0], PhaseLobby; got != want {
		t.Errorf("published phase = %q, want %q", got, want)
	}
}

func TestService_Leave_PublishesTick(t *testing.T) {
	t.Parallel()

	store := &fakeStore{session: &Session{ID: "s1", JoinCode: "ROOM12", Phase: PhaseQuestion}}
	spy := &spyPublisher{}
	svc := NewService(store, &fakeQuiz{}, slog.Default())
	svc.SetPublisher(spy)

	if err := svc.Leave(t.Context(), "room12", 5); err != nil {
		t.Fatalf("Leave err = %v, want nil", err)
	}

	spy.mu.Lock()
	defer spy.mu.Unlock()
	if got, want := len(spy.codes), 1; got != want {
		t.Fatalf("publish count = %d, want %d (leave must publish exactly one tick)", got, want)
	}
	// Publish uses the canonical code off the session, not the raw input.
	if got, want := spy.codes[0], "ROOM12"; got != want {
		t.Errorf("published code = %q, want %q (canonical join code)", got, want)
	}
	if got, want := spy.phases[0], PhaseQuestion; got != want {
		t.Errorf("published phase = %q, want %q", got, want)
	}
}

func TestService_Leave_NotParticipant(t *testing.T) {
	t.Parallel()

	store := &fakeStore{
		session:     &Session{ID: "s1", JoinCode: "ROOM12", Phase: PhaseLobby},
		markLeftErr: ErrNotParticipant,
	}
	spy := &spyPublisher{}
	svc := NewService(store, &fakeQuiz{}, slog.Default())
	svc.SetPublisher(spy)

	if got, want := svc.Leave(t.Context(), "ROOM12", 5), ErrNotParticipant; !errors.Is(got, want) {
		t.Errorf("Leave err = %v, want %v", got, want)
	}

	spy.mu.Lock()
	defer spy.mu.Unlock()
	if got, want := len(spy.codes), 0; got != want {
		t.Errorf("publish count = %d, want %d (a failed leave must not publish)", got, want)
	}
}

func TestService_Leave_SessionNotFound(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	svc := NewService(store, &fakeQuiz{}, slog.Default())

	if got, want := svc.Leave(t.Context(), "NOPE12", 5), ErrSessionNotFound; !errors.Is(got, want) {
		t.Errorf("Leave err = %v, want %v", got, want)
	}
}

func TestService_AuthorizeView_GatesNonParticipants(t *testing.T) {
	t.Parallel()

	sess := &Session{
		ID:           "s1",
		JoinCode:     "ROOM12",
		Phase:        PhaseLobby,
		HostPlayerID: 1,
		Players:      []*Player{{PlayerID: 5}},
	}
	store := &fakeStore{session: sess}
	svc := NewService(store, &fakeQuiz{}, slog.Default())

	// Host passes and gets the canonical code + phase + host flag back.
	view, err := svc.AuthorizeView(t.Context(), "room12", 1)
	if err != nil {
		t.Fatalf("AuthorizeView host err = %v, want nil", err)
	}
	if got, want := view.Code, "ROOM12"; got != want {
		t.Errorf("AuthorizeView code = %q, want %q (canonical)", got, want)
	}
	if got, want := view.Phase, PhaseLobby; got != want {
		t.Errorf("AuthorizeView phase = %q, want %q", got, want)
	}
	if !view.IsHost {
		t.Error("AuthorizeView host IsHost = false, want true")
	}

	// Roster player passes too, and is not flagged as the host.
	playerView, perr := svc.AuthorizeView(t.Context(), "ROOM12", 5)
	if perr != nil {
		t.Errorf("AuthorizeView roster player err = %v, want nil", perr)
	}
	if playerView.IsHost {
		t.Error("AuthorizeView roster player IsHost = true, want false")
	}

	// A stranger is rejected as a non-participant (handler maps to 404).
	if _, serr := svc.AuthorizeView(t.Context(), "ROOM12", 999); !errors.Is(serr, ErrNotParticipant) {
		t.Errorf("AuthorizeView stranger err = %v, want %v", serr, ErrNotParticipant)
	}
}

func TestService_AuthorizeView_UnknownCode(t *testing.T) {
	t.Parallel()

	store := &fakeStore{} // nil session yields ErrSessionNotFound
	svc := NewService(store, &fakeQuiz{}, slog.Default())

	if _, err := svc.AuthorizeView(t.Context(), "NOPE99", 1); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("AuthorizeView unknown code err = %v, want %v", err, ErrSessionNotFound)
	}
}

func TestService_Mutations_TolerateNilPublisher(t *testing.T) {
	t.Parallel()

	// With no publisher wired, the lobby mutations must still succeed - the
	// publish step is best-effort and nil-tolerant.
	store := &fakeStore{session: &Session{ID: "s1", JoinCode: "ROOM12", Phase: PhaseLobby}}
	svc := NewService(store, &fakeQuiz{}, slog.Default())

	if _, err := svc.Join(t.Context(), "ROOM12", 5); err != nil {
		t.Errorf("Join with nil publisher err = %v, want nil", err)
	}
	if err := svc.SetReady(t.Context(), "ROOM12", 5, true); err != nil {
		t.Errorf("SetReady with nil publisher err = %v, want nil", err)
	}
}

// TestService_SubmitAnswer_RespectsWindowBounds pins the answer gate against a
// real DB-backed session driven into the question phase by the runner: a pick
// before StartedAt (during the read beat) is rejected with ErrQuestionNotOpen,
// a pick inside [StartedAt, ExpiresAt] succeeds, and a pick after ExpiresAt is
// rejected. The read beat anchors StartedAt after the question is issued, so a
// client must not be able to pre-submit during the read beat.
func TestService_SubmitAnswer_RespectsWindowBounds(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.June, 5, 12, 0, 0, 0, time.UTC)
	h := newRunnerHarness(t, start, [][]bool{{true}})
	ctx := t.Context()

	if err := h.service.Start(ctx, h.code, 1); err != nil {
		t.Fatalf("Start err = %v, want nil", err)
	}
	h.clock.advance(runnerCfg.RoundIntroBeat)
	h.tick(ctx)
	q := h.reload(t)
	if got, want := q.Phase, PhaseQuestion; got != want {
		t.Fatalf("phase after intro beat = %q, want %q", got, want)
	}
	if q.QuestionStartedAt == nil || q.QuestionExpiresAt == nil {
		t.Fatal("question phase has nil StartedAt/ExpiresAt")
	}
	startedAt, expiresAt := *q.QuestionStartedAt, *q.QuestionExpiresAt
	optRight := correctOptionID(ctx, t, h.service, h.code, h.players[0])
	player := h.players[0]

	// Before StartedAt (still in the read beat): rejected.
	beforeOpen := startedAt.Add(-time.Millisecond)
	if got, want := h.service.SubmitAnswer(
		ctx,
		h.code,
		player,
		optRight,
		beforeOpen,
	), ErrQuestionNotOpen; !errors.Is(
		got,
		want,
	) {
		t.Errorf("SubmitAnswer during read beat err = %v, want %v", got, want)
	}

	// At StartedAt (answers open): accepted.
	if err := h.service.SubmitAnswer(ctx, h.code, player, optRight, startedAt); err != nil {
		t.Errorf("SubmitAnswer at StartedAt err = %v, want nil", err)
	}

	// After ExpiresAt (window closed): rejected.
	afterClose := expiresAt.Add(time.Millisecond)
	if got, want := h.service.SubmitAnswer(
		ctx,
		h.code,
		player,
		optRight,
		afterClose,
	), ErrQuestionNotOpen; !errors.Is(
		got,
		want,
	) {
		t.Errorf("SubmitAnswer after ExpiresAt err = %v, want %v", got, want)
	}
}
