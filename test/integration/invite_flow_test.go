//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/session"
)

// invitePassword is the password every accept-invite test submits; it
// clears MinPasswordLength, so the only variable under test is the
// username and the token state.
const invitePassword = "invite-pass-12345"

// TestAdminInvite_CreatesPendingInvite drives the admin POST /admin/invites
// over HTTP and asserts a pending invite row lands for the target email.
// The integration server runs with an unconfigured (no-op) mailer, so the
// send is attempted and reports "not configured"; the invite row is
// committed regardless, which is what the handler promises.
func TestAdminInvite_CreatesPendingInvite(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{
		"REGISTRATION_ENABLED": "true",
		"BASE_URL":             "https://topbanana.example",
	})
	admin := registerAdminClient(ctx, t, srv.BaseURL, srv.DBURI, "invite-admin")

	dbConn, _ := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	token := fetchCSRFToken(ctx, t, admin, srv.BaseURL+"/admin/invites/new")
	status, _, body := postForm(ctx, t, admin, srv.BaseURL+"/admin/invites", url.Values{
		"csrf_token": {token},
		"email":      {"new-invitee@example.test"},
		"note":       {"a friend"},
	})
	if status != http.StatusOK {
		t.Fatalf("POST /admin/invites status = %d, want %d", status, http.StatusOK)
	}
	if got := string(body); !strings.Contains(got, "new-invitee@example.test") {
		t.Error("confirmation body missing invitee email")
	}

	if got, want := pendingInviteCount(ctx, t, dbConn, "new-invitee@example.test"), 1; got != want {
		t.Errorf("pending invite count = %d, want %d", got, want)
	}
}

// TestAdminInvite_RejectsExistingAccount pins that inviting an email that
// already has an account is refused with "sign in instead" and no invite
// row is written.
func TestAdminInvite_RejectsExistingAccount(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, map[string]string{"REGISTRATION_ENABLED": "true"})
	admin := registerAdminClient(ctx, t, srv.BaseURL, srv.DBURI, "invite-admin2")

	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	if _, err := stores.Players.CreatePlayer(
		ctx, "already", "already@example.test", "h", "player",
	); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}

	token := fetchCSRFToken(ctx, t, admin, srv.BaseURL+"/admin/invites/new")
	status, _, body := postForm(ctx, t, admin, srv.BaseURL+"/admin/invites", url.Values{
		"csrf_token": {token},
		"email":      {"already@example.test"},
	})
	if status != http.StatusConflict {
		t.Errorf("status = %d, want %d", status, http.StatusConflict)
	}
	if got, want := string(body), "sign in instead"; !strings.Contains(got, want) {
		t.Errorf("body missing %q", want)
	}
	if got, want := pendingInviteCount(ctx, t, dbConn, "already@example.test"), 0; got != want {
		t.Errorf("pending invite count = %d, want %d (must not write a row)", got, want)
	}
}

// TestAcceptInvite_HappyPath mints a live invite against the store, drives
// GET + POST /accept-invite over HTTP, and asserts the new player is
// created email-verified, the invite is marked accepted, the recipient is
// auto-logged-in, and they land on the player home page able to reach a
// gated page.
func TestAcceptInvite_HappyPath(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	// Consume the "first credentialled registrant becomes admin" promotion
	// with a seeded admin so the accepting player lands as a plain Player
	// (role landing "/"), not the admin dashboard.
	if _, err := stores.Players.CreatePlayer(
		ctx, "seed-admin", "seed-admin@example.test", "h", "admin",
	); err != nil {
		t.Fatalf("CreatePlayer seed admin err = %v, want nil", err)
	}

	raw := mintInvite(ctx, t, stores.Invites, "accept-happy@example.test", time.Now().Add(time.Hour))

	client := authClient(t)
	// GET renders the form for a live token.
	getResp := getWith(ctx, t, client, srv.BaseURL+"/accept-invite?"+url.Values{"token": {raw}}.Encode())
	getBody := readAllClose(t, getResp)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /accept-invite status = %d, want %d", getResp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(getBody, `name="token"`) {
		t.Error("accept form missing token field")
	}

	resp := postAcceptInvite(ctx, t, client, srv.BaseURL, raw, "Accepted Player")
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("POST /accept-invite status = %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Location"), "/"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if !hasSessionCookie(resp) {
		t.Errorf("accept response did not set a %q cookie; auto-login must mint a session", session.CookieName)
	}

	player, err := stores.Players.GetPlayerByEmail(ctx, "accept-happy@example.test")
	if err != nil {
		t.Fatalf("GetPlayerByEmail err = %v, want nil", err)
	}
	if got, want := player.Username, "Accepted Player"; got != want {
		t.Errorf("username = %q, want %q", got, want)
	}
	if !player.IsEmailVerified() {
		t.Error("new player must land email-verified")
	}

	// The invite is consumed: a second GET short-circuits to the invalid page.
	deadGet := getWith(ctx, t, authClient(t), srv.BaseURL+"/accept-invite?"+url.Values{"token": {raw}}.Encode())
	deadBody := readAllClose(t, deadGet)
	if got, want := deadGet.StatusCode, http.StatusGone; got != want {
		t.Errorf("post-accept GET status = %d, want %d", got, want)
	}
	if !strings.Contains(deadBody, "no longer valid") {
		t.Error("post-accept GET body missing invalid message")
	}

	// The auto-login cookie lets the new player reach a gated page.
	gated := getWith(ctx, t, client, srv.BaseURL+"/profile")
	defer gated.Body.Close() //nolint:errcheck // cleanup.
	if got, want := gated.StatusCode, http.StatusOK; got != want {
		t.Errorf("post-accept gated status = %d, want %d", got, want)
	}
}

