//go:build integration

// Package integration_test (this file) exercises the Google OAuth
// sign-in flow against a real server + DB. The Google endpoints are
// mocked with an httptest.Server pointed at via GOOGLE_ISSUER_URL;
// running the real Google flow in a Playwright e2e is out of scope
// because it requires a tenant + consent prompt that cannot be
// scripted from CI.
package integration_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testGoogleClientID     = "test-client-id"
	testGoogleClientSecret = "test-client-secret"
	testGoogleRedirectPath = "/login/google/callback"
	testGoogleSubject      = "google-sub-99"
	testGoogleKID          = "test-kid"
	// googleStateCookieName mirrors the constant in
	// internal/auth/google.go. Duplicated here because the integration
	// test runs in package integration_test and cannot reach the
	// in-package export_test alias. Test-pinned by the redirect-sets-
	// cookie assertion below; if the constant ever changes that
	// assertion fails loudly.
	googleStateCookieName = "tb_google_state"
)

// TestGoogleLogin_RedirectSetsStateCookie pins step 1 of the OAuth
// flow: GET /login/google issues a state cookie and redirects to
// Google's authorization endpoint.
func TestGoogleLogin_RedirectSetsStateCookie(t *testing.T) {
	t.Parallel()

	mock := newGoogleMock(t)
	ctx, srv := startGoogleServer(t, mock)

	client := authClient(t)
	resp := doGet(ctx, t, client, srv.BaseURL+"/login/google")

	if got, want := resp.StatusCode, http.StatusFound; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	if resp.Location == "" {
		t.Fatal("Location header is empty")
	}
	if got, want := resp.Location, mock.URL+"/o/oauth2/v2/auth"; !strings.HasPrefix(got, want) {
		t.Errorf("Location = %q, want prefix %q", got, want)
	}

	stateCookie := mustFindStateCookie(t, resp.Cookies)
	if stateCookie.Value == "" {
		t.Error("state cookie value is empty")
	}

	parsed, err := url.Parse(resp.Location)
	if err != nil {
		t.Fatalf("url.Parse err = %v, want nil", err)
	}
	if got, want := parsed.Query().Get("state"), stateCookie.Value; got != want {
		t.Errorf("Location state = %q, want %q (same as cookie)", got, want)
	}
}

// TestGoogleLogin_CallbackCreatesPlayer pins the new-account path: a
// callback with a fresh email creates a players row and signs the
// user in.
func TestGoogleLogin_CallbackCreatesPlayer(t *testing.T) {
	t.Parallel()

	mock := newGoogleMock(t)
	mock.email = "newcomer@example.test"
	mock.emailVerified = true

	ctx, srv := startGoogleServer(t, mock)

	client := authClient(t)
	finalResp := driveGoogleFlow(ctx, t, client, srv.BaseURL, mock)

	if got, want := finalResp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("callback status = %d, want %d (location=%q)", got, want, finalResp.Location)
	}
	// First password-less registrant goes to /admin/quizzes (the SQL
	// CASE in CreatePlayerFromOAuth promotes them to admin).
	if got, want := finalResp.Location, "/admin/quizzes"; got != want {
		t.Errorf("callback Location = %q, want %q", got, want)
	}

	requireDBRowCounts(t, srv.DBURI, mock.email, 1, 1)
}

// TestGoogleLogin_CallbackSecondUser_IsPlayer pins the credentialled
// player promotion rule: only the very first credentialled registrant
// becomes admin. Without this regression check, the OAuth-promotion
// SQL that counts password-bearing rows would promote every Google
// sign-in to admin on an OAuth-only deployment (no row ever has a
// password_hash, so the count stays 0 forever).
func TestGoogleLogin_CallbackSecondUser_IsPlayer(t *testing.T) {
	t.Parallel()

	mock := newGoogleMock(t)
	mock.email = "first@example.test"
	mock.subject = "google-sub-first"
	mock.emailVerified = true
	ctx, srv := startGoogleServer(t, mock)

	// First Google sign-in becomes admin via the bootstrap path. Use
	// a fresh client so the second flow does not inherit the session
	// cookie this one sets.
	first := driveGoogleFlow(ctx, t, authClient(t), srv.BaseURL, mock)
	if got, want := first.Location, "/admin/quizzes"; got != want {
		t.Fatalf("first Google sign-in Location = %q, want %q (bootstrap admin)", got, want)
	}

	// Re-point the mock at a different Google identity and drive a
	// fresh flow. Same mock + same signing key + same discovery URL,
	// so the verifier still trusts the id_token; only the subject and
	// email change, which is what a second Google account looks like
	// to the OAuth callback.
	mock.mu.Lock()
	mock.email = "second@example.test"
	mock.subject = "google-sub-second"
	mock.mu.Unlock()

	second := driveGoogleFlow(ctx, t, authClient(t), srv.BaseURL, mock)
	if got, want := second.Location, "/"; got != want {
		t.Errorf("second Google sign-in Location = %q, want %q (player, not admin)", got, want)
	}
}

