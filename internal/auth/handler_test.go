package auth_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/mailer"
	"github.com/starquake/topbanana/internal/session"
)

// stubPlayerStore is an in-memory PlayerStore for tests.
type stubPlayerStore struct {
	mu      sync.Mutex
	byID    map[int64]*Player
	byName  map[string]*Player
	nextID  int64
	failGet bool
	// forceAnonCollisions, when > 0, makes the next N CreateAnonymousPlayer
	// calls return ErrUsernameTaken without inserting. Each call decrements
	// the counter, so once it reaches zero the stub returns to its normal
	// insert-or-conflict behaviour. Used by the petname retry-loop tests
	// to drive the middleware down the collision path a deterministic
	// number of times.
	forceAnonCollisions int
	// forceAnonErr, when set, is returned by the NEXT CreateAnonymousPlayer
	// call and then automatically cleared. The single-shot semantics keep
	// each test scenario self-contained: setting the error and then
	// triggering a single request exercises one error branch without
	// leaking the failure into any follow-up call inside the same test.
	// Used to exercise the non-collision error branch of EnsurePlayer.
	forceAnonErr error
}

func newStubPlayerStore() *stubPlayerStore {
	return &stubPlayerStore{
		byID:   map[int64]*Player{},
		byName: map[string]*Player{},
		nextID: 1,
	}
}

func (s *stubPlayerStore) GetPlayerByDisplayName(_ context.Context, username string) (*Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.byName[username]
	if !ok {
		return nil, ErrPlayerNotFound
	}

	return p, nil
}

func (s *stubPlayerStore) GetPlayerByEmail(_ context.Context, email string) (*Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range s.byID {
		if p.Email == email {
			return p, nil
		}
	}

	return nil, ErrPlayerNotFound
}

func (s *stubPlayerStore) GetPlayerByID(_ context.Context, id int64) (*Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failGet {
		return nil, errors.New("boom")
	}
	p, ok := s.byID[id]
	if !ok {
		return nil, ErrPlayerNotFound
	}

	return p, nil
}

// CreatePlayer mirrors the SQL semantics of internal/queries/players.sql:
// honour an explicit "admin" request, otherwise promote the very first
// password-bearing player to admin so the "first registrant becomes admin"
// rule is observed atomically.
func (s *stubPlayerStore) CreatePlayer(
	_ context.Context,
	username, email, passwordHash, requestedRole string,
) (*Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byName[username]; exists {
		return nil, ErrUsernameTaken
	}
	if email != "" {
		for _, p := range s.byID {
			if p.Email == email {
				return nil, ErrEmailTaken
			}
		}
	}

	role := s.resolveRoleLocked(requestedRole)

	p := &Player{
		ID:           s.nextID,
		DisplayName:  username,
		Email:        email,
		PasswordHash: passwordHash,
		Role:         role,
	}
	s.nextID++
	s.byID[p.ID] = p
	s.byName[username] = p

	return p, nil
}

// CreateAnonymousPlayer mirrors store.CreateAnonymousPlayer: insert a row
// with the given username, no password_hash, role = "player".
func (s *stubPlayerStore) CreateAnonymousPlayer(_ context.Context, username string) (*Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.forceAnonErr != nil {
		err := s.forceAnonErr
		s.forceAnonErr = nil

		return nil, err
	}
	if s.forceAnonCollisions > 0 {
		s.forceAnonCollisions--

		return nil, ErrUsernameTaken
	}

	if _, exists := s.byName[username]; exists {
		return nil, ErrUsernameTaken
	}

	p := &Player{
		ID:          s.nextID,
		DisplayName: username,
		Role:        RolePlayer,
	}
	s.nextID++
	s.byID[p.ID] = p
	s.byName[username] = p

	return p, nil
}

// ClaimPlayer mirrors store.ClaimPlayer: upgrades an anonymous row in place
// or fails with ErrPlayerAlreadyClaimed / ErrPlayerNotFound /
// ErrUsernameTaken.
func (s *stubPlayerStore) ClaimPlayer(
	_ context.Context,
	playerID int64,
	username, email, passwordHash, requestedRole string,
) (*Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.byID[playerID]
	if !ok {
		return nil, ErrPlayerNotFound
	}
	if existing.PasswordHash != "" {
		return nil, ErrPlayerAlreadyClaimed
	}
	if other, exists := s.byName[username]; exists && other.ID != playerID {
		return nil, ErrUsernameTaken
	}
	if email != "" {
		for _, p := range s.byID {
			if p.ID != playerID && p.Email == email {
				return nil, ErrEmailTaken
			}
		}
	}

	delete(s.byName, existing.DisplayName)
	existing.Email = email
	existing.DisplayName = username
	existing.PasswordHash = passwordHash
	existing.Role = s.resolveRoleLocked(requestedRole)
	s.byName[username] = existing

	return existing, nil
}

func (s *stubPlayerStore) UpdatePlayerDisplayName(
	_ context.Context, playerID int64, username string,
) (*Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.byID[playerID]
	if !ok {
		return nil, ErrPlayerNotFound
	}
	if existing.PasswordHash != "" {
		return nil, ErrPlayerNotAnonymous
	}
	if other, exists := s.byName[username]; exists && other.ID != playerID {
		return nil, ErrUsernameTaken
	}

	delete(s.byName, existing.DisplayName)
	existing.DisplayName = username
	s.byName[username] = existing

	return existing, nil
}

