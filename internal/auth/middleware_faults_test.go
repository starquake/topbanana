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

func TestRequireGameHost_StoreError_500(t *testing.T) {
	t.Parallel()

	store := newFakePlayerStore()
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

func TestEnsurePlayer_PetnameCollision_Retries(t *testing.T) {
	t.Parallel()

	store := newFakePlayerStore()
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

	store := newFakePlayerStore()
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

	store := newFakePlayerStore()
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

	store := newFakePlayerStore()
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
