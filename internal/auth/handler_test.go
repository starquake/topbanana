package auth_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/session"
)

// stubPlayerStore is an in-memory PlayerStore for tests.
type stubPlayerStore struct {
	mu      sync.Mutex
	byID    map[int64]*auth.Player
	byName  map[string]*auth.Player
	nextID  int64
	failGet bool
}

func newStubPlayerStore() *stubPlayerStore {
	return &stubPlayerStore{
		byID:   map[int64]*auth.Player{},
		byName: map[string]*auth.Player{},
		nextID: 1,
	}
}

func (s *stubPlayerStore) GetPlayerByUsername(_ context.Context, username string) (*auth.Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.byName[username]
	if !ok {
		return nil, auth.ErrPlayerNotFound
	}

	return p, nil
}

func (s *stubPlayerStore) GetPlayerByID(_ context.Context, id int64) (*auth.Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failGet {
		return nil, errors.New("boom")
	}
	p, ok := s.byID[id]
	if !ok {
		return nil, auth.ErrPlayerNotFound
	}

	return p, nil
}

// CreatePlayer mirrors the SQL semantics of internal/queries/players.sql:
// honour an explicit "admin" request, otherwise promote the very first
// password-bearing player to admin so the "first registrant becomes admin"
// rule is observed atomically.
func (s *stubPlayerStore) CreatePlayer(
	_ context.Context,
	username, passwordHash, requestedRole string,
) (*auth.Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byName[username]; exists {
		return nil, auth.ErrUsernameTaken
	}

	role := requestedRole
	if role != auth.RoleAdmin {
		hasPasswordBearer := false
		for _, existing := range s.byID {
			if existing.PasswordHash != "" {
				hasPasswordBearer = true

				break
			}
		}
		if !hasPasswordBearer {
			role = auth.RoleAdmin
		} else {
			role = auth.RolePlayer
		}
	}

	p := &auth.Player{
		ID:           s.nextID,
		Username:     username,
		PasswordHash: passwordHash,
		Role:         role,
	}
	s.nextID++
	s.byID[p.ID] = p
	s.byName[username] = p

	return p, nil
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

	handler := auth.HandleRegisterForm(discardLogger())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/register", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), `name="username"`; !strings.Contains(got, want) {
		t.Errorf("body did not contain %q", want)
	}
}

func TestHandleRegisterSubmit_FirstUser_BecomesAdmin(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	handler := auth.HandleRegisterSubmit(discardLogger(), store, session.New([]byte("k")), nil)

	rec := postForm(t, handler, "/register", url.Values{
		"username": {"alice"},
		"password": {"correctbattery"},
	})

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d (body=%q)", got, want, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), "/admin/quizzes"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}

	p, err := store.GetPlayerByUsername(t.Context(), "alice")
	if err != nil {
		t.Fatalf("GetPlayerByUsername err = %v, want nil", err)
	}
	if got, want := p.Role, auth.RoleAdmin; got != want {
		t.Errorf("Role = %q, want %q", got, want)
	}

	// Cookie was set
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == session.CookieName {
			found = true
		}
	}
	if !found {
		t.Error("session cookie was not set")
	}
}

func TestHandleRegisterSubmit_SecondUser_DefaultsToPlayer(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	// Pre-seed first admin.
	if _, err := store.CreatePlayer(t.Context(), "admin", "hash", auth.RoleAdmin); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	handler := auth.HandleRegisterSubmit(discardLogger(), store, session.New([]byte("k")), nil)
	rec := postForm(t, handler, "/register", url.Values{
		"username": {"bob"},
		"password": {"correctbattery"},
	})

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d (body=%q)", got, want, rec.Body.String())
	}

	p, err := store.GetPlayerByUsername(t.Context(), "bob")
	if err != nil {
		t.Fatalf("GetPlayerByUsername err = %v, want nil", err)
	}
	if got, want := p.Role, auth.RolePlayer; got != want {
		t.Errorf("Role = %q, want %q", got, want)
	}
}

func TestHandleRegisterSubmit_AdminUsernamesEnv_PromotesToAdmin(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	// Pre-seed first user so the count > 0 and the env var path is exercised.
	if _, err := store.CreatePlayer(t.Context(), "first", "h", auth.RolePlayer); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	handler := auth.HandleRegisterSubmit(
		discardLogger(),
		store,
		session.New([]byte("k")),
		[]string{"alice", "carol"},
	)
	rec := postForm(t, handler, "/register", url.Values{
		"username": {"carol"},
		"password": {"correctbattery"},
	})

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	p, err := store.GetPlayerByUsername(t.Context(), "carol")
	if err != nil {
		t.Fatalf("GetPlayerByUsername err = %v, want nil", err)
	}
	if got, want := p.Role, auth.RoleAdmin; got != want {
		t.Errorf("Role = %q, want %q", got, want)
	}
}