func (s *stubPlayerStore) RenamePlayer(
	_ context.Context, playerID int64, username string,
) (*Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(username) == "" {
		return nil, ErrUsernameEmpty
	}
	existing, ok := s.byID[playerID]
	if !ok {
		return nil, ErrPlayerNotFound
	}
	if other, exists := s.byName[username]; exists && other.ID != playerID {
		return nil, ErrUsernameTaken
	}

	delete(s.byName, existing.DisplayName)
	existing.DisplayName = username
	s.byName[username] = existing

	return existing, nil
}

func (s *stubPlayerStore) SetPlayerPasswordHash(_ context.Context, username, passwordHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.byName[username]
	if !ok {
		return ErrPlayerNotFound
	}
	p.PasswordHash = passwordHash

	return nil
}

func (s *stubPlayerStore) ChangePlayerPassword(_ context.Context, playerID int64, passwordHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.byID[playerID]
	if !ok {
		return ErrPlayerNotFound
	}
	p.PasswordHash = passwordHash
	p.SessionVersion++

	return nil
}

// resolveRoleLocked applies the same "first password-bearing registrant
// becomes admin" rule as the production SQL. Caller must hold s.mu.
func (s *stubPlayerStore) resolveRoleLocked(requestedRole string) string {
	if requestedRole == RoleAdmin {
		return RoleAdmin
	}
	for _, existing := range s.byID {
		if existing.PasswordHash != "" {
			return RolePlayer
		}
	}

	return RoleAdmin
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func postForm(t *testing.T, handler http.Handler, path string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func TestHandleRegisterForm_GET_RendersForm(t *testing.T) {
	t.Parallel()

	handler := HandleRegisterForm(
		discardLogger(),
		nil,
		newStubPlayerStore(),
		session.New([]byte("k"), true),
		false,
	)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/register", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	body := rec.Body.String()
	if want := `name="display_name"`; !strings.Contains(body, want) {
		t.Errorf("body = %q, want substring %q", body, want)
	}
	// passwordHelp template func renders the form's help text from the
	// MinPasswordLength/MaxPasswordLength constants. Asserting the
	// rendered output keeps the func bound to the constants and the
	// validator-side error messages.
	helpWant := fmt.Sprintf("Must be %d-%d characters.", MinPasswordLength, MaxPasswordLength)
	if !strings.Contains(body, helpWant) {
		t.Errorf("body = %q, want password-help substring %q", body, helpWant)
	}
}

func TestHandleRegisterSubmit_FirstUser_BecomesAdmin(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	handler := HandleRegisterSubmit(discardLogger(), nil, store, session.New([]byte("k"), true), RegisterDeps{})

	rec := postForm(t, handler, "/register", url.Values{
		"display_name":     {"alice"},
		"email":            {"alice@example.test"},
		"password":         {"correctbattery"},
		"password_confirm": {"correctbattery"},
	})

	assertRegisterPending(t, rec, "alice@example.test")

	p, err := store.GetPlayerByDisplayName(t.Context(), "alice")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
	}
	if got, want := p.Role, RoleAdmin; got != want {
		t.Errorf("Role = %q, want %q", got, want)
	}
}

// assertRegisterPending pins the post-#574 hard-gate contract: a
// successful registration renders the confirmation page with 200,
// names the recipient address in the body, and does NOT leave a live
// session cookie. sessions.Clear emits a deletion cookie (empty value,
// negative MaxAge), which is expected; a non-empty session value is the
// failure we guard against.
func assertRegisterPending(t *testing.T, rec *httptest.ResponseRecorder, email string) {
	t.Helper()
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d (body=%q)", got, want, rec.Body.String())
	}
	body := rec.Body.String()
	if got, want := body, "Verify your email"; !strings.Contains(got, want) {
		t.Errorf("body missing confirmation headline %q; body=%.300q", want, got)
	}
	if got, want := body, email; !strings.Contains(got, want) {
		t.Errorf("body missing recipient address %q; body=%.300q", want, got)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.CookieName && c.Value != "" {
			t.Errorf("live session cookie set on register: %+v", c)
		}
	}
}

// assertSessionCleared pins the anonymous-upgrade arm of the hard gate
// (#574): registering while holding an anonymous session cookie must
// emit a deletion cookie (empty value, negative MaxAge) so the
// pre-existing anonymous session is signed out rather than left live.
func assertSessionCleared(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.CookieName && c.Value == "" && c.MaxAge < 0 {
			cleared = true

			break
		}
	}
	if !cleared {
		t.Errorf("expected session cookie to be cleared; cookies = %v", rec.Result().Cookies())
	}
}

func TestHandleRegisterSubmit_SecondUser_DefaultsToPlayer(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	// Pre-seed first admin.
	if _, err := store.CreatePlayer(t.Context(), "admin", "admin@example.test", "hash", RoleAdmin); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	handler := HandleRegisterSubmit(discardLogger(), nil, store, session.New([]byte("k"), true), RegisterDeps{})
	rec := postForm(t, handler, "/register", url.Values{
		"display_name":     {"bob"},
		"email":            {"bob@example.test"},
		"password":         {"correctbattery"},
		"password_confirm": {"correctbattery"},
	})

	assertRegisterPending(t, rec, "bob@example.test")

	p, err := store.GetPlayerByDisplayName(t.Context(), "bob")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
	}
	if got, want := p.Role, RolePlayer; got != want {
		t.Errorf("Role = %q, want %q", got, want)
	}
}

