package auth_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/bgtasks"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/mailer"
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

	handler := HandleVerifyEmail(discardLogger(), nil, VerifyEmailDeps{
		Tokens:   stores.VerifyTokens,
		Players:  stores.Players,
		Roles:    stores.AdminPlayers,
		Sessions: sessions,
	})
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

	handler := HandleVerifyEmail(discardLogger(), nil, VerifyEmailDeps{
		Tokens:   stores.VerifyTokens,
		Players:  stores.Players,
		Roles:    stores.AdminPlayers,
		Sessions: sessions,
	})
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

// TestHandleVerifyEmail_AllowlistedEmail_PromotesToAdmin pins the
// verify-time half of #785: a registrant whose now-proven email is on
// the ADMIN_EMAILS allowlist is stamped admin only after consuming the
// verify token, not at registration.
func TestHandleVerifyEmail_AllowlistedEmail_PromotesToAdmin(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	stores := store.New(db, discardLogger())
	// Seed a credentialled player first so the store's first-registrant
	// admin rule does not auto-promote alice and mask the allowlist path.
	createVerifyPlayer(t, stores.Players, "first", "first@example.test", RolePlayer)
	player := createVerifyPlayer(t, stores.Players, "alice", "alice@example.test", RolePlayer)
	if got, want := player.Role, RolePlayer; got != want {
		t.Fatalf("seed player Role = %q, want %q (must start as plain player)", got, want)
	}
	raw := seedVerifyToken(t, stores.VerifyTokens, player.ID, time.Now().Add(time.Hour))

	rec := runVerifyEmailWithAdminEmails(t, db, raw, []string{"alice@example.test"})
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}

	promoted, err := stores.Players.GetPlayerByID(t.Context(), player.ID)
	if err != nil {
		t.Fatalf("GetPlayerByID err = %v, want nil", err)
	}
	if got, want := promoted.Role, RoleAdmin; got != want {
		t.Errorf("Role after verify = %q, want %q", got, want)
	}
}

// TestHandleVerifyEmail_NotAllowlisted_StaysPlayer pins the negative
// case for #785: a registrant absent from the allowlist keeps the
// player role after verifying.
func TestHandleVerifyEmail_NotAllowlisted_StaysPlayer(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	stores := store.New(db, discardLogger())
	// Seed a credentialled player first so bob is not the first-registrant
	// the store would auto-promote to admin.
	createVerifyPlayer(t, stores.Players, "first", "first@example.test", RolePlayer)
	player := createVerifyPlayer(t, stores.Players, "bob", "bob@example.test", RolePlayer)
	if got, want := player.Role, RolePlayer; got != want {
		t.Fatalf("seed player Role = %q, want %q (must start as plain player)", got, want)
	}
	raw := seedVerifyToken(t, stores.VerifyTokens, player.ID, time.Now().Add(time.Hour))

	rec := runVerifyEmailWithAdminEmails(t, db, raw, []string{"alice@example.test"})
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}

	after, err := stores.Players.GetPlayerByID(t.Context(), player.ID)
	if err != nil {
		t.Fatalf("GetPlayerByID err = %v, want nil", err)
	}
	if got, want := after.Role, RolePlayer; got != want {
		t.Errorf("Role after verify = %q, want %q", got, want)
	}
}

func runVerifyEmail(t *testing.T, db *sql.DB, raw string) *httptest.ResponseRecorder {
	t.Helper()

	return runVerifyEmailWithAdminEmails(t, db, raw, nil)
}