// TestAcceptInvite_RejectsDeadTokens pins that an expired token and an
// already-accepted invite both render the 410 invalid page on GET and are
// refused on POST.
func TestAcceptInvite_RejectsDeadTokens(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	expired := mintInvite(ctx, t, stores.Invites, "exp@example.test", time.Now().Add(-time.Hour))
	getExpired := getWith(ctx, t, authClient(t), srv.BaseURL+"/accept-invite?"+url.Values{"token": {expired}}.Encode())
	defer getExpired.Body.Close() //nolint:errcheck // cleanup.
	if got, want := getExpired.StatusCode, http.StatusGone; got != want {
		t.Errorf("expired GET status = %d, want %d", got, want)
	}
	postExpired := postAcceptInvite(ctx, t, authClient(t), srv.BaseURL, expired, "X")
	defer postExpired.Body.Close() //nolint:errcheck // cleanup.
	if got, want := postExpired.StatusCode, http.StatusGone; got != want {
		t.Errorf("expired POST status = %d, want %d", got, want)
	}

	// An accepted invite: mint, consume, then attempt to use it again.
	accepted := mintInvite(ctx, t, stores.Invites, "acc@example.test", time.Now().Add(time.Hour))
	if err := stores.Invites.ConsumeInvite(ctx, auth.HashInviteToken(accepted)); err != nil {
		t.Fatalf("ConsumeInvite err = %v, want nil", err)
	}
	postAccepted := postAcceptInvite(ctx, t, authClient(t), srv.BaseURL, accepted, "Y")
	defer postAccepted.Body.Close() //nolint:errcheck // cleanup.
	if got, want := postAccepted.StatusCode, http.StatusGone; got != want {
		t.Errorf("accepted POST status = %d, want %d", got, want)
	}
}

// TestAcceptInvite_TakenUsernameKeepsInviteLive pins the ordering choice:
// a username collision fails the create and leaves the invite pending, so
// the recipient can retry on the same link with a different name.
func TestAcceptInvite_TakenUsernameKeepsInviteLive(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	dbConn, stores := openStores(t, srv.DBURI)
	defer dbConn.Close() //nolint:errcheck // cleanup.

	if _, err := stores.Players.CreatePlayer(
		ctx, "Taken Name", "taken@example.test", "h", "player",
	); err != nil {
		t.Fatalf("CreatePlayer err = %v, want nil", err)
	}
	raw := mintInvite(ctx, t, stores.Invites, "retry@example.test", time.Now().Add(time.Hour))

	client := authClient(t)
	resp := postAcceptInvite(ctx, t, client, srv.BaseURL, raw, "Taken Name")
	body := readAllClose(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("collision status = %d, want %d", resp.StatusCode, http.StatusConflict)
	}
	if !strings.Contains(body, "already taken") {
		t.Error("collision body missing already-taken message")
	}

	// The invite must still be live: a retry with a free name succeeds.
	if _, err := stores.Invites.GetLiveInvite(ctx, auth.HashInviteToken(raw)); err != nil {
		t.Errorf("invite must stay live after username collision: err = %v", err)
	}
	retry := postAcceptInvite(ctx, t, authClient(t), srv.BaseURL, raw, "Free Name")
	defer retry.Body.Close() //nolint:errcheck // cleanup.
	if got, want := retry.StatusCode, http.StatusSeeOther; got != want {
		t.Errorf("retry status = %d, want %d", got, want)
	}
}

// mintInvite creates a pending invite directly through the store and
// returns the raw token (the only place the raw value lives, since the
// integration server's no-op mailer drops the email body).
func mintInvite(
	ctx context.Context, t *testing.T, invites auth.InviteStore, email string, expiresAt time.Time,
) string {
	t.Helper()
	raw, hash, err := auth.GenerateInviteToken()
	if err != nil {
		t.Fatalf("GenerateInviteToken err = %v, want nil", err)
	}
	if cerr := invites.CreateInvite(ctx, email, hash, "", 0, expiresAt); cerr != nil {
		t.Fatalf("CreateInvite err = %v, want nil", cerr)
	}

	return raw
}

// postAcceptInvite issues POST /accept-invite with a freshly-fetched CSRF
// token on the supplied client. The token is fetched from /login so the
// helper works regardless of the accept-invite GET preflight
// short-circuiting on a dead token.
func postAcceptInvite(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	baseURL, rawToken, username string,
) *http.Response {
	t.Helper()
	csrfToken := fetchCSRFToken(ctx, t, client, baseURL+"/login")
	form := url.Values{
		"csrf_token": {csrfToken},
		"token":      {rawToken},
		"username":   {username},
		"password":   {invitePassword},
		"confirm":    {invitePassword},
	}
	req := newFormReq(ctx, t, baseURL+"/accept-invite", form)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /accept-invite err = %v, want nil", err)
	}

	return resp
}

// pendingInviteCount counts pending invites for email via a direct query.
// Test-only SQL: the production code has no list query until slice 2.
func pendingInviteCount(ctx context.Context, t *testing.T, dbConn *sql.DB, email string) int {
	t.Helper()
	var n int
	row := dbConn.QueryRowContext(
		ctx, "SELECT count(*) FROM invites WHERE email = ? AND status = 'pending'", email,
	)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("pendingInviteCount scan err = %v, want nil", err)
	}

	return n
}

// readAllClose reads and closes the response body, returning it as a
// string. Fails the test on a read error.
func readAllClose(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}

	return string(body)
}
