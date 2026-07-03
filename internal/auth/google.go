package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/render"
	"github.com/starquake/topbanana/internal/session"
)

// googleStateCookieName is the short-lived cookie that pins the OAuth
// `state` parameter. Distinct from the form CSRF cookie because OAuth
// state has different semantics: it round-trips through Google and is
// only validated on the callback, whereas the form CSRF token is
// validated on every unsafe POST.
const googleStateCookieName = "tb_google_state"

// googleStateMaxAge bounds how long a freshly issued state cookie
// stays valid. The user has to click "Sign in", complete Google's
// consent screen, and return - usually well under a minute. Ten
// minutes gives slow flows headroom without leaving the cookie usable
// indefinitely if a user wanders off.
const googleStateMaxAge = 10 * 60

// googleStateNonceLength is the length of the random nonce embedded
// in the state value. 24 raw bytes (32 base64url chars) is comfortably
// above the 128-bit collision threshold without bloating the cookie.
const googleStateNonceLength = 24

// googleStateDerivationLabel is mixed into the SESSION_KEY to derive
// the HMAC key used to sign the state cookie. Versioned so we can
// rotate without breaking outstanding cookies.
const googleStateDerivationLabel = "google-state-v1"

// googleDefaultIssuer is the production OIDC issuer URL. go-oidc
// fetches <issuer>/.well-known/openid-configuration to bootstrap; the
// integration test overrides this with an httptest.Server URL.
const googleDefaultIssuer = "https://accounts.google.com"

// ErrGoogleStateMismatch is returned when the state value submitted to
// the callback does not match the cookie. Either CSRF, a stale cookie,
// or a tampered redirect.
var ErrGoogleStateMismatch = errors.New("google state mismatch")

// ErrRegistrationDisabled is returned by linkOrCreateGooglePlayer when
// a verified Google sign-in resolves to no existing account and no
// claimable session row, but REGISTRATION_ENABLED is off. Only the
// create-fresh branch is gated; existing-identity, link-by-email, and
// claim-session sign-ins still succeed.
var ErrRegistrationDisabled = errors.New("registration disabled")

// GoogleConfig groups the runtime knobs needed by HandleGoogleLogin
// and HandleGoogleCallback. Lets the route wiring stay readable
// instead of threading half-a-dozen parameters through each handler.
type GoogleConfig struct {
	ClientID      string
	ClientSecret  string
	RedirectURL   string
	IssuerURL     string
	SecureCookies bool
}

// GoogleAuthenticator bundles the OIDC provider + state-cookie key
// once at startup so each request reuses the same cached discovery
// document and signing key. Safe for concurrent use.
type GoogleAuthenticator struct {
	cfg      GoogleConfig
	stateKey []byte

	mu       sync.Mutex
	ready    bool
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth2   *oauth2.Config
}

// NewGoogleAuthenticator returns an authenticator ready to serve the
// /login/google + callback routes. sessionKey is reused (via HMAC
// derivation) to sign the state cookie so the deployment does not
// need a second secret.
func NewGoogleAuthenticator(cfg GoogleConfig, sessionKey []byte) *GoogleAuthenticator {
	h := hmac.New(sha256.New, sessionKey)
	_, _ = h.Write([]byte(googleStateDerivationLabel))

	return &GoogleAuthenticator{
		cfg:      cfg,
		stateKey: h.Sum(nil),
	}
}