// runVerifyEmailWithAdminEmails drives the verify handler with the given
// ADMIN_EMAILS allowlist so the #785 promotion path can be exercised; a
// nil allowlist is the no-promotion default the other cases use.
func runVerifyEmailWithAdminEmails(
	t *testing.T, db *sql.DB, raw string, adminEmails []string,
) *httptest.ResponseRecorder {
	t.Helper()

	stores := store.New(db, discardLogger())
	sessions := session.New([]byte("test-key-32-bytes-test-key-32byt"), true)
	handler := HandleVerifyEmail(discardLogger(), nil, VerifyEmailDeps{
		Tokens:      stores.VerifyTokens,
		Players:     stores.Players,
		Roles:       stores.AdminPlayers,
		Sessions:    sessions,
		AdminEmails: adminEmails,
	})

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

// TestHandleVerifyEmail_ApprovalRequired_NotifiesUserAndAdmins pins the #1227
// awaiting-approval fan-out: a first-time verify under LOGIN_APPROVAL_REQUIRED
// notifies the registrant and every admin.
func TestHandleVerifyEmail_ApprovalRequired_NotifiesUserAndAdmins(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	stores := store.New(db, discardLogger())
	// The first credentialled registrant becomes the admin (with an email).
	createVerifyPlayer(t, stores.Players, "site-admin", "admin@example.test", RoleAdmin)
	target := createVerifyPlayer(t, stores.Players, "newbie", "newbie@example.test", RolePlayer)
	raw := seedVerifyToken(t, stores.VerifyTokens, target.ID, time.Now().Add(time.Hour))

	tester := mailer.NewTester(mailer.NewNoop())
	tracker := bgtasks.New()
	sessions := session.New([]byte("test-key-32-bytes-test-key-32byt"), true)
	handler := HandleVerifyEmail(discardLogger(), nil, VerifyEmailDeps{
		Tokens:                stores.VerifyTokens,
		Players:               stores.Players,
		Roles:                 stores.AdminPlayers,
		Sessions:              sessions,
		LoginApprovalRequired: true,
		Sender:                tester,
		AdminEmailLister:      stores.AdminEmailLister,
		BaseURL:               "https://tb.example",
		Tasks:                 tracker,
	})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/verify-email?token="+raw, nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if err := tracker.Wait(t.Context()); err != nil {
		t.Fatalf("tracker.Wait err = %v, want nil", err)
	}

	var toUser, toAdmin bool
	for _, e := range tester.Recent(10) {
		if e.Kind == mailer.KindApprovalPending && e.To == "newbie@example.test" {
			toUser = true
		}
		if e.Kind == mailer.KindApprovalRequest && e.To == "admin@example.test" {
			toAdmin = true
		}
	}
	if !toUser {
		t.Error("no approval-pending notice sent to the registrant")
	}
	if !toAdmin {
		t.Error("no approval-request notice sent to the admin")
	}
}

// TestHandleVerifyEmail_ApprovalOff_SendsNoApprovalMail pins that with the gate
// off, a verify sends none of the awaiting-approval notices.
func TestHandleVerifyEmail_ApprovalOff_SendsNoApprovalMail(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	stores := store.New(db, discardLogger())
	createVerifyPlayer(t, stores.Players, "site-admin", "admin@example.test", RoleAdmin)
	target := createVerifyPlayer(t, stores.Players, "newbie", "newbie@example.test", RolePlayer)
	raw := seedVerifyToken(t, stores.VerifyTokens, target.ID, time.Now().Add(time.Hour))

	tester := mailer.NewTester(mailer.NewNoop())
	tracker := bgtasks.New()
	sessions := session.New([]byte("test-key-32-bytes-test-key-32byt"), true)
	handler := HandleVerifyEmail(discardLogger(), nil, VerifyEmailDeps{
		Tokens:                stores.VerifyTokens,
		Players:               stores.Players,
		Roles:                 stores.AdminPlayers,
		Sessions:              sessions,
		LoginApprovalRequired: false,
		Sender:                tester,
		AdminEmailLister:      stores.AdminEmailLister,
		BaseURL:               "https://tb.example",
		Tasks:                 tracker,
	})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/verify-email?token="+raw, nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if err := tracker.Wait(t.Context()); err != nil {
		t.Fatalf("tracker.Wait err = %v, want nil", err)
	}

	if got := tester.Count(); got != 0 {
		t.Errorf("mailer send count = %d, want 0 when approval is off", got)
	}
}