// TestGoogleLogin_CallbackLinksExistingEmail pins the silent
// account-linking rule: a Google sign-in whose verified email matches
// an existing players row attaches the identity to it (no duplicate
// player created).
func TestGoogleLogin_CallbackLinksExistingEmail(t *testing.T) {
	t.Parallel()

	mock := newGoogleMock(t)
	mock.email = "existing@example.test"
	mock.emailVerified = true

	ctx, srv := startGoogleServer(t, mock)
	seedPlayerWithEmail(t, srv.DBURI, "existing-user", mock.email)

	client := authClient(t)
	finalResp := driveGoogleFlow(ctx, t, client, srv.BaseURL, mock)

	if got, want := finalResp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("callback status = %d, want %d (location=%q)", got, want, finalResp.Location)
	}

	// Exactly one players row for that email; one identity row linking
	// the Google subject to it.
	requireDBRowCounts(t, srv.DBURI, mock.email, 1, 1)
}

// TestGoogleLogin_CallbackRejectsUnverifiedEmail pins the security
// boundary on silent account linking: an unverified email is refused
// before any DB row is touched.
func TestGoogleLogin_CallbackRejectsUnverifiedEmail(t *testing.T) {
	t.Parallel()

	mock := newGoogleMock(t)
	mock.email = "unverified@example.test"
	mock.emailVerified = false

	ctx, srv := startGoogleServer(t, mock)

	client := authClient(t)
	finalResp := driveGoogleFlow(ctx, t, client, srv.BaseURL, mock)

	if got, want := finalResp.StatusCode, http.StatusUnauthorized; got != want {
		t.Fatalf("callback status = %d, want %d", got, want)
	}

	// No players row created for the unverified email.
	requireDBRowCounts(t, srv.DBURI, mock.email, 0, 0)
}

// TestGoogleLogin_CallbackRegistrationDisabled_RefusesNewAccount pins
// the registration gate on OAuth: with REGISTRATION_ENABLED off, a
// brand-new Google user (no existing identity, no session) is refused
// and no row is created.
func TestGoogleLogin_CallbackRegistrationDisabled_RefusesNewAccount(t *testing.T) {
	t.Parallel()

	mock := newGoogleMock(t)
	mock.email = "newcomer-disabled@example.test"
	mock.emailVerified = true

	ctx, srv := startGoogleServerEnv(t, mock, map[string]string{"REGISTRATION_ENABLED": "false"})

	client := authClient(t)
	finalResp := driveGoogleFlow(ctx, t, client, srv.BaseURL, mock)

	if got, want := finalResp.StatusCode, http.StatusUnauthorized; got != want {
		t.Fatalf("callback status = %d, want %d (location=%q)", got, want, finalResp.Location)
	}
	requireDBRowCounts(t, srv.DBURI, mock.email, 0, 0)
}

// TestGoogleLogin_CallbackRegistrationDisabled_ExistingIdentityLogsIn
// pins that registration off only gates the create-fresh branch: a
// Google sign-in for an already-linked identity still succeeds.
func TestGoogleLogin_CallbackRegistrationDisabled_ExistingIdentityLogsIn(t *testing.T) {
	t.Parallel()

	mock := newGoogleMock(t)
	mock.email = "existing-disabled@example.test"
	mock.emailVerified = true

	ctx, srv := startGoogleServerEnv(t, mock, map[string]string{"REGISTRATION_ENABLED": "false"})
	seedPlayerWithEmail(t, srv.DBURI, "existing-disabled", mock.email)

	client := authClient(t)
	finalResp := driveGoogleFlow(ctx, t, client, srv.BaseURL, mock)

	if got, want := finalResp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("callback status = %d, want %d (location=%q)", got, want, finalResp.Location)
	}
	// Linked onto the existing row by email; no duplicate created.
	requireDBRowCounts(t, srv.DBURI, mock.email, 1, 1)
}