// HandleGoogleLogin renders the initial redirect to Google's consent
// screen. It mints a random state value, signs it into a short-lived
// cookie, and redirects the browser to the authorization URL with the
// same value in the `state` query parameter.
func HandleGoogleLogin(logger *slog.Logger, authn *GoogleAuthenticator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := authn.ensureProvider(r.Context()); err != nil {
			logger.ErrorContext(r.Context(), "error initialising oidc provider", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		nonce, err := newStateNonce()
		if err != nil {
			logger.ErrorContext(r.Context(), "error generating state nonce", slog.Any("err", err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		state := signState(authn.stateKey, nonce)
		http.SetCookie(w, googleStateCookie(state, authn.cfg.SecureCookies, googleStateMaxAge))
		// Always overwrite the next cookie - either with the validated
		// new value or with a clear - so a stale cookie from an
		// abandoned earlier flow cannot leak its destination into this
		// login.
		if next := SafeNextPath(r.URL.Query().Get("next")); next != "" {
			http.SetCookie(w, googleNextCookie(
				signNext(authn.stateKey, next), authn.cfg.SecureCookies, googleStateMaxAge))
		} else {
			http.SetCookie(w, googleNextCookie("", authn.cfg.SecureCookies, -1))
		}

		http.Redirect(w, r, authn.oauth2.AuthCodeURL(state, oauth2.AccessTypeOnline), http.StatusFound)
	})
}

// HandleGoogleCallback handles the redirect back from Google. It
// validates the state cookie, exchanges the code for tokens, verifies
// the id_token, finds-or-links a player, signs them in, and redirects
// to the role-appropriate landing page.
//
// On any user-facing failure the handler re-renders the login template
// with a short error message instead of 500-ing, so the player sees a
// recoverable form rather than a stack trace.
//
// The find-or-link decision lives in linkOrCreateGooglePlayer; this
// handler is just the request-shaped wrapper around it.
//
// adminEmails is the ADMIN_EMAILS allowlist; once the callback has a
// verified, persisted player it promotes that player to admin when the
// now-proven address matches an entry (#824), mirroring the verify-token
// path. Google attests the address on every callback (the unverified
// branch is already refused), so this upholds the "admin only on a
// verified address" invariant. roles is the narrow setter used for that
// promotion.
//
//nolint:revive // argument-limit: the callback genuinely needs every collaborator threaded in; the success tail is already split into finalizeGoogleSignIn (with its deps bundled), so the remaining list is the irreducible request-shaped surface.
func HandleGoogleCallback(
	logger *slog.Logger,
	authn *GoogleAuthenticator,
	csrfMgr *csrf.Manager,
	identities OAuthIdentityStore,
	players PlayerStore,
	roles RoleSetter,
	sessions *session.Manager,
	games AnonymousGameMigrator,
	adminEmails []string,
	registrationEnabled, forgotPasswordEnabled bool,
) http.Handler {
	renderer := newTemplateRenderer(logger, csrfMgr, "auth/pages/login.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the next cookie BEFORE clearing the state pair so a
		// successful callback can use it; the clears below run
		// unconditionally so a single callback URL cannot be replayed
		// even if the rest of the handler returns early.
		next := readGoogleNext(r, authn.stateKey)
		http.SetCookie(w, googleStateCookie("", authn.cfg.SecureCookies, -1))
		http.SetCookie(w, googleNextCookie("", authn.cfg.SecureCookies, -1))

		if msg, ok := validateCallbackRequest(authn.stateKey, r); !ok {
			renderGoogleError(renderer, w, r, msg, registrationEnabled, forgotPasswordEnabled)

			return
		}

		result := authn.exchangeAndVerify(r, logger)
		if result.Fatal {
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}
		if result.UserMessage != "" {
			renderGoogleError(renderer, w, r, result.UserMessage, registrationEnabled, forgotPasswordEnabled)

			return
		}

		var sessionPlayerID *int64
		if id, ok := sessions.PlayerID(r); ok {
			sessionPlayerID = &id
		}
		player, err := linkOrCreateGooglePlayer(
			r.Context(), identities, result.Subject, result.Email, sessionPlayerID, registrationEnabled,
		)
		if err != nil {
			if !errors.Is(err, ErrRegistrationDisabled) {
				logger.ErrorContext(r.Context(), "error linking google player", slog.Any("err", err))
			}
			renderGoogleError(renderer, w, r, googleLinkErrorMessage(err), registrationEnabled, forgotPasswordEnabled)

			return
		}

		// Defence-in-depth mirror of HandleLoginSubmit's verify gate
		// (#492). createGooglePlayer / claimAnonymousSessionPlayer /
		// linkExistingPlayerByEmail all stamp email_verified_at because
		// Google attests the address on every callback, so this branch
		// should not fire in production - it exists so a future OAuth
		// store regression cannot silently mint a session for a row
		// whose email_verified_at column is still NULL.
		if !player.IsEmailVerified() {
			logger.WarnContext(r.Context(), "google sign-in blocked: email_verified_at not stamped",
				slog.Int64("player_id", player.ID))
			renderGoogleError(renderer, w, r,
				"Sign-in blocked: your email is not verified. Try requesting a verification link.",
				registrationEnabled, forgotPasswordEnabled)

			return
		}

		finalizeGoogleSignIn(w, r, googleSignInDeps{
			logger:      logger,
			players:     players,
			roles:       roles,
			sessions:    sessions,
			games:       games,
			adminEmails: adminEmails,
		}, player, sessionPlayerID, next)
	})
}

// googleSignInDeps groups the collaborators finalizeGoogleSignIn needs.
// Bundling them keeps the helper under revive's argument-count cap
// without flattening the call site into a long positional list, the same
// packaging the verify handler uses for verifyOutcome.
type googleSignInDeps struct {
	logger      *slog.Logger
	players     PlayerStore
	roles       RoleSetter
	sessions    *session.Manager
	games       AnonymousGameMigrator
	adminEmails []string
}

// finalizeGoogleSignIn runs the success tail of the callback once a
// verified, persisted player is in hand: best-effort admin promotion on
// the now-proven address, the session cookie, anonymous-game migration,
// and the redirect to the validated next path or the role landing. Split
// out of HandleGoogleCallback so the constructor stays under revive's
// function-length cap.
//
// Promotion mirrors the verify-token path (#824) and is idempotent: the
// helper skips when the row is already admin or the email is not on the
// allowlist, and logs+swallows any failure, so a promotion error never
// blocks the login.
func finalizeGoogleSignIn(
	w http.ResponseWriter,
	r *http.Request,
	deps googleSignInDeps,
	player *Player,
	sessionPlayerID *int64,
	next string,
) {
	promoteVerifiedAdminIfAllowlisted(
		r.Context(), deps.logger, deps.players, deps.roles, deps.adminEmails, player.ID,
	)

	deps.sessions.Set(w, player.ID, player.SessionVersion)
	deps.logger.InfoContext(r.Context(), "google sign-in succeeded",
		slog.Int64(logPlayerKey, player.ID),
		slog.String(logEmailKey, player.Email))
	migrateGamesAfterSignIn(r.Context(), deps.logger, deps.players, deps.games, sessionPlayerID, player.ID)
	target := next
	if target == "" {
		// Resolve the landing from the freshly persisted role: the promotion
		// above may have just changed it, and `player` was read before that.
		// Mirrors the verify-token path's postVerifyLanding so a just-promoted
		// admin reaches /admin instead of the player home. A read failure
		// falls back to the pre-promotion role.
		role := player.Role
		if fresh, err := deps.players.GetPlayerByID(r.Context(), player.ID); err == nil {
			role = fresh.Role
		}
		target = landingPathFor(role)
	}
	//nolint:gosec // G710: target is either landingPathFor (constant) or a SafeNextPath-validated relative path returned by readGoogleNext.
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// validateCallbackRequest walks the cheap up-front checks on a
// callback: state validation, Google-reported error, and missing
// code. Returns ("", true) when the request passes; otherwise returns
// a user-facing message and false. Splitting this out keeps
// HandleGoogleCallback under the function-length linter limit and
// makes the early-exit paths trivially testable.
func validateCallbackRequest(stateKey []byte, r *http.Request) (string, bool) {
	if validateState(stateKey, r) != nil {
		return "Sign-in expired. Please try again.", false
	}

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		// Google reports user-side failures (consent declined,
		// account chooser closed) by redirecting with ?error=...
		// instead of an error code; keep the message generic.
		return "Google sign-in was cancelled.", false
	}

	if r.URL.Query().Get("code") == "" {
		return "Google sign-in failed. Please try again.", false
	}

	return "", true
}

// callbackResult holds the outcome of exchangeAndVerify. Exactly one
// of Fatal=true, UserMessage!="", or (Subject!="" && Email!="") is
// populated on return; the callback handler branches on those three
// states.
type callbackResult struct {
	Subject     string
	Email       string
	UserMessage string
	Fatal       bool
}

// googleClaims is the slice of the id_token payload the handler reads.
// The struct's JSON keys are OIDC-spec snake_case; the nolint
// directive overrides the project-wide camelCase rule for that
// reason.
//
//nolint:tagliatelle // OIDC id_token claims are spec-defined snake_case.
type googleClaims struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}

// exchangeAndVerify performs the token exchange + id_token
// verification half of the callback. The three populated states on
// the returned [callbackResult] map to the three outcomes: success
// (Subject + Email populated); 500-worthy internal failure
// (Fatal=true); user-facing failure that re-renders the login form
// (UserMessage populated). email_verified=false is mapped to a
// UserMessage so the caller never reaches the silent-link path with
// an unverified address.
func (a *GoogleAuthenticator) exchangeAndVerify(r *http.Request, logger *slog.Logger) callbackResult {
	if err := a.ensureProvider(r.Context()); err != nil {
		logger.ErrorContext(r.Context(), "error initialising oidc provider", slog.Any("err", err))

		return callbackResult{Fatal: true}
	}

	token, err := a.oauth2.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		logger.ErrorContext(r.Context(), "error exchanging oauth code", slog.Any("err", err))

		return callbackResult{UserMessage: "Google sign-in failed. Please try again."}
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		logger.ErrorContext(r.Context(), "missing id_token from google token response")

		return callbackResult{UserMessage: "Google sign-in failed. Please try again."}
	}

	idToken, err := a.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		logger.ErrorContext(r.Context(), "id token verification failed", slog.Any("err", err))

		return callbackResult{UserMessage: "Could not verify your Google sign-in."}
	}

	var claims googleClaims
	if cErr := idToken.Claims(&claims); cErr != nil {
		logger.ErrorContext(r.Context(), "id token claim parse failed", slog.Any("err", cErr))

		return callbackResult{UserMessage: "Could not verify your Google sign-in."}
	}

	// email_verified is the security boundary for the silent
	// account-linking path. Without it, anyone who can take over an
	// unverified Google address could attach themselves to an
	// existing Top Banana account with the same address.
	if !claims.EmailVerified {
		return callbackResult{
			UserMessage: "Your Google account email is not verified. Verify it with Google and try again.",
		}
	}

	// A verified id_token without a subject should never happen, but an
	// empty subject would link every such sign-in to the same identity
	// row; refuse rather than proceed with the spoofable empty key.
	if idToken.Subject == "" {
		logger.ErrorContext(r.Context(), "id token has empty subject")

		return callbackResult{UserMessage: "Could not verify your Google sign-in."}
	}

	return callbackResult{Subject: idToken.Subject, Email: claims.Email}
}

