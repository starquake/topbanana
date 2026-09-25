package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/session"
	"github.com/starquake/topbanana/internal/store"
)

func TestRequireGameHost_AllowsAdmin(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	admin, err := players.CreatePlayer(t.Context(), "alice", "alice@example.test", "h", RoleAdmin)
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
	mw := RequireGameHost(next, players, sessions, nil, discardLogger())

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

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	// Pre-seed a credentialled admin so the next CreatePlayer call is not
	// auto-promoted to admin by the "first password-bearing registrant
	// becomes admin" rule. (The migration-seeded admin row has no
	// password_hash, so it does not count for that rule.)
	if _, err := players.CreatePlayer(
		t.Context(),
		"seed-admin",
		"seed-admin@example.test",
		"h",
		RoleAdmin,
	); err != nil {
		t.Fatalf("seed admin err = %v, want nil", err)
	}
	player, err := players.CreatePlayer(t.Context(), "bob", "bob@example.test", "h", RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called for player role")
	})

	sessions := session.New([]byte("k"), true)
	mw := RequireGameHost(next, players, sessions, nil, discardLogger())

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

// TestRequireGameHost_AnonymousSession_RedirectsToLogin pins #1045: an
// anonymous-session visitor (a petname-only row minted by playing a quiz)
// is treated like a cookieless visitor - 303 to /login carrying ?next= -
// instead of being shown the access-denied page meant for signed-in
// players who lack host rights.
func TestRequireGameHost_AnonymousSession_RedirectsToLogin(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	anon, err := players.CreateAnonymousPlayer(t.Context(), "anon-visitor")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	if !anon.IsAnonymous() {
		t.Fatalf("CreateAnonymousPlayer produced a non-anonymous row: %+v", anon)
	}

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called for an anonymous session")
	})

	sessions := session.New([]byte("k"), true)
	mw := RequireGameHost(next, players, sessions, nil, discardLogger())

	rec := httptest.NewRecorder()
	sessions.Set(rec, anon.ID, anon.SessionVersion)
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Location"), "/login?next=%2Fadmin%2Fquizzes"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if got, dontWant := rec.Body.String(), "Access denied"; strings.Contains(got, dontWant) {
		t.Errorf("body should not contain %q, got %q", dontWant, got)
	}
}

func TestRequireGameHost_NoCookie_RedirectsToLogin(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called without cookie")
	})

	mw := RequireGameHost(next, players, session.New([]byte("k"), true), nil, discardLogger())

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

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called without cookie")
	})

	mw := RequireGameHost(next, players, session.New([]byte("k"), true), nil, discardLogger())

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

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called for unknown player")
	})

	sessions := session.New([]byte("k"), true)
	mw := RequireGameHost(next, players, sessions, nil, discardLogger())

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