// TestGoogleLogin_CallbackRejectsEmptySubject pins the defensive guard
// in exchangeAndVerify: a verified id_token with an empty `sub` claim
// is refused rather than linking every such sign-in to one empty
// identity key.
func TestGoogleLogin_CallbackRejectsEmptySubject(t *testing.T) {
	t.Parallel()

	mock := newGoogleMock(t)
	mock.email = "empty-subject@example.test"
	mock.emailVerified = true
	mock.subject = ""

	ctx, srv := startGoogleServer(t, mock)

	client := authClient(t)
	finalResp := driveGoogleFlow(ctx, t, client, srv.BaseURL, mock)

	if got, want := finalResp.StatusCode, http.StatusUnauthorized; got != want {
		t.Fatalf("callback status = %d, want %d", got, want)
	}
	requireDBRowCounts(t, srv.DBURI, mock.email, 0, 0)
}

// TestGoogleLogin_CallbackClaimsAnonymousSession pins the
// continuity-across-sign-in rule: a visitor who has been playing
// anonymously (session cookie pointing at an auto-petname row) and
// then signs in with Google for the first time keeps that same
// player_id and username. No new row is created; the existing row
// just gains the verified email and an identity link.
func TestGoogleLogin_CallbackClaimsAnonymousSession(t *testing.T) {
	t.Parallel()

	mock := newGoogleMock(t)
	mock.email = "anon-then-google@example.test"
	mock.emailVerified = true

	ctx, srv := startGoogleServer(t, mock)

	client := authClient(t)

	// Touch the public API once so EnsurePlayer creates an anonymous
	// players row and sets the session cookie on the client's jar.
	priming := doGet(ctx, t, client, srv.BaseURL+"/api/players/me")
	if got, want := priming.StatusCode, http.StatusOK; got != want {
		t.Fatalf("priming GET /api/players/me status = %d, want %d", got, want)
	}

	preID := lookupOnlyPlayerID(t, srv.DBURI)

	finalResp := driveGoogleFlow(ctx, t, client, srv.BaseURL, mock)
	if got, want := finalResp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("callback status = %d, want %d (location=%q)", got, want, finalResp.Location)
	}

	requireDBRowCounts(t, srv.DBURI, mock.email, 1, 1)

	postID := lookupPlayerIDByEmail(t, srv.DBURI, mock.email)
	if got, want := postID, preID; got != want {
		t.Errorf("player id after Google sign-in = %d, want %d (anonymous row reused, not replaced)", got, want)
	}
}

// TestGoogleLogin_CallbackRejectsStateMismatch pins the OAuth CSRF
// defence: a callback whose `state` query does not match the cookie
// is refused before any token exchange.
func TestGoogleLogin_CallbackRejectsStateMismatch(t *testing.T) {
	t.Parallel()

	mock := newGoogleMock(t)
	mock.email = "csrf@example.test"
	mock.emailVerified = true

	ctx, srv := startGoogleServer(t, mock)

	client := authClient(t)

	// Walk the initial redirect to get a real state cookie...
	startResp := doGet(ctx, t, client, srv.BaseURL+"/login/google")
	_ = mustFindStateCookie(t, startResp.Cookies)

	// ...then call the callback with the cookie present but a
	// different `state` query value.
	callbackURL := srv.BaseURL + testGoogleRedirectPath + "?code=some-code&state=tampered"
	resp := doGet(ctx, t, client, callbackURL)

	if got, want := resp.StatusCode, http.StatusUnauthorized; got != want {
		t.Errorf("callback status = %d, want %d", got, want)
	}
}

