//go:build integration

package auth_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/session"
	"github.com/starquake/topbanana/internal/store"
)

func TestHandleVerifyEmail_MissingToken(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	rec := runVerifyEmail(t, db, "")
	if got, want := rec.Code, http.StatusBadRequest; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func TestHandleVerifyEmail_Success(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	stores := store.New(db, discardLogger())
	player := createVerifyPlayer(t, stores.Players, "alice", "alice@example.test", RolePlayer)
	if player.EmailVerifiedAt != nil {
		t.Fatalf("seed player should start unverified, EmailVerifiedAt = %v", player.EmailVerifiedAt)
	}
	raw := seedVerifyToken(t, stores.VerifyTokens, player.ID, time.Now().Add(time.Hour))

	rec := runVerifyEmail(t, db, raw)
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}

	verified, err := stores.Players.GetPlayerByID(t.Context(), player.ID)
	if err != nil {
		t.Fatalf("GetPlayerByID err = %v, want nil", err)
	}
	if verified.EmailVerifiedAt == nil {
		t.Error("EmailVerifiedAt is nil after successful verify, want stamped")
	}
}

func TestHandleVerifyEmail_AlreadyUsed(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	stores := store.New(db, discardLogger())
	player := createVerifyPlayer(t, stores.Players, "alice", "alice@example.test", RolePlayer)
	raw := seedVerifyToken(t, stores.VerifyTokens, player.ID, time.Now().Add(time.Hour))

	// Burn the token once so the second consume reports already-used.
	if _, err := stores.VerifyTokens.ConsumeVerifyToken(t.Context(), HashVerifyToken(raw)); err != nil {
		t.Fatalf("first ConsumeVerifyToken err = %v, want nil", err)
	}

	rec := runVerifyEmail(t, db, raw)
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func TestHandleVerifyEmail_Invalid(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	stores := store.New(db, discardLogger())
	player := createVerifyPlayer(t, stores.Players, "alice", "alice@example.test", RolePlayer)
	// An expired-but-unconsumed token classifies as invalid -> 410 Gone.
	raw := seedVerifyToken(t, stores.VerifyTokens, player.ID, time.Now().Add(-time.Hour))

	rec := runVerifyEmail(t, db, raw)
	if got, want := rec.Code, http.StatusGone; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

// TestHandleVerifyEmail_MismatchedSessionClears pins the shared-device
// fix: when the session player differs from the token owner, the
// success page must clear the session and render the neutral /
// landing rather than the session player's role landing (#472).
func TestHandleVerifyEmail_MismatchedSessionClears(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	stores := store.New(db, discardLogger())
	// First password-bearing registrant is promoted to admin, so seed the
	// session player first to give it the admin role the fix would otherwise
	// land on if the session were honoured.
	sessionPlayer := createVerifyPlayer(t, stores.Players, "session-user", "session@example.test", RoleAdmin)
	tokenOwner := createVerifyPlayer(t, stores.Players, "token-owner", "token-owner@example.test", RolePlayer)
	raw := seedVerifyToken(t, stores.VerifyTokens, tokenOwner.ID, time.Now().Add(time.Hour))
	sessions := session.New([]byte("test-key-32-bytes-test-key-32byt"), false)

	rec := httptest.NewRecorder()
	sessions.Set(rec, sessionPlayer.ID, sessionPlayer.SessionVersion)
	cookie := rec.Result().Cookies()[0]

	handler := HandleVerifyEmail(discardLogger(), nil, stores.VerifyTokens, stores.Players, sessions)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/verify-email?token="+raw, nil)
	req.AddCookie(cookie)
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, req)

	if got, want := out.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := out.Body.String(), `href="/"`; !strings.Contains(got, want) {
		t.Errorf("body should render neutral landing %q, got %q", want, got)
	}
	cleared := false
	for _, c := range out.Result().Cookies() {
		if c.Name == "topbanana_session" && c.MaxAge < 0 {
			cleared = true

			break
		}
	}
	if !cleared {
		t.Errorf("expected session cookie to be cleared; cookies = %v", out.Result().Cookies())
	}
}

// TestHandleVerifyEmail_MatchingSessionKeepsLanding pins the
// other half of the mismatch fix: when the session player matches the
// token owner, the Continue link goes to the role landing (admin in
// this case) and the session is preserved.
func TestHandleVerifyEmail_MatchingSessionKeepsLanding(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	stores := store.New(db, discardLogger())
	player := createVerifyPlayer(t, stores.Players, "match-user", "match@example.test", RoleAdmin)
	raw := seedVerifyToken(t, stores.VerifyTokens, player.ID, time.Now().Add(time.Hour))
	sessions := session.New([]byte("test-key-32-bytes-test-key-32byt"), false)

	rec := httptest.NewRecorder()
	sessions.Set(rec, player.ID, player.SessionVersion)
	cookie := rec.Result().Cookies()[0]

	handler := HandleVerifyEmail(discardLogger(), nil, stores.VerifyTokens, stores.Players, sessions)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/verify-email?token="+raw, nil)
	req.AddCookie(cookie)
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, req)

	if got, want := out.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := out.Body.String(), `href="/admin/quizzes"`; !strings.Contains(got, want) {
		t.Errorf("body should render admin landing %q, got %q", want, got)
	}
}

func runVerifyEmail(t *testing.T, db *sql.DB, raw string) *httptest.ResponseRecorder {
	t.Helper()

	stores := store.New(db, discardLogger())
	sessions := session.New([]byte("test-key-32-bytes-test-key-32byt"), true)
	handler := HandleVerifyEmail(discardLogger(), nil, stores.VerifyTokens, stores.Players, sessions)

	target := "/verify-email"
	if raw != "" {
		target += "?token=" + raw
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// createVerifyPlayer inserts a credentialled player through the real
// store and returns it. The verify-handler tests need a real row the
// token can reference and the session can point at.
func createVerifyPlayer(
	t *testing.T, players PlayerStore, displayName, email, role string,
) *Player {
	t.Helper()

	p, err := players.CreatePlayer(t.Context(), displayName, email, "hash", role)
	if err != nil {
		t.Fatalf("CreatePlayer %q err = %v, want nil", displayName, err)
	}

	return p
}

// seedVerifyToken mints a raw token, stores its hash for the given player
// with the supplied expiry through the real store, and returns the raw
// token so the caller can drive the handler with it. A past expiry yields
// the expired-but-unconsumed row the invalid-link branch reads.
func seedVerifyToken(t *testing.T, tokens VerifyTokenStore, playerID int64, expiresAt time.Time) string {
	t.Helper()

	raw, hash, err := GenerateVerifyToken()
	if err != nil {
		t.Fatalf("GenerateVerifyToken err = %v, want nil", err)
	}
	if err := tokens.CreateVerifyToken(t.Context(), hash, playerID, expiresAt, ""); err != nil {
		t.Fatalf("CreateVerifyToken err = %v, want nil", err)
	}

	return raw
}