func TestHandleRegisterSubmit_AdminEmailsEnv_PromotesToAdmin(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	// Pre-seed first user so the count > 0 and the env var path is exercised.
	if _, err := store.CreatePlayer(t.Context(), "first", "first@example.test", "h", RolePlayer); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	handler := HandleRegisterSubmit(
		discardLogger(),
		nil,
		store,
		session.New([]byte("k"), true),
		RegisterDeps{AdminEmails: []string{"alice@example.test", "carol@example.test"}},
	)
	rec := postForm(t, handler, "/register", url.Values{
		"display_name":     {"carol"},
		"email":            {"carol@example.test"},
		"password":         {"correctbattery"},
		"password_confirm": {"correctbattery"},
	})

	assertRegisterPending(t, rec, "carol@example.test")

	p, err := store.GetPlayerByDisplayName(t.Context(), "carol")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
	}
	if got, want := p.Role, RoleAdmin; got != want {
		t.Errorf("Role = %q, want %q", got, want)
	}
}

func TestHandleRegisterSubmit_PasswordTooShort(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	handler := HandleRegisterSubmit(discardLogger(), nil, store, session.New([]byte("k"), true), RegisterDeps{})

	rec := postForm(t, handler, "/register", url.Values{
		"display_name":     {"alice"},
		"email":            {"alice@example.test"},
		"password":         {"short"},
		"password_confirm": {"short"},
	})

	if got, want := rec.Code, http.StatusBadRequest; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	want := fmt.Sprintf("at least %d characters", MinPasswordLength)
	if got := rec.Body.String(); !strings.Contains(got, want) {
		t.Errorf("body = %q, want substring %q", got, want)
	}
	if _, err := store.GetPlayerByDisplayName(t.Context(), "alice"); !errors.Is(err, ErrPlayerNotFound) {
		t.Errorf("player should not have been created, GetPlayerByDisplayName err = %v", err)
	}
}

// TestHandleRegisterSubmit_PasswordTooLong covers the bcrypt 72-byte cap on
// the public registration form. Without the upfront check, bcrypt would
// surface a wrapped 500; the friendly form error keeps the failure mode
// consistent with the too-short case.
func TestHandleRegisterSubmit_PasswordTooLong(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	handler := HandleRegisterSubmit(discardLogger(), nil, store, session.New([]byte("k"), true), RegisterDeps{})

	longPassword := strings.Repeat("a", MaxPasswordLength+1)
	rec := postForm(t, handler, "/register", url.Values{
		"display_name":     {"alice"},
		"email":            {"alice@example.test"},
		"password":         {longPassword},
		"password_confirm": {longPassword},
	})

	if got, want := rec.Code, http.StatusBadRequest; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	want := fmt.Sprintf("at most %d characters", MaxPasswordLength)
	if got := rec.Body.String(); !strings.Contains(got, want) {
		t.Errorf("body = %q, want substring %q", got, want)
	}
	if _, err := store.GetPlayerByDisplayName(t.Context(), "alice"); !errors.Is(err, ErrPlayerNotFound) {
		t.Errorf("player should not have been created, GetPlayerByDisplayName err = %v", err)
	}
}

func TestHandleRegisterSubmit_DuplicateUsername(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	if _, err := store.CreatePlayer(t.Context(), "alice", "alice@example.test", "h", RolePlayer); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	handler := HandleRegisterSubmit(discardLogger(), nil, store, session.New([]byte("k"), true), RegisterDeps{})
	rec := postForm(t, handler, "/register", url.Values{
		"display_name":     {"alice"},
		"email":            {"alice@example.test"},
		"password":         {"correctbattery"},
		"password_confirm": {"correctbattery"},
	})

	if got, want := rec.Code, http.StatusConflict; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "already taken"; !strings.Contains(got, want) {
		t.Errorf("body did not mention duplicate, got %q", got)
	}
}

// TestHandleRegisterSubmit_DuplicateEmail pins the account-enumeration
// opacity contract (#573): registering with an already-registered email
// must return the same pending page (200, same body, no live session) as
// a fresh signup, and dispatch an out-of-band notice to the real owner
// rather than surface a distinct "already registered" response.
func TestHandleRegisterSubmit_DuplicateEmail(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	if _, err := store.CreatePlayer(t.Context(), "alice", "shared@example.test", "h", RolePlayer); err != nil {
		t.Fatalf("seed CreatePlayer err = %v, want nil", err)
	}

	sender := &recordingSender{}
	handler := HandleRegisterSubmit(
		discardLogger(), nil, store, session.New([]byte("k"), true),
		RegisterDeps{Mailer: sender},
	)
	rec := postForm(t, handler, "/register", url.Values{
		"display_name":     {"bob"},
		"email":            {"shared@example.test"},
		"password":         {"correctbattery"},
		"password_confirm": {"correctbattery"},
	})

	assertRegisterPending(t, rec, "shared@example.test")
	if got := rec.Body.String(); strings.Contains(got, "already registered") {
		t.Errorf("body leaked account existence; body=%.300q", got)
	}

	// The owner-notification send fires on a detached goroutine; give it
	// up to a second to land. The mailer is the in-process recordingSender
	// so the wait is just goroutine scheduling, not network.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(sender.Sent()) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	sent := sender.Sent()
	if got, want := len(sent), 1; got != want {
		t.Fatalf("sender.Sent() len = %d, want %d", got, want)
	}
	if got, want := sent[0].Kind, mailer.KindRegisterExisting; got != want {
		t.Errorf("sent[0].Kind = %q, want %q", got, want)
	}
	if got, want := sent[0].To, "shared@example.test"; got != want {
		t.Errorf("sent[0].To = %q, want %q", got, want)
	}
}

