package integration_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestHostBigScreen_JoinQRUsesTrustedBaseURL pins that the join QR target is
// built from the trusted BASE_URL, so a forged X-Forwarded-Host cannot leak
// into it (#1).
func TestHostBigScreen_JoinQRUsesTrustedBaseURL(t *testing.T) {
	t.Parallel()

	const trustedBase = "https://topbanana.example"

	ctx, setup := setupIntegrationWithEnv(t, map[string]string{"BASE_URL": trustedBase})
	baseURL := setup.BaseURL
	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "host-qr-trusted")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "host-qr-host", "host-qr-pass-123")

	code := createSession(ctx, t, host, baseURL, qz.ID)

	// Load the big screen with a forged X-Forwarded-Host.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/host/"+code, nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("X-Forwarded-Host", "attacker.example")
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := host.Do(req)
	if err != nil {
		t.Fatalf("GET host big screen err = %v, want nil", err)
	}
	defer closeBody(t, resp.Body)
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body err = %v, want nil", err)
	}
	body := string(bodyBytes)

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if want := trustedBase + "/join/" + code; !strings.Contains(body, want) {
		t.Errorf("big screen missing trusted join url %q", want)
	}
	if strings.Contains(body, "attacker.example") {
		t.Error("big screen leaked the forged X-Forwarded-Host into the page")
	}
}
