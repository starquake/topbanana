package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/session"
)

// findCookie returns the first response cookie with the given name and a
// boolean reporting whether it was found. Used by the EnsurePlayer tests to
// assert that a fresh session cookie is set on the response.
func findCookie(rec *httptest.ResponseRecorder, name string) (*http.Cookie, bool) {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c, true
		}
	}

	return nil, false
}

func TestRequireGameHost_AllowsAdmin(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	admin, err := store.CreatePlayer(t.Context(), "alice", "alice@example.test", "h", RoleAdmin)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	called := false
	var seenPlayer *Player
	var seenOK bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		seenPlayer, seenOK = PlayerFromContext(r.Context())
		w.WriteHeader(http.StatusTeapot)
	})

	sessions := session.New([]byte("k"), true)
	mw := RequireGameHost(next, store, sessions, nil, discardLogger())

	rec := httptest.NewRecorder()
	sessions.Set(rec, admin.ID, 0)
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if !called {
		t.Error("next handler was not called")
	}
	if got, want := rec.Code, http.StatusTeapot; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if !seenOK {
		t.Fatal("PlayerFromContext ok = false, want true (admin should be on context)")
	}
	if got, want := seenPlayer.ID, admin.ID; got != want {
		t.Errorf("player.ID on context = %d, want %d", got, want)
	}
	if got, want := seenPlayer.DisplayName, "alice"; got != want {
		t.Errorf("player.DisplayName on context = %q, want %q", got, want)
	}
}

func TestRequireGameHost_DeniesPlayer(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	// Pre-seed an admin so the next CreatePlayer call is not auto-promoted to admin
	// by the "first password-bearing registrant becomes admin" rule.
	if _, err := store.CreatePlayer(t.Context(), "admin", "admin@example.test", "h", RoleAdmin); err != nil {
		t.Fatalf("seed admin err = %v, want nil", err)
	}
	player, err := store.CreatePlayer(t.Context(), "bob", "bob@example.test", "h", RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called for player role")
	})

	sessions := session.New([]byte("k"), true)
	mw := RequireGameHost(next, store, sessions, nil, discardLogger())

	rec := httptest.NewRecorder()
	sessions.Set(rec, player.ID, 0)
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusForbidden; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "Access denied"; !strings.Contains(got, want) {
		t.Errorf("body should contain %q, got %q", want, got)
	}
	if got, want := rec.Body.String(), "bob"; !strings.Contains(got, want) {
		t.Errorf("body should contain signed-in displayName %q, got %q", want, got)
	}
}

func TestRequireGameHost_NoCookie_RedirectsToLogin(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called without cookie")
	})

	mw := RequireGameHost(next, store, session.New([]byte("k"), true), nil, discardLogger())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	// #449: GET to a protected route carries the original URI as
	// ?next=<encoded> so the login flow can drop the visitor back on
	// the page they tried to reach.
	if got, want := rec.Header().Get("Location"), "/login?next=%2Fadmin%2Fquizzes"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// TestRequireGameHost_PostDropsNext pins #449's safe-method gate: a POST
// that hits an expired session still redirects to /login but does
// NOT carry the URI as ?next= because the form body cannot be
// replayed after the visitor signs back in.
func TestRequireGameHost_PostDropsNext(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called without cookie")
	})

	mw := RequireGameHost(next, store, session.New([]byte("k"), true), nil, discardLogger())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/quizzes", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Location"), "/login"; got != want {
		t.Errorf("Location = %q, want %q (POST must not carry ?next=)", got, want)
	}
}

func TestRequireGameHost_UnknownPlayerID_RedirectsToLogin(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called for unknown player")
	})

	sessions := session.New([]byte("k"), true)
	mw := RequireGameHost(next, store, sessions, nil, discardLogger())

	rec := httptest.NewRecorder()
	sessions.Set(rec, 9999, 0)
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func TestRequireGameHost_StoreError_500(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	admin, err := store.CreatePlayer(t.Context(), "alice", "alice@example.test", "h", RoleAdmin)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	store.failGet = true

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called when store errors")
	})

	sessions := session.New([]byte("k"), true)
	mw := RequireGameHost(next, store, sessions, nil, discardLogger())

	rec := httptest.NewRecorder()
	sessions.Set(rec, admin.ID, 0)
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusInternalServerError; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