func TestHandleRegisterSubmit_RejectsInvalidEmail(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	handler := HandleRegisterSubmit(discardLogger(), nil, store, session.New([]byte("k"), true), RegisterDeps{})
	rec := postForm(t, handler, "/register", url.Values{
		"display_name":     {"alice"},
		"email":            {"not-an-email"},
		"password":         {"correctbattery"},
		"password_confirm": {"correctbattery"},
	})

	if got, want := rec.Code, http.StatusBadRequest; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "Enter a valid email address"; !strings.Contains(got, want) {
		t.Errorf("body did not surface the email validator banner, got %q", got)
	}
}

func TestHandleRegisterSubmit_MatchingPasswords_CreatesPlayer(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	handler := HandleRegisterSubmit(discardLogger(), nil, store, session.New([]byte("k"), true), RegisterDeps{})

	rec := postForm(t, handler, "/register", url.Values{
		"display_name":     {"alice"},
		"email":            {"alice@example.test"},
		"password":         {"correctbattery"},
		"password_confirm": {"correctbattery"},
	})

	assertRegisterPending(t, rec, "alice@example.test")
	if _, err := store.GetPlayerByDisplayName(t.Context(), "alice"); err != nil {
		t.Errorf("player should have been created, err = %v", err)
	}
}

func TestHandleRegisterSubmit_MismatchedPasswords_Rejects(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	handler := HandleRegisterSubmit(discardLogger(), nil, store, session.New([]byte("k"), true), RegisterDeps{})

	password := "correctbattery"
	rec := postForm(t, handler, "/register", url.Values{
		"display_name":     {"alice"},
		"email":            {"alice@example.test"},
		"password":         {password},
		"password_confirm": {"correctbatterydifferent"},
	})

	if got, want := rec.Code, http.StatusBadRequest; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	body := rec.Body.String()
	if got, want := body, "Passwords do not match."; !strings.Contains(got, want) {
		t.Errorf("body did not surface mismatch banner, got %q", got)
	}
	if got, want := body, `value="alice"`; !strings.Contains(got, want) {
		t.Errorf("body = %q, should contain %q", got, want)
	}
	if got, want := body, `value="alice@example.test"`; !strings.Contains(got, want) {
		t.Errorf("body = %q, should contain %q", got, want)
	}
	// Security: the failed-validation re-render must not echo either
	// password back into the response so a shoulder-surfer or cached
	// page can't recover the typed value.
	if strings.Contains(body, password) {
		t.Errorf("body must not leak the submitted password, got %q", body)
	}
	if strings.Contains(body, "correctbatterydifferent") {
		t.Errorf("body must not leak the submitted password_confirm, got %q", body)
	}
	if _, err := store.GetPlayerByDisplayName(t.Context(), "alice"); !errors.Is(err, ErrPlayerNotFound) {
		t.Errorf("player should not have been created, err = %v", err)
	}
}

func TestHandleRegisterSubmit_LowercasesAndTrimsEmail(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	handler := HandleRegisterSubmit(discardLogger(), nil, store, session.New([]byte("k"), true), RegisterDeps{})
	rec := postForm(t, handler, "/register", url.Values{
		"display_name":     {"alice"},
		"email":            {"  ALICE@Example.Test "},
		"password":         {"correctbattery"},
		"password_confirm": {"correctbattery"},
	})

	assertRegisterPending(t, rec, "alice@example.test")
	p, err := store.GetPlayerByDisplayName(t.Context(), "alice")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
	}
	if got, want := p.Email, "alice@example.test"; got != want {
		t.Errorf("stored email = %q, want %q", got, want)
	}
}

// TestHandleRegisterSubmit_ClaimsAnonymousSession verifies that registering
// while already holding an anonymous session upgrades the existing row in
// place rather than inserting a new one. This is the score-claiming flow:
// the visitor's player_id stays stable so any games they played before
// signing up still belong to them.
func TestHandleRegisterSubmit_ClaimsAnonymousSession(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	anon, err := store.CreateAnonymousPlayer(t.Context(), "anon-existing")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	sessions := session.New([]byte("k"), true)
	handler := HandleRegisterSubmit(discardLogger(), nil, store, sessions, RegisterDeps{})

	// Build a request that already carries the anonymous session cookie.
	rec := httptest.NewRecorder()
	sessions.Set(rec, anon.ID, 0)
	cookie := rec.Result().Cookies()[0]

	form := url.Values{
		"display_name":     {"alice"},
		"email":            {"alice@example.test"},
		"password":         {"correctbattery"},
		"password_confirm": {"correctbattery"},
	}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/register", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assertRegisterPending(t, rec, "alice@example.test")
	assertSessionCleared(t, rec)

	// The same row was upgraded - no new row should appear, and the
	// anonymous username should be gone.
	upgraded, err := store.GetPlayerByDisplayName(t.Context(), "alice")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
	}
	if got, want := upgraded.ID, anon.ID; got != want {
		t.Errorf("upgraded.ID = %d, want %d (player ID should be preserved)", got, want)
	}
	if upgraded.PasswordHash == "" {
		t.Error("upgraded.PasswordHash is empty, want bcrypt hash from claim")
	}
	if _, err := store.GetPlayerByDisplayName(t.Context(), "anon-existing"); !errors.Is(err, ErrPlayerNotFound) {
		t.Errorf("anonymous row still resolvable by old username; err = %v, want ErrPlayerNotFound", err)
	}
}