// linkOrCreateGooglePlayer resolves a verified Google sign-in to a
// player: existing identity > link-by-email > claim-session-row >
// create-fresh. The silent link-by-email branch is only safe because
// the caller has already verified email_verified=true on the id-token.
//
//nolint:revive // registrationEnabled gates only the final create-fresh branch (#492-adjacent); threading the policy in is clearer than splitting the branch table across two functions.
func linkOrCreateGooglePlayer(
	ctx context.Context,
	identities OAuthIdentityStore,
	subject, email string,
	sessionPlayerID *int64,
	registrationEnabled bool,
) (*Player, error) {
	existing, err := identities.GetPlayerByProviderSubject(ctx, ProviderGoogle, subject)
	if err == nil {
		// Self-heal a partial commit from an earlier link path: if the
		// row was linked but a transient failure prevented the
		// email_verified_at stamp, every subsequent login otherwise
		// short-circuits here and the player is stranded on the
		// verify-email gate. Google attests the address on every
		// callback that reaches us, so stamping here is safe and
		// idempotent. See #471.
		if email != "" && existing.EmailVerifiedAt == nil {
			if markErr := identities.MarkPlayerEmailVerifiedIfNew(ctx, existing.ID); markErr != nil {
				return nil, fmt.Errorf("mark email verified on existing identity: %w", markErr)
			}
			now := time.Now().UTC()
			existing.EmailVerifiedAt = &now
		}

		return existing, nil
	}
	if !errors.Is(err, ErrPlayerNotFound) {
		return nil, fmt.Errorf("get player by google subject: %w", err)
	}

	if email != "" {
		linked, linkErr := linkExistingPlayerByEmail(ctx, identities, subject, email)
		if linkErr == nil {
			return linked, nil
		}
		if !errors.Is(linkErr, ErrPlayerNotFound) {
			return nil, linkErr
		}
	}

	if sessionPlayerID != nil {
		claimed, claimErr := claimAnonymousSessionPlayer(ctx, identities, *sessionPlayerID, subject, email)
		if claimErr == nil {
			return claimed, nil
		}
		if !errors.Is(claimErr, ErrPlayerNotFound) {
			return nil, claimErr
		}
	}

	if !registrationEnabled {
		return nil, ErrRegistrationDisabled
	}

	return createGooglePlayer(ctx, identities, subject, email)
}