// driveGoogleFlow walks the GET /login/google -> mock auth ->
// callback dance and returns the snapshot of the final callback
// response. The body is drained + closed inside this helper so the
// caller does not have to manage it.
func driveGoogleFlow(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	baseURL string,
	mock *googleMock,
) responseSnapshot {
	t.Helper()

	start := doGet(ctx, t, client, baseURL+"/login/google")
	if got, want := start.StatusCode, http.StatusFound; got != want {
		t.Fatalf("initial GET /login/google status = %d, want %d", got, want)
	}

	parsed, err := url.Parse(start.Location)
	if err != nil {
		t.Fatalf("url.Parse err = %v, want nil", err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatal("state missing from authorization URL")
	}

	// Skip the (mock) consent screen and call the callback URL
	// directly with the code the mock will accept on exchange.
	mock.SetCode("test-auth-code")
	callbackURL := fmt.Sprintf("%s%s?code=%s&state=%s",
		baseURL, testGoogleRedirectPath, url.QueryEscape("test-auth-code"), url.QueryEscape(state))

	return doGet(ctx, t, client, callbackURL)
}

// responseSnapshot is the subset of an http.Response that the
// integration tests assert against. Used in place of *http.Response so
// the body can be drained + closed inside the helper, which keeps
// bodyclose lint happy at the call sites.
type responseSnapshot struct {
	StatusCode int
	Location   string
	Cookies    []*http.Cookie
}

// doGet sends a GET and returns a snapshot of the response's status,
// Location header, and Set-Cookies. The body is drained + closed
// before this helper returns.
func doGet(ctx context.Context, t *testing.T, client *http.Client, target string) responseSnapshot {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("Body.Close err = %v, want nil", cerr)
		}
	}()

	return responseSnapshot{
		StatusCode: resp.StatusCode,
		Location:   resp.Header.Get("Location"),
		Cookies:    resp.Cookies(),
	}
}