// TestHandleRegisterSubmit_ClaimWithTakenUsername returns 409 and leaves
// the anonymous row untouched so the visitor can retry with a different
// username. First-sign-in-wins semantics: the row that already owns the
// requested username keeps it.
func TestHandleRegisterSubmit_ClaimWithTakenUsername(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	if _, err := store.CreatePlayer(
		t.Context(),
		"alice",
		"alice@example.test",
		"previousHash",
		RolePlayer,
	); err != nil {
		t.Fatalf("seed CreatePlayer err = %v, want nil", err)
	}
	anon, err := store.CreateAnonymousPlayer(t.Context(), "anon-claimer")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	sessions := session.New([]byte("k"), true)
	handler := HandleRegisterSubmit(discardLogger(), nil, store, sessions, RegisterDeps{})

	rec := httptest.NewRecorder()
	sessions.Set(rec, anon.ID, 0)
	cookie := rec.Result().Cookies()[0]

	form := url.Values{
		"display_name":     {"alice"}, // already taken by the seeded credentialled row
		"email":            {"new-alice@example.test"},
		"password":         {"correctbattery"},
		"password_confirm": {"correctbattery"},
	}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/register", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusConflict; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	// Anonymous row was not mutated.
	stillAnon, err := store.GetPlayerByID(t.Context(), anon.ID)
	if err != nil {
		t.Fatalf("GetPlayerByID err = %v, want nil", err)
	}
	if got, want := stillAnon.DisplayName, "anon-claimer"; got != want {
		t.Errorf("anonymous row Username = %q, want %q (should be unchanged)", got, want)
	}
	if !stillAnon.IsAnonymous() {
		t.Error("anonymous row IsAnonymous() = false, want true (should still be unclaimed)")
	}
}

// TestHandleRegisterSubmit_ClaimAlreadyClaimed_FallsBackToCreate covers the
// concurrent-registration race: by the time the handler reaches ClaimPlayer
// the anonymous row has already been claimed (e.g. another tab raced ahead).
// The handler should fall through to CreatePlayer so the registration still
// succeeds, just with a fresh row.
func TestHandleRegisterSubmit_ClaimAlreadyClaimed_FallsBackToCreate(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	anon, err := store.CreateAnonymousPlayer(t.Context(), "anon-racer")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	// Simulate another tab claiming the row first.
	if _, claimErr := store.ClaimPlayer(
		t.Context(),
		anon.ID,
		"winner",
		"winner@example.test",
		"winnerHash",
		RolePlayer,
	); claimErr != nil {
		t.Fatalf("ClaimPlayer err = %v, want nil", claimErr)
	}

	sessions := session.New([]byte("k"), true)
	handler := HandleRegisterSubmit(discardLogger(), nil, store, sessions, RegisterDeps{})

	rec := httptest.NewRecorder()
	sessions.Set(rec, anon.ID, 0) // cookie still points at the now-claimed row
	cookie := rec.Result().Cookies()[0]

	form := url.Values{
		"display_name":     {"latecomer"},
		"email":            {"latecomer@example.test"},
		"password":         {"correctbattery"},
		"password_confirm": {"correctbattery"},
	}
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/register", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assertRegisterPending(t, rec, "latecomer@example.test")
	assertSessionCleared(t, rec)
	// A fresh row was created instead of clobbering the already-claimed one.
	latecomer, err := store.GetPlayerByDisplayName(t.Context(), "latecomer")
	if err != nil {
		t.Fatalf("GetPlayerByDisplayName err = %v, want nil", err)
	}
	if got, dontWant := latecomer.ID, anon.ID; got == dontWant {
		t.Errorf("latecomer reused the racer's anonymous ID %d, want a fresh row", got)
	}
}

func TestHandleLoginForm_GET_RendersForm(t *testing.T) {
	t.Parallel()

	handler := HandleLoginForm(
		discardLogger(),
		nil,
		newStubPlayerStore(),
		session.New([]byte("k"), true),
		false,
		false,
	)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "Log in"; !strings.Contains(got, want) {
		t.Errorf("body did not contain %q", want)
	}
}

func TestHandleLoginForm_RegistrationDisabled_HidesRegisterLink(t *testing.T) {
	t.Parallel()

	handler := HandleLoginForm(
		discardLogger(),
		nil,
		newStubPlayerStore(),
		session.New([]byte("k"), true),
		false,
		false,
	)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got := rec.Body.String(); strings.Contains(got, "/register") {
		t.Errorf("body should not contain %q when registration is disabled, got %q", "/register", got)
	}
}

func TestHandleLoginForm_RegistrationEnabled_ShowsRegisterLink(t *testing.T) {
	t.Parallel()

	handler := HandleLoginForm(
		discardLogger(),
		nil,
		newStubPlayerStore(),
		session.New([]byte("k"), true),
		true,
		false,
	)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), `href="/register"`; !strings.Contains(got, want) {
		t.Errorf("body should contain %q when registration is enabled, got %q", want, got)
	}
}