// TestRequireGameHost_AllowsHost pins that a Host (middle tier) reaches the
// dashboard routes (#538). A pre-seeded admin keeps the new row off the
// first-registrant promotion; the row is then bumped to Host directly.
func TestRequireGameHost_AllowsHost(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	if _, err := players.CreatePlayer(
		t.Context(),
		"seed-admin",
		"seed-admin@example.test",
		"h",
		RoleAdmin,
	); err != nil {
		t.Fatalf("seed admin err = %v, want nil", err)
	}
	host, err := players.CreatePlayer(t.Context(), "hank", "hank@example.test", "h", RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	if err = players.SetPlayerRole(t.Context(), host.ID, RoleHost); err != nil {
		t.Fatalf("SetPlayerRole err = %v, want nil", err)
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})

	sessions := session.New([]byte("k"), true)
	mw := RequireGameHost(next, players, sessions, nil, discardLogger())

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

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	admin, err := players.CreatePlayer(t.Context(), "alice", "alice@example.test", "h", RoleAdmin)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})

	sessions := session.New([]byte("k"), true)
	mw := RequireAdmin(next, players, sessions, discardLogger())

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

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	if _, err := players.CreatePlayer(
		t.Context(),
		"seed-admin",
		"seed-admin@example.test",
		"h",
		RoleAdmin,
	); err != nil {
		t.Fatalf("seed admin err = %v, want nil", err)
	}
	host, err := players.CreatePlayer(t.Context(), "hank", "hank@example.test", "h", RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	if err = players.SetPlayerRole(t.Context(), host.ID, RoleHost); err != nil {
		t.Fatalf("SetPlayerRole err = %v, want nil", err)
	}

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called for a host on an admin-only route")
	})

	sessions := session.New([]byte("k"), true)
	mw := RequireAdmin(next, players, sessions, discardLogger())

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

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	sessions := session.New([]byte("k"), true)

	var seenPlayer *Player
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		seenPlayer, _ = PlayerFromContext(r.Context())
		w.WriteHeader(http.StatusTeapot)
	})

	mw := EnsurePlayer(next, players, sessions, discardLogger())

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

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	existing, err := players.CreateAnonymousPlayer(t.Context(), "anon-existing")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}

	sessions := session.New([]byte("k"), true)

	var seenPlayer *Player
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenPlayer, _ = PlayerFromContext(r.Context())
	})

	mw := EnsurePlayer(next, players, sessions, discardLogger())

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

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	sessions := session.New([]byte("k"), true)

	var seenPlayer *Player
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenPlayer, _ = PlayerFromContext(r.Context())
	})

	mw := EnsurePlayer(next, players, sessions, discardLogger())

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

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	sessions := session.New([]byte("k"), true)

	var seenIDs []int64
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		p, _ := PlayerFromContext(r.Context())
		seenIDs = append(seenIDs, p.ID)
	})

	mw := EnsurePlayer(next, players, sessions, discardLogger())

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

func TestRequireGameHost_StoreError_500(t *testing.T) {
	t.Parallel()

	fakeStore := newFakePlayerStore()
	admin, err := fakeStore.CreatePlayer(t.Context(), "alice", "alice@example.test", "h", RoleAdmin)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	fakeStore.failGet = true

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called when store errors")
	})

	sessions := session.New([]byte("k"), true)
	mw := RequireGameHost(next, fakeStore, sessions, nil, discardLogger())

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

func TestEnsurePlayer_PetnameCollision_Retries(t *testing.T) {
	t.Parallel()

	fakeStore := newFakePlayerStore()
	// Force the first three CreateAnonymousPlayer calls to return
	// ErrDisplayNameTaken; the fourth attempt should succeed and produce a
	// regular petname row.
	fakeStore.forceAnonCollisions = 3
	sessions := session.New([]byte("k"), true)

	var seenPlayer *Player
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenPlayer, _ = PlayerFromContext(r.Context())
	})

	mw := EnsurePlayer(next, fakeStore, sessions, discardLogger())

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

	fakeStore := newFakePlayerStore()
	// Force every petname attempt (5) to collide so the fallback xid path
	// has to run.
	fakeStore.forceAnonCollisions = 5
	sessions := session.New([]byte("k"), true)

	var seenPlayer *Player
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenPlayer, _ = PlayerFromContext(r.Context())
	})

	mw := EnsurePlayer(next, fakeStore, sessions, discardLogger())

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

	fakeStore := newFakePlayerStore()
	fakeStore.forceAnonErr = errors.New("boom")
	sessions := session.New([]byte("k"), true)

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called when CreateAnonymousPlayer returns a non-collision error")
	})

	mw := EnsurePlayer(next, fakeStore, sessions, discardLogger())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/quizzes", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusInternalServerError; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func TestEnsurePlayer_GetPlayerError_500(t *testing.T) {
	t.Parallel()

	fakeStore := newFakePlayerStore()
	existing, err := fakeStore.CreateAnonymousPlayer(t.Context(), "anon-x")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	fakeStore.failGet = true

	sessions := session.New([]byte("k"), true)

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called on store error")
	})

	mw := EnsurePlayer(next, fakeStore, sessions, discardLogger())

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

// sessionCookieFor mints a session cookie for the player at its current version.
func sessionCookieFor(t *testing.T, sessions *session.Manager, p *Player) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	sessions.Set(rec, p.ID, p.SessionVersion)

	return rec.Result().Cookies()[0]
}