// claimAnonymousSessionPlayer upgrades the row identified by
// sessionPlayerID in place - attaching the OAuth-verified email and
// linking the (provider, subject) identity onto it - so the visitor's
// existing player_id carries forward. Returns ErrPlayerNotFound when
// the row is missing, already credentialled, or already carries an
// email; the caller falls through to createGooglePlayer on that
// sentinel.
func claimAnonymousSessionPlayer(
	ctx context.Context,
	identities OAuthIdentityStore,
	sessionPlayerID int64,
	subject, email string,
) (*Player, error) {
	claimed, err := identities.ClaimPlayerForOAuth(ctx, sessionPlayerID, email)
	if err != nil {
		if errors.Is(err, ErrPlayerNotFound) {
			// The session row is no longer claimable. Before reporting
			// "fall through to create", check whether a concurrent
			// callback for the same (provider, subject) already linked
			// the identity onto another row. The window opens when two
			// OAuth callbacks for the same anonymous session race and
			// the loser arrives here AFTER the winner finished claiming
			// + linking; without this re-read the loser would create a
			// duplicate row and then fail at LinkProviderIdentity with
			// ErrIdentityAlreadyLinked. Mirrors the recovery branch in
			// linkExistingPlayerByEmail.
			if existing, lookupErr := identities.GetPlayerByProviderSubject(
				ctx, ProviderGoogle, subject,
			); lookupErr == nil {
				return existing, nil
			} else if !errors.Is(lookupErr, ErrPlayerNotFound) {
				return nil, fmt.Errorf("lookup after claim race: %w", lookupErr)
			}

			return nil, ErrPlayerNotFound
		}

		return nil, fmt.Errorf("claim anonymous player for oauth: %w", err)
	}

	if linkErr := identities.LinkProviderIdentity(ctx, claimed.ID, ProviderGoogle, subject); linkErr != nil {
		if errors.Is(linkErr, ErrIdentityAlreadyLinked) {
			// Lost a race with a concurrent callback that already
			// linked this (provider, subject) onto a different row.
			// Re-read by subject and return that row instead so the
			// session ends up pointing at the canonical OAuth-linked
			// player.
			refetched, refetchErr := identities.GetPlayerByProviderSubject(ctx, ProviderGoogle, subject)
			if refetchErr != nil {
				return nil, fmt.Errorf("refetch after link race: %w", refetchErr)
			}

			return refetched, nil
		}

		return nil, fmt.Errorf("link identity to claimed anonymous player: %w", linkErr)
	}

	return claimed, nil
}

