//go:build integration

package auth_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/session"
	"github.com/starquake/topbanana/internal/store"
)

func TestHandleResetForm_LivePreflightRendersForm(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	player, err := stores.Players.CreatePlayer(
		t.Context(), "reset-live", "reset-live@example.test", "old-hash", RolePlayer,
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	raw := seedResetToken(t, stores.ResetTokens, player.ID, time.Now().Add(time.Hour))

	rec := runResetForm(t, stores.ResetTokens, raw)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), `name="token"`; !strings.Contains(got, want) {
		t.Errorf("body should render form token field %q", want)
	}
}

func TestHandleResetForm_EmptyTokenRendersInvalid(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	rec := runResetForm(t, stores.ResetTokens, "")

	if got, want := rec.Code, http.StatusGone; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func TestHandleResetForm_DeadTokenRendersInvalid(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	player, err := stores.Players.CreatePlayer(
		t.Context(), "reset-dead", "reset-dead@example.test", "old-hash", RolePlayer,
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	// A past expiry yields a row the preflight peek classifies as not live.
	raw := seedResetToken(t, stores.ResetTokens, player.ID, time.Now().Add(-time.Hour))

	rec := runResetForm(t, stores.ResetTokens, raw)

	if got, want := rec.Code, http.StatusGone; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := rec.Body.String(), "Link is no longer valid"; !strings.Contains(got, want) {
		t.Errorf("body should contain %q", want)
	}
}

// TestHandleResetForm_LookupErrorFallsOpen forces the preflight lookup
// to error with a closed DB so the handler must fall open and render the
// form (the POST consume is the real security boundary).
func TestHandleResetForm_LookupErrorFallsOpen(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	stores := store.New(db, discardLogger())
	if err := db.Close(); err != nil {
		t.Fatalf("close db err = %v, want nil", err)
	}

	rec := runResetForm(t, stores.ResetTokens, "raw-token")

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d (preflight error must fall open)", got, want)
	}
}

func TestHandleResetSubmit_AutoLoginToRoleLanding(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	admin, err := stores.Players.CreatePlayer(
		t.Context(), "reset-admin", "reset-admin@example.test", "old-hash", RoleAdmin,
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	raw := seedResetToken(t, stores.ResetTokens, admin.ID, time.Now().Add(time.Hour))

	sessions := session.New([]byte("test-session-key"), false)
	rec := runResetSubmit(t, stores.ResetTokens, sessions, stores.Players, raw)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Location"), "/admin/quizzes"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}

	id, version, ok := decodeSessionCookie(t, sessions, rec)
	if !ok {
		t.Fatal("reset response did not set a valid session cookie")
	}
	if got, want := id, admin.ID; got != want {
		t.Errorf("session player id = %d, want %d", got, want)
	}
	// The cookie must carry the post-rotation version (the consume bumps
	// session_version), not the stale pre-reset value, or the next
	// request would reject it.
	if got, want := version, admin.SessionVersion+1; got != want {
		t.Errorf("session version = %d, want %d (post-rotation value)", got, want)
	}
}

func TestHandleResetSubmit_PlayerLandsOnHome(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	// The first password-bearing registrant is auto-promoted to admin, so
	// seed an admin first to keep the next row a plain player.
	if _, err := stores.Players.CreatePlayer(
		t.Context(), "seed-admin", "seed-admin@example.test", "h", RoleAdmin,
	); err != nil {
		t.Fatalf("seed admin err = %v, want nil", err)
	}
	p, err := stores.Players.CreatePlayer(
		t.Context(), "reset-player", "reset-player@example.test", "old-hash", RolePlayer,
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	raw := seedResetToken(t, stores.ResetTokens, p.ID, time.Now().Add(time.Hour))

	sessions := session.New([]byte("test-session-key"), false)
	rec := runResetSubmit(t, stores.ResetTokens, sessions, stores.Players, raw)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Location"), "/"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if _, _, ok := decodeSessionCookie(t, sessions, rec); !ok {
		t.Error("reset response did not set a valid session cookie")
	}
}

// TestHandleResetSubmit_LookupFailureFallsBackToLogin pins the
// graceful-degradation path: the password change already committed, so a
// failed auto-login lookup must clear the session and 303 to /login
// rather than 500 in a way that hides the successful reset. The consume
// runs against a live store; the auto-login lookup runs against a
// separate closed store, so the lookup fails for real after a real
// consume.
func TestHandleResetSubmit_LookupFailureFallsBackToLogin(t *testing.T) {
	t.Parallel()

	consumeStores := store.New(dbtest.Open(t), discardLogger())
	player, err := consumeStores.Players.CreatePlayer(
		t.Context(), "reset-degraded", "reset-degraded@example.test", "old-hash", RolePlayer,
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	raw := seedResetToken(t, consumeStores.ResetTokens, player.ID, time.Now().Add(time.Hour))

	failDB := dbtest.Open(t)
	failStores := store.New(failDB, discardLogger())
	if err := failDB.Close(); err != nil {
		t.Fatalf("close db err = %v, want nil", err)
	}

	sessions := session.New([]byte("test-session-key"), false)
	rec := runResetSubmit(t, consumeStores.ResetTokens, sessions, failStores.Players, raw)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Location"), "/login"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	// Fallback clears the session: the cookie is set with an empty value.
	if _, _, ok := decodeSessionCookie(t, sessions, rec); ok {
		t.Error("fallback must not leave a live session cookie")
	}
}

func TestHandleResetSubmit_InvalidTokenRendersGone(t *testing.T) {
	t.Parallel()

	stores := store.New(dbtest.Open(t), discardLogger())
	player, err := stores.Players.CreatePlayer(
		t.Context(), "reset-consumed", "reset-consumed@example.test", "old-hash", RolePlayer,
	)
	if err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	raw := seedResetToken(t, stores.ResetTokens, player.ID, time.Now().Add(time.Hour))
	// Burn the token once so the handler's consume reports it invalid.
	if _, err := stores.ResetTokens.ConsumeResetToken(t.Context(), HashResetToken(raw), "burned-hash"); err != nil {
		t.Fatalf("first ConsumeResetToken err = %v, want nil", err)
	}

	sessions := session.New([]byte("test-session-key"), false)
	rec := runResetSubmit(t, stores.ResetTokens, sessions, stores.Players, raw)

	if got, want := rec.Code, http.StatusGone; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

// seedResetToken mints a raw reset token, stores its hash for the given
// player with the supplied expiry through the real store, and returns
// the raw token so the caller can drive the handler with it. A past
// expiry yields the dead row the preflight and consume paths reject.
func seedResetToken(t *testing.T, tokens ResetTokenStore, playerID int64, expiresAt time.Time) string {
	t.Helper()

	raw, hash, err := GenerateResetToken()
	if err != nil {
		t.Fatalf("GenerateResetToken err = %v, want nil", err)
	}
	if err := tokens.CreateResetToken(t.Context(), hash, playerID, expiresAt); err != nil {
		t.Fatalf("CreateResetToken err = %v, want nil", err)
	}

	return raw
}

func runResetForm(t *testing.T, tokens ResetTokenStore, raw string) *httptest.ResponseRecorder {
	t.Helper()

	handler := HandleResetForm(discardLogger(), nil, tokens)
	target := "/reset-password"
	if raw != "" {
		target += "?token=" + raw
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func runResetSubmit(
	t *testing.T,
	tokens ResetTokenStore,
	sessions *session.Manager,
	players PlayerStore,
	raw string,
) *httptest.ResponseRecorder {
	t.Helper()

	handler := HandleResetSubmit(discardLogger(), nil, tokens, sessions, players)
	form := url.Values{}
	form.Set("token", raw)
	form.Set("password", "new-pass-12345")
	form.Set("confirm", "new-pass-12345")
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/reset-password", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// decodeSessionCookie replays the Set-Cookie from rec onto a fresh
// request and asks the manager to decode it, so the test verifies the
// cookie the handler minted is actually valid (not just present).
func decodeSessionCookie(
	t *testing.T, sessions *session.Manager, rec *httptest.ResponseRecorder,
) (playerID, sessionVersion int64, ok bool) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}

	return sessions.Decode(req)
}
