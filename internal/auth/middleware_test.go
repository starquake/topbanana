package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/session"
)

func TestRequireAdmin_AllowsAdmin(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	admin, err := store.CreatePlayer(t.Context(), "alice", "h", auth.RoleAdmin)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})

	sessions := session.New([]byte("k"))
	mw := auth.RequireAdmin(next, store, sessions, discardLogger())

	rec := httptest.NewRecorder()
	sessions.Set(rec, admin.ID)
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
}

func TestRequireAdmin_DeniesPlayer(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	// Pre-seed an admin so the next CreatePlayer call is not auto-promoted to admin
	// by the "first password-bearing registrant becomes admin" rule.
	if _, err := store.CreatePlayer(t.Context(), "admin", "h", auth.RoleAdmin); err != nil {
		t.Fatalf("seed admin err = %v, want nil", err)
	}
	player, err := store.CreatePlayer(t.Context(), "bob", "h", auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called for player role")
	})

	sessions := session.New([]byte("k"))
	mw := auth.RequireAdmin(next, store, sessions, discardLogger())

	rec := httptest.NewRecorder()
	sessions.Set(rec, player.ID)
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Location"), "/login"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestRequireAdmin_NoCookie_RedirectsToLogin(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called without cookie")
	})

	mw := auth.RequireAdmin(next, store, session.New([]byte("k")), discardLogger())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Location"), "/login"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestRequireAdmin_UnknownPlayerID_RedirectsToLogin(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called for unknown player")
	})

	sessions := session.New([]byte("k"))
	mw := auth.RequireAdmin(next, store, sessions, discardLogger())

	rec := httptest.NewRecorder()
	sessions.Set(rec, 9999)
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func TestRequireAdmin_StoreError_500(t *testing.T) {
	t.Parallel()

	store := newStubPlayerStore()
	admin, err := store.CreatePlayer(t.Context(), "alice", "h", auth.RoleAdmin)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	store.failGet = true

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next should not be called when store errors")
	})

	sessions := session.New([]byte("k"))
	mw := auth.RequireAdmin(next, store, sessions, discardLogger())

	rec := httptest.NewRecorder()
	sessions.Set(rec, admin.ID)
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusInternalServerError; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}