// linkExistingPlayerByEmail looks up a player by verified email and,
// if one is found, links the supplied (provider, subject) onto it.
// Returns ErrPlayerNotFound when no row matches the email; the
// caller treats that sentinel as "no email match, create instead".
func linkExistingPlayerByEmail(
	ctx context.Context,
	identities OAuthIdentityStore,
	subject, email string,
) (*Player, error) {
	player, err := identities.GetPlayerByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrPlayerNotFound) {
			return nil, ErrPlayerNotFound
		}

		return nil, fmt.Errorf("get player by email: %w", err)
	}

	if linkErr := identities.LinkProviderIdentity(ctx, player.ID, ProviderGoogle, subject); linkErr != nil {
		if errors.Is(linkErr, ErrIdentityAlreadyLinked) {
			// Lost a race with a concurrent callback for the same
			// (provider, subject). Re-read by subject and return that
			// row.
			refetched, refetchErr := identities.GetPlayerByProviderSubject(ctx, ProviderGoogle, subject)
			if refetchErr != nil {
				return nil, fmt.Errorf("refetch after link race: %w", refetchErr)
			}

			return refetched, nil
		}

		return nil, fmt.Errorf("link identity to existing player: %w", linkErr)
	}

	// Google attests the address; stamp email_verified_at if not already set.
	if err := identities.MarkPlayerEmailVerifiedIfNew(ctx, player.ID); err != nil {
		return nil, fmt.Errorf("mark email verified after link: %w", err)
	}
	if player.EmailVerifiedAt == nil {
		now := time.Now().UTC()
		player.EmailVerifiedAt = &now
	}

	return player, nil
}

