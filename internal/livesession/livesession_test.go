package livesession_test

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strconv"
	"sync"
	"testing"

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
// store cannot force a join-code probe collision on demand, and the
// service's petname-collision retry needs a store that reports
// ErrDisplayNameTaken N times then succeeds. It is not a tautological
// restatement of the store - it injects specific failure sequences the
// real store cannot reproduce deterministically.
type fakeStore struct {
	mu sync.Mutex

	existingCodes map[string]bool
	createErr     error
	createdCodes  []string

	// session returned by GetSessionByJoinCode; nil yields
	// ErrSessionNotFound.
	session *Session

	// addPlayerTakenFor counts how many AddPlayer calls should report
	// ErrDisplayNameTaken before succeeding.
	addPlayerTakenFor int
	addedNames        []string

	setReadyErr error
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

func (f *fakeStore) AddPlayer(_ context.Context, _ string, playerID int64, displayName string) (*Player, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.addPlayerTakenFor > 0 {
		f.addPlayerTakenFor--

		return nil, ErrDisplayNameTaken
	}
	f.addedNames = append(f.addedNames, displayName)

	return &Player{PlayerID: playerID, DisplayName: displayName}, nil
}

func (f *fakeStore) SetReady(context.Context, string, int64, bool) error {
	return f.setReadyErr
}

// fakeQuiz returns the configured quiz or ErrQuizNotFound when nil.
type fakeQuiz struct {
	quiz *quiz.Quiz
}

func (f *fakeQuiz) GetQuiz(_ context.Context, _ int64) (*quiz.Quiz, error) {
	if f.quiz == nil {
		return nil, quiz.ErrQuizNotFound
	}

	return f.quiz, nil
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

func TestService_Join_FallsBackToPetnameOnCollision(t *testing.T) {
	t.Parallel()

	// The requested name collides twice; the service retries with petnames
	// and lands on the first free one.
	store := &fakeStore{
		session:           &Session{ID: "s1", JoinCode: "ROOM12"},
		addPlayerTakenFor: 2,
	}
	svc := NewService(store, &fakeQuiz{}, slog.Default())

	var petCalls int
	petname := func() string {
		petCalls++

		return "Pet-" + strconv.Itoa(petCalls)
	}

	player, err := svc.Join(t.Context(), "ROOM12", 5, "Wanted", petname)
	if err != nil {
		t.Fatalf("Join err = %v, want nil", err)
	}
	// addPlayerTakenFor=2 means: "Wanted" taken, first petname taken,
	// second petname succeeds.
	if got, want := player.DisplayName, "Pet-2"; got != want {
		t.Errorf("Join DisplayName = %q, want %q", got, want)
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

func TestService_Join_PublishesTick(t *testing.T) {
	t.Parallel()

	store := &fakeStore{session: &Session{ID: "s1", JoinCode: "ROOM12", Phase: PhaseLobby}}
	spy := &spyPublisher{}
	svc := NewService(store, &fakeQuiz{}, slog.Default())
	svc.SetPublisher(spy)

	if _, err := svc.Join(t.Context(), "room12", 5, "Wanted", func() string { return "Pet" }); err != nil {
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

	// Host passes and gets the canonical code + phase back.
	code, phase, err := svc.AuthorizeView(t.Context(), "room12", 1)
	if err != nil {
		t.Fatalf("AuthorizeView host err = %v, want nil", err)
	}
	if got, want := code, "ROOM12"; got != want {
		t.Errorf("AuthorizeView code = %q, want %q (canonical)", got, want)
	}
	if got, want := phase, PhaseLobby; got != want {
		t.Errorf("AuthorizeView phase = %q, want %q", got, want)
	}

	// Roster player passes too.
	if _, _, perr := svc.AuthorizeView(t.Context(), "ROOM12", 5); perr != nil {
		t.Errorf("AuthorizeView roster player err = %v, want nil", perr)
	}

	// A stranger is rejected as a non-participant (handler maps to 404).
	if _, _, serr := svc.AuthorizeView(t.Context(), "ROOM12", 999); !errors.Is(serr, ErrNotParticipant) {
		t.Errorf("AuthorizeView stranger err = %v, want %v", serr, ErrNotParticipant)
	}
}

func TestService_AuthorizeView_UnknownCode(t *testing.T) {
	t.Parallel()

	store := &fakeStore{} // nil session yields ErrSessionNotFound
	svc := NewService(store, &fakeQuiz{}, slog.Default())

	if _, _, err := svc.AuthorizeView(t.Context(), "NOPE99", 1); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("AuthorizeView unknown code err = %v, want %v", err, ErrSessionNotFound)
	}
}

func TestService_Mutations_TolerateNilPublisher(t *testing.T) {
	t.Parallel()

	// With no publisher wired, the lobby mutations must still succeed - the
	// publish step is best-effort and nil-tolerant.
	store := &fakeStore{session: &Session{ID: "s1", JoinCode: "ROOM12", Phase: PhaseLobby}}
	svc := NewService(store, &fakeQuiz{}, slog.Default())

	if _, err := svc.Join(t.Context(), "ROOM12", 5, "Wanted", func() string { return "Pet" }); err != nil {
		t.Errorf("Join with nil publisher err = %v, want nil", err)
	}
	if err := svc.SetReady(t.Context(), "ROOM12", 5, true); err != nil {
		t.Errorf("SetReady with nil publisher err = %v, want nil", err)
	}
}