// TestRequireAuthenticated_LoginApproval pins #1327's defence in depth: under
// LOGIN_APPROVAL_REQUIRED a live cookie for an unapproved account reads as
// signed out, and the rule is off when the policy is.
func TestRequireAuthenticated_LoginApproval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		required bool
		approve  bool
		wantCode int
	}{
		{name: "required unapproved", required: true, approve: false, wantCode: http.StatusSeeOther},
		{name: "required approved", required: true, approve: true, wantCode: http.StatusTeapot},
		{name: "not required unapproved", required: false, approve: false, wantCode: http.StatusTeapot},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
			if _, err := players.CreatePlayer(t.Context(), "boot", "boot@example.test", "h", RolePlayer); err != nil {
				t.Fatalf("bootstrap CreatePlayer err = %v, want nil", err)
			}
			p, err := players.CreatePlayer(t.Context(), "held", "held@example.test", "h", RolePlayer)
			if err != nil {
				t.Fatalf("CreatePlayer err = %v, want nil", err)
			}
			if tt.approve {
				if _, err := players.SetPlayerApprovedNow(t.Context(), p.ID); err != nil {
					t.Fatalf("SetPlayerApprovedNow err = %v, want nil", err)
				}
			}
			sessions := session.New([]byte("k"), true).WithLoginApprovalRequired(tt.required)
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTeapot)
			})
			mw := RequireAuthenticated(next, players, sessions, discardLogger())

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/profile", nil)
			req.AddCookie(sessionCookieFor(t, sessions, p))
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)

			if got, want := rec.Code, tt.wantCode; got != want {
				t.Errorf("status = %d, want %d", got, want)
			}
			_, ok := AuthenticatedSessionPlayer(req, players, sessions)
			if got, want := ok, tt.wantCode == http.StatusTeapot; got != want {
				t.Errorf("AuthenticatedSessionPlayer ok = %v, want %v (must agree with the gate)", got, want)
			}
		})
	}
}

// TestEnsurePlayer_LoginApproval_AnonymousGuestNotHeld pins that the approval
// rule never catches a guest row, which carries no credentials.
func TestEnsurePlayer_LoginApproval_AnonymousGuestNotHeld(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	guest, err := players.CreateAnonymousPlayer(t.Context(), "guest")
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer err = %v, want nil", err)
	}
	sessions := session.New([]byte("k"), true).WithLoginApprovalRequired(true)
	var seen *Player
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, _ = PlayerFromContext(r.Context())
	})
	mw := EnsurePlayer(next, players, sessions, discardLogger())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/players/me", nil)
	req.AddCookie(sessionCookieFor(t, sessions, guest))
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if seen == nil {
		t.Fatal("no player on context")
	}
	if got, want := seen.ID, guest.ID; got != want {
		t.Errorf("player.ID = %d, want %d (guest kept)", got, want)
	}
}

// TestEnsurePlayer_LoginApproval_HeldAccountReplaced pins that EnsurePlayer does
// not serve the API as a held account: it mints a fresh guest instead.
func TestEnsurePlayer_LoginApproval_HeldAccountReplaced(t *testing.T) {
	t.Parallel()

	players := store.NewPlayerStore(dbtest.Open(t), discardLogger())
	if _, err := players.CreatePlayer(t.Context(), "boot", "boot@example.test", "h", RolePlayer); err != nil {
		t.Fatalf("bootstrap CreatePlayer err = %v, want nil", err)
	}
	held, err := players.CreatePlayer(t.Context(), "held", "held@example.test", "h", RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	sessions := session.New([]byte("k"), true).WithLoginApprovalRequired(true)
	var seen *Player
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, _ = PlayerFromContext(r.Context())
	})
	mw := EnsurePlayer(next, players, sessions, discardLogger())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/players/me", nil)
	req.AddCookie(sessionCookieFor(t, sessions, held))
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if seen == nil {
		t.Fatal("no player on context")
	}
	if seen.ID == held.ID {
		t.Errorf("player.ID = %d, want a fresh guest, not the held account", seen.ID)
	}
}