// createGooglePlayer creates a fresh players row + linked identity.
// Retries a handful of petname collisions before giving up; the pool
// is large enough that a real production deployment should never run
// out, but a tight test loop could hit the same petname twice in a
// row.
func createGooglePlayer(
	ctx context.Context,
	identities OAuthIdentityStore,
	subject, email string,
) (*Player, error) {
	const maxPetnameAttempts = 5
	var lastErr error
	for range maxPetnameAttempts {
		displayName := GeneratePetname()
		player, err := identities.CreatePlayerFromOAuth(ctx, displayName, email)
		if err != nil {
			if errors.Is(err, ErrDisplayNameTaken) {
				lastErr = err

				continue
			}

			return nil, fmt.Errorf("create player from oauth: %w", err)
		}
		if linkErr := identities.LinkProviderIdentity(ctx, player.ID, ProviderGoogle, subject); linkErr != nil {
			if errors.Is(linkErr, ErrIdentityAlreadyLinked) {
				// Symmetric race recovery to claimAnonymousSessionPlayer
				// and linkExistingPlayerByEmail: a concurrent callback
				// for the same (provider, subject) linked the identity
				// onto a different row between our identity-miss and
				// our LinkProviderIdentity call. Return that row so the
				// session points at the canonical OAuth-linked player.
				// The row we just created stays in the DB as an unlinked
				// orphan; harmless but visible to operators.
				refetched, refetchErr := identities.GetPlayerByProviderSubject(
					ctx, ProviderGoogle, subject,
				)
				if refetchErr != nil {
					return nil, fmt.Errorf("refetch after create race: %w", refetchErr)
				}

				return refetched, nil
			}

			return nil, fmt.Errorf("link identity to new player: %w", linkErr)
		}

		return player, nil
	}

	return nil, fmt.Errorf("create player after %d attempts: %w", maxPetnameAttempts, lastErr)
}

// ensureProvider lazily initialises the OIDC provider, verifier, and
// oauth2.Config the first time a request arrives. Deferring this past
// startup keeps the process bootable when Google (or the test mock) is
// briefly unreachable.
//
// Guarded by a.mu rather than a [sync.Once] because a transient
// discovery failure must be retryable: a.ready stays false on error so
// the next request tries again, instead of pinning the server in a
// failed state. The mutex (held across the discovery fetch) both
// serialises concurrent first-requests onto a single fetch and provides
// the happens-before that lets the callback path read a.oauth2 /
// a.verifier unlocked after ensureProvider returns nil. The previous
// reset-the-Once-on-error retry raced on its unexported init fields and
// could deadlock under contention (#622).
func (a *GoogleAuthenticator) ensureProvider(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ready {
		return nil
	}

	issuer := a.cfg.IssuerURL
	if issuer == "" {
		issuer = googleDefaultIssuer
	}

	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return fmt.Errorf("oidc new provider: %w", err)
	}

	a.provider = provider
	a.verifier = provider.Verifier(&oidc.Config{ClientID: a.cfg.ClientID})
	a.oauth2 = &oauth2.Config{
		ClientID:     a.cfg.ClientID,
		ClientSecret: a.cfg.ClientSecret,
		RedirectURL:  a.cfg.RedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}
	a.ready = true

	return nil
}