// TestRequireGameHost_AllowsHost pins that a Host (middle tier) reaches the
// dashboard routes (#538). A pre-seeded admin keeps the new row off the
// first-registrant promotion; the row is then bumped to Host directly.
func TestRequireGameHost_AllowsHost(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	if _, err := store.CreatePlayer(t.Context(), "admin", "admin@example.test", "h", RoleAdmin); err != nil {
		t.Fatalf("seed admin err = %v, want nil", err)
	}
	host, err := store.CreatePlayer(t.Context(), "hank", "hank@example.test", "h", RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	host.Role = RoleHost

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})

	sessions := session.New([]byte("k"), true)
	mw := RequireGameHost(next, store, sessions, nil, discardLogger())

	rec := httptest.NewRecorder()
	sessions.Set(rec, host.ID, 0)
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if !called {
		t.Error("next handler was not called for host")
	}
	if got, want := rec.Code, http.StatusTeapot; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

// TestRequireAdmin_AllowsAdmin pins that an Admin (top tier) reaches the
// admin-only routes (#538).
func TestRequireAdmin_AllowsAdmin(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	admin, err := store.CreatePlayer(t.Context(), "alice", "alice@example.test", "h", RoleAdmin)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})

	sessions := session.New([]byte("k"), true)
	mw := RequireAdmin(next, store, sessions, discardLogger())

	rec := httptest.NewRecorder()
	sessions.Set(rec, admin.ID, 0)
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/players", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if !called {
		t.Error("next handler was not called for admin")
	}
	if got, want := rec.Code, http.StatusTeapot; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

// TestRequireAdmin_DeniesHostWith404 pins that a Host hitting an Admin-only
// route gets a plain 404 - the admin surface's existence stays hidden from
// Hosts (#320/#538), so next must not run and no access-denied page leaks.
func TestRequireAdmin_DeniesHostWith404(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	if _, err := store.CreatePlayer(t.Context(), "admin", "admin@example.test", "h", RoleAdmin); err != nil {
		t.Fatalf("seed admin err = %v, want nil", err)
	}
	host, err := store.CreatePlayer(t.Context(), "hank", "hank@example.test", "h", RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	host.Role = RoleHost

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called for a host on an admin-only route")
	})

	sessions := session.New([]byte("k"), true)
	mw := RequireAdmin(next, store, sessions, discardLogger())

	rec := httptest.NewRecorder()
	sessions.Set(rec, host.ID, 0)
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/players", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusNotFound; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func TestEnsurePlayer_NoCookie_CreatesAnonymousAndSetsCookie(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	sessions := session.New([]byte("k"), true)

	var seenPlayer *Player
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		seenPlayer, _ = PlayerFromContext(r.Context())
		w.WriteHeader(http.StatusTeapot)
	})

	mw := EnsurePlayer(next, store, sessions, discardLogger())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/quizzes", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if !called {
		t.Fatal("next handler was not called")
	}
	if got, want := rec.Code, http.StatusTeapot; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if seenPlayer == nil {
		t.Fatal("PlayerFromContext returned nil player; middleware should have populated one")
	}
	if !seenPlayer.IsAnonymous() {
		t.Errorf("seenPlayer.IsAnonymous() = false, want true (PasswordHash = %q)", seenPlayer.PasswordHash)
	}
	// EnsurePlayer should mint a petname-style "Adjective-Adjective-Noun"
	// displayName, not the legacy "anon-<xid>" form (the xid form is the
	// last-resort fallback only).
	if got := seenPlayer.DisplayName; strings.HasPrefix(got, "anon-") {
		t.Errorf("seenPlayer.DisplayName = %q, want a petname-style name (no anon- prefix)", got)
	}
	if got, want := strings.Count(seenPlayer.DisplayName, "-"), 2; got != want {
		t.Errorf(
			"seenPlayer.DisplayName = %q, want %d hyphens (Adjective-Adjective-Noun)",
			seenPlayer.DisplayName,
			want,
		)
	}
	cookie, ok := findCookie(rec, session.CookieName)
	if !ok {
		t.Fatal("session cookie was not set on the response")
	}
	if cookie.Value == "" {
		t.Error("session cookie value is empty")
	}
}

func TestEnsurePlayer_ValidCookie_ReusesExistingRow(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	existing, err := store.CreateAnonymousPlayer(t.Context(), "anon-existing")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	sessions := session.New([]byte("k"), true)

	var seenPlayer *Player
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenPlayer, _ = PlayerFromContext(r.Context())
	})

	mw := EnsurePlayer(next, store, sessions, discardLogger())

	rec := httptest.NewRecorder()
	sessions.Set(rec, existing.ID, 0)
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/quizzes", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if seenPlayer == nil {
		t.Fatal("PlayerFromContext returned nil player; middleware should have loaded the existing row")
	}
	if got, want := seenPlayer.ID, existing.ID; got != want {
		t.Errorf("seenPlayer.ID = %d, want %d (existing row)", got, want)
	}
	if _, ok := findCookie(rec, session.CookieName); ok {
		t.Error("session cookie should not be re-set when the cookie is valid")
	}
}