func TestHandleRegisterSubmit_PasswordTooShort(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	handler := auth.HandleRegisterSubmit(discardLogger(), store, session.New([]byte("k")), nil)

	rec := postForm(t, handler, "/register", url.Values{
		"username": {"alice"},
		"password": {"short"},
	})

	if got, want := rec.Code, http.StatusBadRequest; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "at least 13 characters"; !strings.Contains(got, want) {
		t.Errorf("body did not mention min length, got %q", got)
	}
	if _, err := store.GetPlayerByUsername(t.Context(), "alice"); !errors.Is(err, auth.ErrPlayerNotFound) {
		t.Errorf("player should not have been created, GetPlayerByUsername err = %v", err)
	}
}

func TestHandleRegisterSubmit_DuplicateUsername(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	if _, err := store.CreatePlayer(t.Context(), "alice", "h", auth.RolePlayer); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	handler := auth.HandleRegisterSubmit(discardLogger(), store, session.New([]byte("k")), nil)
	rec := postForm(t, handler, "/register", url.Values{
		"username": {"alice"},
		"password": {"correctbattery"},
	})

	if got, want := rec.Code, http.StatusConflict; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "already taken"; !strings.Contains(got, want) {
		t.Errorf("body did not mention duplicate, got %q", got)
	}
}

func TestHandleLoginForm_GET_RendersForm(t *testing.T) {
	t.Parallel()

	handler := auth.HandleLoginForm(discardLogger())

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

func TestHandleLoginSubmit_Success(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	hash, err := auth.HashPassword("correctbattery")
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}
	if _, err := store.CreatePlayer(t.Context(), "alice", hash, auth.RoleAdmin); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	handler := auth.HandleLoginSubmit(discardLogger(), store, session.New([]byte("k")))
	rec := postForm(t, handler, "/login", url.Values{
		"username": {"alice"},
		"password": {"correctbattery"},
	})

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d (body=%q)", got, want, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), "/admin/quizzes"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestHandleLoginSubmit_BadCredentials_UnknownUser(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	handler := auth.HandleLoginSubmit(discardLogger(), store, session.New([]byte("k")))

	rec := postForm(t, handler, "/login", url.Values{
		"username": {"ghost"},
		"password": {"correctbattery"},
	})

	if got, want := rec.Code, http.StatusUnauthorized; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "Invalid username or password"; !strings.Contains(got, want) {
		t.Errorf("body should mention invalid credentials, got %q", got)
	}
}

func TestHandleLoginSubmit_BadCredentials_WrongPassword(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	hash, err := auth.HashPassword("right-password-yes")
	if err != nil {
		t.Fatalf("HashPassword err = %v, want nil", err)
	}
	if _, err := store.CreatePlayer(t.Context(), "alice", hash, auth.RolePlayer); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	handler := auth.HandleLoginSubmit(discardLogger(), store, session.New([]byte("k")))
	rec := postForm(t, handler, "/login", url.Values{
		"username": {"alice"},
		"password": {"wrong-password-no"},
	})

	if got, want := rec.Code, http.StatusUnauthorized; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "Invalid username or password"; !strings.Contains(got, want) {
		t.Errorf("body should mention invalid credentials, got %q", got)
	}
}

func TestHandleLoginSubmit_RejectsEmptyHash(t *testing.T) {
	t.Parallel()

	// Seed a player with no password hash (legacy admin from migration).
	store := newStubPlayerStore()
	if _, err := store.CreatePlayer(t.Context(), "legacy", "", auth.RoleAdmin); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	handler := auth.HandleLoginSubmit(discardLogger(), store, session.New([]byte("k")))
	rec := postForm(t, handler, "/login", url.Values{
		"username": {"legacy"},
		"password": {"anything-goes-here-13"},
	})

	if got, want := rec.Code, http.StatusUnauthorized; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func TestHandleRegisterSubmit_WhitespaceOnlyUsername(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	handler := auth.HandleRegisterSubmit(discardLogger(), store, session.New([]byte("k")), nil)

	rec := postForm(t, handler, "/register", url.Values{
		"username": {"   "},
		"password": {"correctbattery"},
	})

	if got, want := rec.Code, http.StatusBadRequest; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "Username is required"; !strings.Contains(got, want) {
		t.Errorf("body did not mention required username, got %q", got)
	}
	// Whitespace-trimmed name should not have been created.
	if _, err := store.GetPlayerByUsername(t.Context(), ""); !errors.Is(err, auth.ErrPlayerNotFound) {
		t.Errorf("empty player should not exist, err = %v", err)
	}
}

func TestHandleRegisterSubmit_PasswordExactlyMinLength(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	handler := auth.HandleRegisterSubmit(discardLogger(), store, session.New([]byte("k")), nil)

	password := strings.Repeat("a", auth.MinPasswordLength) // exactly 13 characters
	rec := postForm(t, handler, "/register", url.Values{
		"username": {"alice"},
		"password": {password},
	})

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d (body=%q)", got, want, rec.Body.String())
	}
	if _, err := store.GetPlayerByUsername(t.Context(), "alice"); err != nil {
		t.Errorf("player should have been created, err = %v", err)
	}
}

func TestHandleLogout_NoCookie(t *testing.T) {
	t.Parallel()

	handler := auth.HandleLogout(session.New([]byte("k")))

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

	handler := auth.HandleLogout(session.New([]byte("k")))

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