// TestHandleLoginForm_AlreadySignedIn_RedirectsToLanding pins the "skip
// the form if the visitor is already authenticated" rule. Without it,
// a returning player who clicked Log in by reflex would see the form
// again and could accidentally rotate their session.
func TestHandleLoginForm_AlreadySignedIn_RedirectsToLanding(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	hash, err := HashPassword("correctbattery")
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}
	// Seed an admin first so the stub's "first password-bearing
	// registrant becomes admin" rule (mirroring the production SQL)
	// promotes that row, not carol. Carol then stays as a plain
	// player and the redirect lands on / instead of /admin/quizzes.
	if _, seedErr := store.CreatePlayer(
		t.Context(),
		"first-admin",
		"first-admin@example.test",
		hash,
		RoleAdmin,
	); seedErr != nil {
		t.Fatalf("seed admin err = %v, want nil", seedErr)
	}
	player, err := store.CreatePlayer(t.Context(), "carol", "carol@example.test", hash, RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	sessions := session.New([]byte("k"), true)
	handler := HandleLoginForm(discardLogger(), nil, store, sessions, false, false)

	rec := httptest.NewRecorder()
	sessions.Set(rec, player.ID, 0)
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d (body=%q)", got, want, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), "/"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// TestHandleLoginForm_AnonymousSession_RendersForm pins the partner to
// the redirect rule: a visitor with only an anonymous (EnsurePlayer-
// stub) session is NOT authenticated and must see the login form.
func TestHandleLoginForm_AnonymousSession_RendersForm(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	anon, err := store.CreateAnonymousPlayer(t.Context(), "anon-fancy")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	sessions := session.New([]byte("k"), true)
	handler := HandleLoginForm(discardLogger(), nil, store, sessions, false, false)

	rec := httptest.NewRecorder()
	sessions.Set(rec, anon.ID, 0)
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "Log in"; !strings.Contains(got, want) {
		t.Errorf("body should render login form, got %q", got)
	}
}

// loginDeps returns a LoginDeps with the supplied store + session
// manager + limiter and the rest of the fields zero-valued. Mailer /
// Tokens / ResendLimiter staying nil keeps the verify-resend branch
// dormant for tests that don't exercise it; the verified-email
// helper [markVerified] is what flips the gate off for happy-path
// tests.
func loginDeps(
	store PlayerStore, sessions *session.Manager, limiter *LoginRateLimiter,
) LoginDeps {
	return LoginDeps{
		Players:  store,
		Sessions: sessions,
		Limiter:  limiter,
	}
}

// markVerified stamps email_verified_at on the named stub player so
// the post-#492 login gate lets them through. Production rows get
// stamped via SendVerifyEmail's ConsumeVerifyToken path; in tests we
// just toggle the column directly because the verify dance is not
// what these cases pin.
func markVerified(t *testing.T, store *stubPlayerStore, username string) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	p, ok := store.byName[username]
	if !ok {
		t.Fatalf("markVerified: no stub player named %q", username)
	}
	now := time.Now().UTC()
	p.EmailVerifiedAt = &now
}

func TestHandleLoginSubmit_Success(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	hash, err := HashPassword("correctbattery")
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}
	if _, err := store.CreatePlayer(t.Context(), "alice", "alice@example.test", hash, RoleAdmin); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	markVerified(t, store, "alice")

	handler := HandleLoginSubmit(discardLogger(), nil,
		loginDeps(store, session.New([]byte("k"), true), NewLoginRateLimiter(time.Minute, nil)))
	rec := postForm(t, handler, "/login", url.Values{
		"email":    {"alice@example.test"},
		"password": {"correctbattery"},
	})

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d (body=%q)", got, want, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), "/admin/quizzes"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// TestHandleLoginSubmit_HonoursNext pins #449: a posted `next` that
// passes SafeNextPath becomes the success-redirect target instead of
// the role landing.
func TestHandleLoginSubmit_HonoursNext(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	hash, err := HashPassword("correctbattery")
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}
	if _, err := store.CreatePlayer(t.Context(), "alice", "alice@example.test", hash, RoleAdmin); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	markVerified(t, store, "alice")

	handler := HandleLoginSubmit(discardLogger(), nil,
		loginDeps(store, session.New([]byte("k"), true), NewLoginRateLimiter(time.Minute, nil)))
	rec := postForm(t, handler, "/login", url.Values{
		"email":    {"alice@example.test"},
		"password": {"correctbattery"},
		"next":     {"/admin/email"},
	})

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d (body=%q)", got, want, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), "/admin/email"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// TestHandleLoginSubmit_RejectsUnsafeNext pins the open-redirect
// defence: a `next` SafeNextPath rejects falls back to the role
// landing instead of forwarding the attacker-controlled value.
func TestHandleLoginSubmit_RejectsUnsafeNext(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	hash, err := HashPassword("correctbattery")
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}
	if _, err := store.CreatePlayer(t.Context(), "alice", "alice@example.test", hash, RoleAdmin); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	markVerified(t, store, "alice")

	handler := HandleLoginSubmit(discardLogger(), nil,
		loginDeps(store, session.New([]byte("k"), true), NewLoginRateLimiter(time.Minute, nil)))
	rec := postForm(t, handler, "/login", url.Values{
		"email":    {"alice@example.test"},
		"password": {"correctbattery"},
		"next":     {"//evil.com/"},
	})

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d (body=%q)", got, want, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), "/admin/quizzes"; got != want {
		t.Errorf("Location = %q, want %q (must fall back to role landing)", got, want)
	}
}