func TestEnsurePlayer_DeletedPlayer_MintsNewAnonymous(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	sessions := session.New([]byte("k"), true)

	var seenPlayer *Player
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenPlayer, _ = PlayerFromContext(r.Context())
	})

	mw := EnsurePlayer(next, store, sessions, discardLogger())

	// Issue a cookie pointing at an ID that does not exist in the store.
	rec := httptest.NewRecorder()
	sessions.Set(rec, 9999, 0)
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/quizzes", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if seenPlayer == nil {
		t.Fatal("PlayerFromContext returned nil player; middleware should have minted a new anonymous row")
	}
	if got, dontWant := seenPlayer.ID, int64(9999); got == dontWant {
		t.Errorf("seenPlayer.ID = %d, should not equal the deleted ID %d", got, dontWant)
	}
	if !seenPlayer.IsAnonymous() {
		t.Error("seenPlayer.IsAnonymous() = false, want true")
	}
	if _, ok := findCookie(rec, session.CookieName); !ok {
		t.Error("session cookie should have been re-issued when the cookie referenced a deleted row")
	}
}

func TestEnsurePlayer_TwoCookielessRequests_TwoDistinctPlayers(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	sessions := session.New([]byte("k"), true)

	var seenIDs []int64
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		p, _ := PlayerFromContext(r.Context())
		seenIDs = append(seenIDs, p.ID)
	})

	mw := EnsurePlayer(next, store, sessions, discardLogger())

	for range 2 {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/quizzes", nil)
		mw.ServeHTTP(httptest.NewRecorder(), req)
	}

	if got, want := len(seenIDs), 2; got != want {
		t.Fatalf("len(seenIDs) = %d, want %d", got, want)
	}
	if seenIDs[0] == seenIDs[1] {
		t.Errorf("two cookieless requests produced the same player ID %d, want distinct", seenIDs[0])
	}
}

func TestEnsurePlayer_PetnameCollision_Retries(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	// Force the first three CreateAnonymousPlayer calls to return
	// ErrDisplayNameTaken; the fourth attempt should succeed and produce a
	// regular petname row.
	store.forceAnonCollisions = 3
	sessions := session.New([]byte("k"), true)

	var seenPlayer *Player
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenPlayer, _ = PlayerFromContext(r.Context())
	})

	mw := EnsurePlayer(next, store, sessions, discardLogger())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/quizzes", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if seenPlayer == nil {
		t.Fatal("PlayerFromContext returned nil player; middleware should have retried past the collisions")
	}
	if got := seenPlayer.DisplayName; strings.HasPrefix(got, "anon-") {
		t.Errorf("seenPlayer.DisplayName = %q, want a petname-style name (no anon- fallback)", got)
	}
	if got, want := strings.Count(seenPlayer.DisplayName, "-"), 2; got != want {
		t.Errorf("seenPlayer.DisplayName = %q, want %d hyphens", seenPlayer.DisplayName, want)
	}
}

func TestEnsurePlayer_PetnameExhausted_FallsBackToXid(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	// Force every petname attempt (5) to collide so the fallback xid path
	// has to run.
	store.forceAnonCollisions = 5
	sessions := session.New([]byte("k"), true)

	var seenPlayer *Player
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenPlayer, _ = PlayerFromContext(r.Context())
	})

	mw := EnsurePlayer(next, store, sessions, discardLogger())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/quizzes", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if seenPlayer == nil {
		t.Fatal("PlayerFromContext returned nil player; fallback should have produced one")
	}
	if got, want := seenPlayer.DisplayName[:5], "anon-"; got != want {
		t.Errorf("seenPlayer.DisplayName prefix = %q, want %q (xid fallback after exhausted retries)", got, want)
	}
}

func TestEnsurePlayer_CreateAnonymousNonCollisionError_500(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	store.forceAnonErr = errors.New("boom")
	sessions := session.New([]byte("k"), true)

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called when CreateAnonymousPlayer returns a non-collision error")
	})

	mw := EnsurePlayer(next, store, sessions, discardLogger())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/quizzes", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusInternalServerError; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func TestEnsurePlayer_GetPlayerError_500(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	existing, err := store.CreateAnonymousPlayer(t.Context(), "anon-x")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	store.failGet = true

	sessions := session.New([]byte("k"), true)

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called on store error")
	})

	mw := EnsurePlayer(next, store, sessions, discardLogger())

	rec := httptest.NewRecorder()
	sessions.Set(rec, existing.ID, 0)
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/quizzes", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusInternalServerError; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "internal error"; !strings.Contains(got, want) {
		t.Errorf("body = %q, should contain %q", got, want)
	}
}
