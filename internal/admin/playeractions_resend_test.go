package admin_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
)

// postResend drives HandlePlayerResendVerification against the target with
// the supplied dependencies, returning the recorder. The limiter is shared
// across calls within a test so the rate-limited branch can be exercised by
// calling twice.
func postResend(
	t *testing.T, env *adminEnv, targetID, baseURL string, limiter *PerTargetLimiter,
) *httptest.ResponseRecorder {
	t.Helper()
	handler := HandlePlayerResendVerification(
		slog.New(slog.DiscardHandler), env.admin, env.tokens, stubVerifyEmailSender{},
		baseURL, limiter, newCredFlash(t), nil,
	)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/admin/players/"+targetID+"/resend-verification", nil,
	)
	req.SetPathValue("playerID", targetID)
	req = req.WithContext(auth.WithPlayer(req.Context(), &auth.Player{ID: testAdminID, Role: auth.RoleAdmin}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func TestHandlePlayerResendVerification(t *testing.T) {
	t.Parallel()

	const baseURL = "https://x.test"

	t.Run("unparseable playerID is a 400", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		limiter := NewPerTargetLimiter(time.Minute)

		rec := postResend(t, env, "abc", baseURL, limiter)
		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("missing target is a 404", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		limiter := NewPerTargetLimiter(time.Minute)

		rec := postResend(t, env, "999999", baseURL, limiter)
		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("happy dispatch issues a token and redirects", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		target := env.seedCredentialledPlayer(t, "unverified", "unverified@example.test", auth.RolePlayer)
		limiter := NewPerTargetLimiter(time.Minute)

		rec := postResend(
			t, env, strconv.FormatInt(target, 10), baseURL, limiter,
		)
		if got, want := rec.Code, http.StatusSeeOther; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		// A real CreateVerifyToken ran on the detached goroutine; the
		// audit row is written synchronously before the redirect.
		entries := env.auditEntries(t, target)
		if got, want := len(entries), 1; got != want {
			t.Fatalf("audit entries = %d, want %d", got, want)
		}
		if got, want := entries[0].Action, auth.AdminActionResendVerification; got != want {
			t.Errorf("audit action = %q, want %q", got, want)
		}
	})

	t.Run("email not configured flashes and writes no audit", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		target := env.seedCredentialledPlayer(t, "unverified", "unverified@example.test", auth.RolePlayer)
		limiter := NewPerTargetLimiter(time.Minute)

		// Empty baseURL makes dispatchAdminResendVerification report
		// "not configured" so the handler skips the audit + success notice.
		rec := postResend(t, env, strconv.FormatInt(target, 10), "", limiter)
		if got, want := rec.Code, http.StatusSeeOther; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		if got, want := len(env.auditEntries(t, target)), 0; got != want {
			t.Errorf("audit entries = %d, want %d when email is unconfigured", got, want)
		}
	})

	t.Run("misconfigured dispatch does not consume the window", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		target := env.seedCredentialledPlayer(t, "unverified", "unverified@example.test", auth.RolePlayer)
		limiter := NewPerTargetLimiter(time.Minute)
		id := strconv.FormatInt(target, 10)

		// Empty baseURL makes dispatch report "not configured" so no mail
		// goes out; the stamp must be rolled back (#996).
		first := postResend(t, env, id, "", limiter)
		if got, want := first.Code, http.StatusSeeOther; got != want {
			t.Fatalf("first resend status = %d, want %d", got, want)
		}
		if got := first.Header().Get("Retry-After"); got != "" {
			t.Fatalf("first resend Retry-After = %q, want empty (not rate limited)", got)
		}

		// A second immediate resend must still be admitted: the window was
		// never consumed because the first send never happened. A rate-limited
		// second call would carry a Retry-After header.
		second := postResend(t, env, id, "", limiter)
		if got, want := second.Code, http.StatusSeeOther; got != want {
			t.Errorf("second resend status = %d, want %d", got, want)
		}
		if got := second.Header().Get("Retry-After"); got != "" {
			t.Errorf("second resend Retry-After = %q, want empty (window not consumed by the misconfigured first)", got)
		}
	})

	t.Run("second resend within the window is rate limited", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		target := env.seedCredentialledPlayer(t, "unverified", "unverified@example.test", auth.RolePlayer)
		limiter := NewPerTargetLimiter(time.Minute)
		id := strconv.FormatInt(target, 10)

		if got, want := postResend(t, env, id, baseURL, limiter).Code,
			http.StatusSeeOther; got != want {
			t.Fatalf("first resend status = %d, want %d", got, want)
		}

		rec := postResend(t, env, id, baseURL, limiter)
		if got, want := rec.Code, http.StatusSeeOther; got != want {
			t.Errorf("second resend status = %d, want %d", got, want)
		}
		if got, want := rec.Header().Get("Retry-After"), ""; got == want {
			t.Errorf("Retry-After = %q, want a non-empty value on the rate-limited path", got)
		}
		// The blocked second call writes no second audit row.
		if got, want := len(env.auditEntries(t, target)), 1; got != want {
			t.Errorf("audit entries = %d, want %d (only the first dispatch audits)", got, want)
		}
	})

	t.Run("store error renders a 500", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		env.seedCredentialledPlayer(t, "unverified", "unverified@example.test", auth.RolePlayer)
		limiter := NewPerTargetLimiter(time.Minute)
		env.closeStore(t)

		rec := postResend(t, env, "1", baseURL, limiter)
		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}