// requireDBRowCounts opens the test DB and asserts the players +
// player_identities row counts for the given email. Keeps each
// integration test honest about exactly what the OAuth flow wrote.
func requireDBRowCounts(t *testing.T, dbURI, email string, wantPlayers, wantIdentities int) {
	t.Helper()

	db, err := sql.Open("sqlite", dbURI)
	if err != nil {
		t.Fatalf("sql.Open err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})

	var playerRows int
	if err := db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM players WHERE email = ?`, email,
	).Scan(&playerRows); err != nil {
		t.Fatalf("count players err = %v, want nil", err)
	}
	if got, want := playerRows, wantPlayers; got != want {
		t.Errorf("players row count = %d, want %d", got, want)
	}

	var identityRows int
	if err := db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM player_identities pi JOIN players p ON p.id = pi.player_id WHERE p.email = ?`,
		email,
	).Scan(&identityRows); err != nil {
		t.Fatalf("count identities err = %v, want nil", err)
	}
	if got, want := identityRows, wantIdentities; got != want {
		t.Errorf("player_identities row count = %d, want %d", got, want)
	}
}

// lookupOnlyPlayerID returns the id of the single non-seeded players
// row, asserting that exactly one such row exists. The migration
// seeds an admin row (id=1) that the anonymous-priming flow does not
// touch; this helper filters it out so the test can refer to "the
// anonymous row" unambiguously.
func lookupOnlyPlayerID(t *testing.T, dbURI string) int64 {
	t.Helper()

	db, err := sql.Open("sqlite", dbURI)
	if err != nil {
		t.Fatalf("sql.Open err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})

	rows, err := db.QueryContext(t.Context(),
		`SELECT id FROM players WHERE id != 1 ORDER BY id`,
	)
	if err != nil {
		t.Fatalf("query players err = %v, want nil", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []int64
	for rows.Next() {
		var id int64
		if scanErr := rows.Scan(&id); scanErr != nil {
			t.Fatalf("scan player id err = %v, want nil", scanErr)
		}
		ids = append(ids, id)
	}
	if rerr := rows.Err(); rerr != nil {
		t.Fatalf("rows iteration err = %v, want nil", rerr)
	}
	if got, want := len(ids), 1; got != want {
		t.Fatalf("non-seeded players row count = %d, want %d (ids=%v)", got, want, ids)
	}

	return ids[0]
}

// lookupPlayerIDByEmail returns the id of the players row whose
// email matches. Fails the test when zero or more than one row is
// found so the caller's assertion stays unambiguous.
func lookupPlayerIDByEmail(t *testing.T, dbURI, email string) int64 {
	t.Helper()

	db, err := sql.Open("sqlite", dbURI)
	if err != nil {
		t.Fatalf("sql.Open err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})

	var id int64
	if scanErr := db.QueryRowContext(t.Context(),
		`SELECT id FROM players WHERE email = ?`, email,
	).Scan(&id); scanErr != nil {
		t.Fatalf("lookup player by email %q err = %v, want nil", email, scanErr)
	}

	return id
}

// seedPlayerWithEmail inserts a row with the given username + email
// directly through SQL so the linking test has an existing player to
// attach to without going through the register form.
func seedPlayerWithEmail(t *testing.T, dbURI, username, email string) {
	t.Helper()

	db, err := sql.Open("sqlite", dbURI)
	if err != nil {
		t.Fatalf("sql.Open err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})

	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO players (username, email, role, username_claimed) VALUES (?, ?, 'player', 1)`,
		username, email,
	); err != nil {
		t.Fatalf("seed insert err = %v, want nil", err)
	}
}

// startGoogleServer boots the app server with Google OAuth env vars
// pointed at the mock's discovery endpoint and registration enabled, so
// the create-fresh path is exercised by default. Tests that need
// registration off use startGoogleServerEnv with
// REGISTRATION_ENABLED=false.
func startGoogleServer(t *testing.T, mock *googleMock) (context.Context, testServer) {
	t.Helper()

	return startGoogleServerEnv(t, mock, map[string]string{"REGISTRATION_ENABLED": "true"})
}

// startGoogleServerEnv is startGoogleServer with caller-supplied extra
// env merged on top of the Google OAuth defaults.
func startGoogleServerEnv(t *testing.T, mock *googleMock, extra map[string]string) (context.Context, testServer) {
	t.Helper()

	// The mock's URL is also the redirect host as far as the mock
	// cares (it only validates client id / secret / code on token
	// exchange). The real server uses its own base URL for the
	// callback; we hard-code the path because the host varies with
	// the ephemeral listen port.
	env := map[string]string{
		"GOOGLE_CLIENT_ID":     testGoogleClientID,
		"GOOGLE_CLIENT_SECRET": testGoogleClientSecret,
		// The redirect URL the app sends to Google must match what
		// Google echoes back; the mock token endpoint does not check
		// redirect_uri so any well-formed value works for the test.
		"GOOGLE_REDIRECT_URL": "http://example.test" + testGoogleRedirectPath,
		// go-oidc fetches <issuer>/.well-known/openid-configuration
		// and validates the discovery doc's `issuer` field equals
		// this URL.
		"GOOGLE_ISSUER_URL": mock.URL,
	}
	maps.Copy(env, extra)

	return startServer(t, env)
}

// googleMock is an httptest.Server speaking the slice of OIDC that
// HandleGoogleCallback consumes: discovery, JWKS, authorization
// endpoint (we don't drive it but the URL is published), and token
// endpoint. Holds an RSA key whose JWKS is served verbatim so the
// id_token signature verifies end-to-end.
type googleMock struct {
	URL string

	key *rsa.PrivateKey

	mu            sync.Mutex
	code          string
	email         string
	emailVerified bool
	// subject is the Google-side stable user id minted into the
	// id_token's `sub` claim. Defaults to testGoogleSubject in
	// newGoogleMock so single-user tests can leave it alone; multi-
	// user tests (e.g. the second-user-is-not-admin regression
	// check) flip it between flows to simulate distinct Google
	// accounts.
	subject string
}

func newGoogleMock(t *testing.T) *googleMock {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey err = %v, want nil", err)
	}

	m := &googleMock{key: key, subject: testGoogleSubject}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", m.handleDiscovery)
	mux.HandleFunc("/jwks", m.handleJWKS)
	mux.HandleFunc("/o/oauth2/v2/auth", m.handleAuth)
	mux.HandleFunc("/token", m.handleToken)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	m.URL = srv.URL

	return m
}

func (m *googleMock) SetCode(code string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.code = code
}

// mockDiscoveryDoc mirrors the subset of the OIDC discovery document
// the mock returns. Spec-defined JSON keys are snake_case; the
// directive below suppresses tagliatelle for that reason.
//
//nolint:tagliatelle // OIDC discovery spec defines snake_case keys.
type mockDiscoveryDoc struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// mockJWKS / mockJWK match what the auth package's verifier expects.
type mockJWKS struct {
	Keys []mockJWK `json:"keys"`
}

type mockJWK struct {
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// mockTokenResponse is the JSON body the mock returns from /token.
//
//nolint:tagliatelle // OAuth 2.0 token response fields are spec-defined snake_case.
type mockTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	IDToken     string `json:"id_token"`
}

// mockIDTokenClaims is the payload of the id_token the mock signs.
//
//nolint:tagliatelle // OIDC id_token claims are spec-defined snake_case.
type mockIDTokenClaims struct {
	Iss           string `json:"iss"`
	Aud           string `json:"aud"`
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Iat           int64  `json:"iat"`
	Exp           int64  `json:"exp"`
}

// mockJWTHeader is the header of the id_token the mock signs.
type mockJWTHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

func (m *googleMock) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	doc := mockDiscoveryDoc{
		Issuer:                m.URL,
		AuthorizationEndpoint: m.URL + "/o/oauth2/v2/auth",
		TokenEndpoint:         m.URL + "/token",
		JWKSURI:               m.URL + "/jwks",
	}
	writeJSON(w, doc)
}

func (m *googleMock) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	pub, ok := m.key.Public().(*rsa.PublicKey)
	if !ok {
		http.Error(w, "unexpected key type", http.StatusInternalServerError)

		return
	}
	jwk := mockJWKS{Keys: []mockJWK{{
		Kty: "RSA",
		Alg: "RS256",
		Use: "sig",
		Kid: testGoogleKID,
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(bigIntBytesForExponent(pub.E)),
	}}}
	writeJSON(w, jwk)
}

// handleAuth answers the (unused) authorization endpoint with 200.
// The mock does not implement the consent screen; tests drive the
// callback directly.
func (*googleMock) handleAuth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (m *googleMock) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	m.mu.Lock()
	wantCode := m.code
	email := m.email
	emailVerified := m.emailVerified
	subject := m.subject
	m.mu.Unlock()

	if got, want := r.FormValue("code"), wantCode; got != want {
		http.Error(w, "bad code", http.StatusBadRequest)

		return
	}
	if got, want := r.FormValue("client_id"), testGoogleClientID; got != want {
		http.Error(w, "bad client_id", http.StatusUnauthorized)

		return
	}
	if got, want := r.FormValue("client_secret"), testGoogleClientSecret; got != want {
		http.Error(w, "bad client_secret", http.StatusUnauthorized)

		return
	}

	resp := mockTokenResponse{
		AccessToken: "mock-access-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		IDToken:     m.signIDToken(subject, email, emailVerified),
	}
	writeJSON(w, resp)
}

// signIDToken mints an RS256 id_token whose iss / aud / sub / email /
// email_verified claims match what the callback expects. The kid in
// the header matches the JWKS entry so VerifyIDToken finds the key.
func (m *googleMock) signIDToken(subject, email string, emailVerified bool) string {
	header := mockJWTHeader{Alg: "RS256", Typ: "JWT", Kid: testGoogleKID}
	now := time.Now().Unix()
	payload := mockIDTokenClaims{
		Iss:           m.URL,
		Aud:           testGoogleClientID,
		Sub:           subject,
		Email:         email,
		EmailVerified: emailVerified,
		Iat:           now,
		Exp:           now + 3600,
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		panic(fmt.Sprintf("marshal header: %v", err))
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("marshal payload: %v", err))
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(payloadJSON)
	hashed := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, m.key, crypto.SHA256, hashed[:])
	if err != nil {
		panic(fmt.Sprintf("sign id_token: %v", err))
	}

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// writeJSON encodes v as the response body. Encode failures are
// only possible from invalid types; the typed structs above all
// serialise cleanly so a failure here is a test-author bug.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		panic(fmt.Sprintf("encode response: %v", err))
	}
}

// mustFindStateCookie returns the Google state cookie from a Cookies
// slice or fatals the test. Folded out of the per-test loops so the
// downstream dereference of stateCookie.Value has an obviously
// non-nil pointer at the assignment site — the loop-then-nil-check
// pattern can confuse some staticcheck builds even though t.Fatal
// halts the goroutine.
func mustFindStateCookie(t *testing.T, cookies []*http.Cookie) *http.Cookie {
	t.Helper()

	for _, c := range cookies {
		if c.Name == googleStateCookieName {
			return c
		}
	}
	t.Fatal("state cookie missing from response")

	return nil
}

// bigIntBytesForExponent returns the big-endian byte representation
// of an RSA public exponent. The standard 65537 fits in three bytes;
// this helper exists so the mock matches the JWK encoding the
// stdlib-based verifier expects. The exponent is bounded to
// math.MaxInt32 by RSA convention, so the int -> uint64 conversion
// is safe.
func bigIntBytesForExponent(e int) []byte {
	const uint64Len = 8
	buf := make([]byte, uint64Len)
	binary.BigEndian.PutUint64(buf, uint64(e))
	for i, b := range buf {
		if b != 0 {
			return buf[i:]
		}
	}

	return []byte{0}
}