// TestHandleLoginSubmit_Success_Player pins the #288 fix: a non-admin
// must land on the public home page, not the admin dashboard (which
// would bounce them straight through RequireAdmin to "Access denied").
func TestHandleLoginSubmit_Success_Player(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	// Pre-seed an admin so the "first password-bearing registrant
	// becomes admin" rule doesn't accidentally promote bob.
	if _, err := store.CreatePlayer(
		t.Context(),
		"first-admin",
		"first-admin@example.test",
		"h",
		RoleAdmin,
	); err != nil {
		t.Fatalf("seed CreatePlayer err = %v, want nil", err)
	}
	hash, err := HashPassword("correctbattery")
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}
	if _, err := store.CreatePlayer(t.Context(), "bob", "bob@example.test", hash, RolePlayer); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	markVerified(t, store, "bob")

	handler := HandleLoginSubmit(discardLogger(), nil,
		loginDeps(store, session.New([]byte("k"), true), NewLoginRateLimiter(time.Minute, nil)))
	rec := postForm(t, handler, "/login", url.Values{
		"email":    {"bob@example.test"},
		"password": {"correctbattery"},
	})

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d (body=%q)", got, want, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), "/"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestHandleLoginSubmit_BadCredentials_UnknownUser(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	handler := HandleLoginSubmit(discardLogger(), nil,
		loginDeps(store, session.New([]byte("k"), true), NewLoginRateLimiter(time.Minute, nil)))

	rec := postForm(t, handler, "/login", url.Values{
		"email":    {"ghost@example.test"},
		"password": {"correctbattery"},
	})

	if got, want := rec.Code, http.StatusUnauthorized; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "Invalid email or password"; !strings.Contains(got, want) {
		t.Errorf("body should mention invalid credentials, got %q", got)
	}
}

func TestHandleLoginSubmit_BadCredentials_WrongPassword(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	hash, err := HashPassword("right-password-yes")
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}
	if _, err := store.CreatePlayer(t.Context(), "alice", "alice@example.test", hash, RolePlayer); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	markVerified(t, store, "alice")

	handler := HandleLoginSubmit(discardLogger(), nil,
		loginDeps(store, session.New([]byte("k"), true), NewLoginRateLimiter(time.Minute, nil)))
	rec := postForm(t, handler, "/login", url.Values{
		"email":    {"alice@example.test"},
		"password": {"wrong-password-no"},
	})

	if got, want := rec.Code, http.StatusUnauthorized; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "Invalid email or password"; !strings.Contains(got, want) {
		t.Errorf("body should mention invalid credentials, got %q", got)
	}
}

func TestHandleLoginSubmit_RejectsEmptyHash(t *testing.T) {
	t.Parallel()

	// Seed a player with no password hash (legacy admin from migration).
	store := newStubPlayerStore()
	if _, err := store.CreatePlayer(t.Context(), "legacy", "legacy@example.test", "", RoleAdmin); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	handler := HandleLoginSubmit(discardLogger(), nil,
		loginDeps(store, session.New([]byte("k"), true), NewLoginRateLimiter(time.Minute, nil)))
	rec := postForm(t, handler, "/login", url.Values{
		"email":    {"legacy@example.test"},
		"password": {"anything-goes-here-13"},
	})

	if got, want := rec.Code, http.StatusUnauthorized; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

// TestHandleLoginSubmit_EmailMixedCaseAndWhitespace pins the trim +
// ToLower normalisation in HandleLoginSubmit so a registrant who
// signed up with "alice@example.test" can still log in by typing
// " ALICE@Example.Test " (#446).
func TestHandleLoginSubmit_EmailMixedCaseAndWhitespace(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	hash, err := HashPassword("correctbattery")
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}
	if _, err := store.CreatePlayer(t.Context(), "alice", "alice@example.test", hash, RoleAdmin); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	markVerified(t, store, "alice")

	handler := HandleLoginSubmit(discardLogger(), nil,
		loginDeps(store, session.New([]byte("k"), true), NewLoginRateLimiter(time.Minute, nil)))
	rec := postForm(t, handler, "/login", url.Values{
		"email":    {"  ALICE@Example.Test "},
		"password": {"correctbattery"},
	})

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d (body=%q)", got, want, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), "/admin/quizzes"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// TestHandleRegisterSubmit_BlankUsername_GeneratesPetname pins the post-
// #446 contract: a blank display name on the register form falls back to
// GeneratePetname so register-with-just-email is a valid signup. Pre-446
// this branch was a 400 "Username is required.".
func TestHandleRegisterSubmit_BlankUsername_GeneratesPetname(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	handler := HandleRegisterSubmit(discardLogger(), nil, store, session.New([]byte("k"), true), RegisterDeps{})

	rec := postForm(t, handler, "/register", url.Values{
		"display_name":     {"   "},
		"email":            {"petname@example.test"},
		"password":         {"correctbattery"},
		"password_confirm": {"correctbattery"},
	})

	assertRegisterPending(t, rec, "petname@example.test")
	// The whitespace-only username path falls into GeneratePetname, so
	// no row should be created under the empty key. The row exists under
	// the petname; we look it up by email instead.
	p, err := store.GetPlayerByEmail(t.Context(), "petname@example.test")
	if err != nil {
		t.Fatalf("GetPlayerByEmail err = %v, want nil", err)
	}
	if p.DisplayName == "" {
		t.Error("created Username is empty, want a non-empty petname")
	}
}

func TestHandleRegisterSubmit_PasswordExactlyMinLength(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	handler := HandleRegisterSubmit(discardLogger(), nil, store, session.New([]byte("k"), true), RegisterDeps{})

	password := strings.Repeat("a", MinPasswordLength) // exactly 13 characters
	rec := postForm(t, handler, "/register", url.Values{
		"display_name":     {"alice"},
		"email":            {"alice@example.test"},
		"password":         {password},
		"password_confirm": {password},
	})

	assertRegisterPending(t, rec, "alice@example.test")
	if _, err := store.GetPlayerByDisplayName(t.Context(), "alice"); err != nil {
		t.Errorf("player should have been created, err = %v", err)
	}
}

func TestHandleLogout_NoCookie(t *testing.T) {
	t.Parallel()

	handler := HandleLogout(session.New([]byte("k"), true))

	// No session cookie attached to the request.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/logout", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Location"), "/login"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestHandleLogout_ClearsCookieAndRedirects(t *testing.T) {
	t.Parallel()

	handler := HandleLogout(session.New([]byte("k"), true))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/logout", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Location"), "/login"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}

	cookies := rec.Result().Cookies()
	if got, want := len(cookies), 1; got != want {
		t.Fatalf("got %d cookies, want %d", got, want)
	}
	c := cookies[0]
	if got, want := c.Name, session.CookieName; got != want {
		t.Errorf("cookie name = %q, want %q", got, want)
	}
	if got, want := c.MaxAge, -1; got != want {
		t.Errorf("cookie MaxAge = %d, want %d", got, want)
	}
}

