package auth_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
)

// TestGoogleAuthenticator_ConcurrentInitRetry pins #622: when OIDC
// discovery keeps failing, concurrent requests retry initialisation
// without a data race. Driven through the exported HandleGoogleLogin
// (which calls the unexported ensureProvider) against an unreachable
// issuer so every request takes the discovery-failure retry path. Run
// under -race this fails against the old sync.Once-reset retry (which
// reassigned the Once and read/wrote initErr unlocked, and could even
// deadlock) and passes with the mutex-guarded version.
func TestGoogleAuthenticator_ConcurrentInitRetry(t *testing.T) {
	t.Parallel()

	authn := auth.NewGoogleAuthenticator(auth.GoogleConfig{
		ClientID:  "test-client",
		IssuerURL: "http://127.0.0.1:1", // connection refused -> fast discovery failure
	}, []byte("test-session-key-0123456789abcdef"))
	handler := auth.HandleGoogleLogin(discardLogger(), authn)

	ctx := t.Context()
	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/login/google", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if got, want := rec.Code, http.StatusInternalServerError; got != want {
				t.Errorf("HandleGoogleLogin status = %d, want %d for an unreachable issuer", got, want)
			}
		}()
	}
	wg.Wait()
}