// newStateNonce returns a fresh random nonce as base64url-encoded
// bytes. Falls back to a no-fallback error so the handler 500s rather
// than issuing a predictable state value.
func newStateNonce() (string, error) {
	b := make([]byte, googleStateNonceLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate state nonce: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

// signState returns the cookie value: nonce + "." + HMAC(nonce). The
// HMAC binds the nonce to the deployment's secret so a value captured
// elsewhere cannot be replayed against this server.
func signState(key []byte, nonce string) string {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(nonce))

	return nonce + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// validateState pulls the state cookie + query parameter off the
// request and delegates to verifySignedState. Kept as the
// [http.Request] adapter so the HMAC + constant-time logic stays
// trivially testable from string inputs without test plumbing.
func validateState(key []byte, r *http.Request) error {
	cookie, err := r.Cookie(googleStateCookieName)
	if err != nil {
		return ErrGoogleStateMismatch
	}

	return verifySignedState(key, cookie.Value, r.URL.Query().Get("state"))
}

// verifySignedState compares the cookie and query values in constant
// time and re-verifies the HMAC on the cookie so a forged value
// cannot bypass the check. Returns ErrGoogleStateMismatch on any
// failure path.
func verifySignedState(key []byte, cookieValue, queryValue string) error {
	if cookieValue == "" || queryValue == "" {
		return ErrGoogleStateMismatch
	}
	if subtle.ConstantTimeCompare([]byte(queryValue), []byte(cookieValue)) != 1 {
		return ErrGoogleStateMismatch
	}

	parts, ok := splitState(cookieValue)
	if !ok {
		return ErrGoogleStateMismatch
	}
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(parts.Nonce))
	wantMAC := h.Sum(nil)
	gotMAC, decErr := base64.RawURLEncoding.DecodeString(parts.MAC)
	if decErr != nil {
		return ErrGoogleStateMismatch
	}
	if !hmac.Equal(gotMAC, wantMAC) {
		return ErrGoogleStateMismatch
	}

	return nil
}

// splitState splits "nonce.mac" into its two pieces; ok is false when
// the input is malformed.
func splitState(value string) (stateParts, bool) {
	for i := range value {
		if value[i] == '.' {
			return stateParts{Nonce: value[:i], MAC: value[i+1:]}, true
		}
	}

	return stateParts{}, false
}

// stateParts holds the decoded pieces of a state cookie value.
type stateParts struct {
	Nonce string
	MAC   string
}

// googleStateCookie returns the state cookie with the safe defaults.
// HttpOnly is on; Secure follows the per-deployment policy (same
// rationale as session/csrf - see [Config.SecureCookies]).
//
// Path is "/login/google" so the cookie is only sent on /login/google
// and /login/google/callback, not on every page. RFC 6265 path-match
// allows the cookie on /login/google/callback because the cookie-path
// is a proper prefix and the next character of the request-path is
// "/". Renaming the route to anything that doesn't share this prefix
// would silently break the OAuth flow - keep the cookie path and the
// route prefix in lockstep.
func googleStateCookie(value string, secure bool, maxAge int) *http.Cookie {
	//nolint:gosec // G124: Secure is intentionally policy-driven (production
	// passes true via cfg.SecureCookies(); dev passes false so plain-HTTP
	// LAN access works). See #205.
	return &http.Cookie{
		Name:     googleStateCookieName,
		Value:    value,
		Path:     "/login/google",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// googleLinkErrorMessage maps a linkOrCreateGooglePlayer failure to the
// user-facing banner. A refused registration gets its own message; any
// other failure gets the generic retry copy.
func googleLinkErrorMessage(err error) string {
	if errors.Is(err, ErrRegistrationDisabled) {
		return "Registration is currently disabled. Ask an administrator for an account."
	}

	return "Sign-in failed. Please try again."
}

// renderGoogleError re-renders the login template with a short
// message. Keeps the failed-OAuth-flow UX consistent with the
// invalid-credentials flow - a recoverable form, not an HTTP error
// page.
func renderGoogleError(
	renderer *render.Renderer,
	w http.ResponseWriter,
	r *http.Request,
	message string,
	registrationEnabled, forgotPasswordEnabled bool,
) {
	renderer.Render(w, r, http.StatusUnauthorized, formData{
		Title:              "Log in",
		Message:            message,
		ShowRegister:       registrationEnabled,
		ShowGoogle:         true,
		ShowForgotPassword: forgotPasswordEnabled,
	})
}