// TestHandleLoginSubmit_UnverifiedEmail_RefusesAndResends pins #492:
// valid credentials against a row whose email_verified_at is NULL must
// re-render the login form (no 303 to landing, no session cookie set)
// and trigger a fresh verify-email send through the wired-in mailer.
func TestHandleLoginSubmit_UnverifiedEmail_RefusesAndResends(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	hash, err := HashPassword("correctbattery")
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}
	if _, err := store.CreatePlayer(t.Context(), "unv", "unv@example.test", hash, RolePlayer); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	tokens := &recordingVerifyTokenStore{}
	sender := &recordingSender{}
	sessions := session.New([]byte("k"), true)
	handler := HandleLoginSubmit(discardLogger(), nil, LoginDeps{
		Players:       store,
		Sessions:      sessions,
		Limiter:       NewLoginRateLimiter(time.Minute, nil),
		Mailer:        sender,
		Tokens:        tokens,
		ResendLimiter: NewVerifyResendLimiter(time.Minute, nil),
		BaseURL:       "https://topbanana.example",
	})

	rec := postForm(t, handler, "/login", url.Values{
		"email":    {"unv@example.test"},
		"password": {"correctbattery"},
	})

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d (body=%q)", got, want, rec.Body.String())
	}
	body := rec.Body.String()
	if want := "verify your email"; !strings.Contains(body, want) {
		t.Errorf("body missing verify-email banner; body=%.300q", body)
	}
	if want := "unv@example.test"; !strings.Contains(body, want) {
		t.Errorf("body missing recipient address; body=%.300q", body)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.CookieName && c.Value != "" {
			t.Errorf("session cookie set on unverified login: %+v", c)
		}
	}

	// The resend goroutine fires off the request goroutine; give it
	// up to a second to land before asserting. The mailer is the
	// in-process recordingSender so the wait is just goroutine
	// scheduling, not network.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(sender.Sent()) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	sent := sender.Sent()
	if got, want := len(sent), 1; got != want {
		t.Fatalf("sender.Sent() len = %d, want %d", got, want)
	}
	if got, want := sent[0].Kind, mailer.KindVerify; got != want {
		t.Errorf("sent[0].Kind = %q, want %q", got, want)
	}
	if got, want := sent[0].To, "unv@example.test"; got != want {
		t.Errorf("sent[0].To = %q, want %q", got, want)
	}
	if got, want := len(tokens.Created()), 1; got != want {
		t.Errorf("tokens.Created() len = %d, want %d", got, want)
	}
}

// TestHandleLoginSubmit_VerifiedEmail_SignsIn pins the verified-row
// happy path: a stamped email_verified_at lets the handler set the
// session cookie and redirect, with no resend leaking out.
func TestHandleLoginSubmit_VerifiedEmail_SignsIn(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	hash, err := HashPassword("correctbattery")
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}
	if _, err := store.CreatePlayer(t.Context(), "ver", "ver@example.test", hash, RoleAdmin); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	markVerified(t, store, "ver")

	tokens := &recordingVerifyTokenStore{}
	sender := &recordingSender{}
	handler := HandleLoginSubmit(discardLogger(), nil, LoginDeps{
		Players:       store,
		Sessions:      session.New([]byte("k"), true),
		Limiter:       NewLoginRateLimiter(time.Minute, nil),
		Mailer:        sender,
		Tokens:        tokens,
		ResendLimiter: NewVerifyResendLimiter(time.Minute, nil),
		BaseURL:       "https://topbanana.example",
	})

	rec := postForm(t, handler, "/login", url.Values{
		"email":    {"ver@example.test"},
		"password": {"correctbattery"},
	})

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d (body=%q)", got, want, rec.Body.String())
	}
	// Wait briefly to catch any rogue async send before asserting.
	time.Sleep(20 * time.Millisecond)
	if got, want := len(sender.Sent()), 0; got != want {
		t.Errorf("sender.Sent() len = %d, want %d (verified login should not resend)", got, want)
	}
	if got, want := len(tokens.Created()), 0; got != want {
		t.Errorf("tokens.Created() len = %d, want %d", got, want)
	}
}
