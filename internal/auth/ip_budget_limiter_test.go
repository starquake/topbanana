package auth_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/auth"
)

func TestIPBudgetLimiter_AdmitsBudgetThenBlocks(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	limiter := NewIPBudgetLimiterWithClock(3, time.Minute, func() time.Time { return now }, nil)

	for i := range 3 {
		if _, ok := limiter.Allow("192.0.2.1"); !ok {
			t.Fatalf("Allow #%d = false, want true (within budget)", i+1)
		}
	}
	wait, ok := limiter.Allow("192.0.2.1")
	if ok {
		t.Fatal("Allow past budget = true, want false")
	}
	if got, want := wait, time.Minute; got != want {
		t.Errorf("wait = %v, want %v", got, want)
	}
}

func TestIPBudgetLimiter_PerIP(t *testing.T) {
	t.Parallel()

	limiter := NewIPBudgetLimiter(1, time.Minute, nil)
	if _, ok := limiter.Allow("192.0.2.1"); !ok {
		t.Fatal("first IP Allow = false, want true")
	}
	if _, ok := limiter.Allow("192.0.2.2"); !ok {
		t.Error("second IP Allow = false, want true (budget is per IP)")
	}
}

// TestIPBudgetLimiter_WindowSlides pins that only the oldest stamp has to age
// out before one more request is admitted, and the reported wait counts down
// to exactly that moment.
func TestIPBudgetLimiter_WindowSlides(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	now := start
	limiter := NewIPBudgetLimiterWithClock(2, time.Minute, func() time.Time { return now }, nil)

	if _, ok := limiter.Allow("192.0.2.1"); !ok {
		t.Fatal("Allow at t=0 = false, want true")
	}
	now = start.Add(30 * time.Second)
	if _, ok := limiter.Allow("192.0.2.1"); !ok {
		t.Fatal("Allow at t=30s = false, want true")
	}
	now = start.Add(45 * time.Second)
	wait, ok := limiter.Allow("192.0.2.1")
	if ok {
		t.Fatal("Allow at t=45s = true, want false (budget spent)")
	}
	if got, want := wait, 15*time.Second; got != want {
		t.Errorf("wait at t=45s = %v, want %v", got, want)
	}

	now = start.Add(time.Minute)
	if _, ok := limiter.Allow("192.0.2.1"); !ok {
		t.Error("Allow at t=60s = false, want true (oldest stamp aged out)")
	}
	if _, ok := limiter.Allow("192.0.2.1"); ok {
		t.Error("second Allow at t=60s = true, want false (t=30s and t=60s stamps fill the budget)")
	}
}

func TestIPBudgetLimiter_PrunesStaleIPs(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	now := start
	limiter := NewIPBudgetLimiterWithClock(5, time.Minute, func() time.Time { return now }, nil)

	for _, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		if _, ok := limiter.Allow(ip); !ok {
			t.Fatalf("Allow(%s) = false, want true", ip)
		}
	}
	if got, want := IPBudgetLimiterTrackedIPs(limiter), 3; got != want {
		t.Fatalf("tracked IPs = %d, want %d", got, want)
	}

	now = start.Add(time.Minute)
	if _, ok := limiter.Allow("192.0.2.9"); !ok {
		t.Fatal("Allow after window = false, want true")
	}
	if got, want := IPBudgetLimiterTrackedIPs(limiter), 1; got != want {
		t.Errorf("tracked IPs after window = %d, want %d (stale IPs pruned)", got, want)
	}
}

func TestIPBudgetLimiter_ZeroBudgetDisables(t *testing.T) {
	t.Parallel()

	limiter := NewIPBudgetLimiter(0, time.Minute, nil)
	for i := range 100 {
		if _, ok := limiter.Allow("192.0.2.1"); !ok {
			t.Fatalf("Allow #%d = false, want true (zero budget disables)", i+1)
		}
	}
}

func TestIPBudgetLimiter_ClientIPHonoursTrustedProxy(t *testing.T) {
	t.Parallel()

	_, trusted, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatalf("ParseCIDR err = %v, want nil", err)
	}
	limiter := NewIPBudgetLimiter(1, time.Minute, []*net.IPNet{trusted})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/games", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")

	if got, want := limiter.ClientIP(req), "203.0.113.7"; got != want {
		t.Errorf("ClientIP = %q, want %q", got, want)
	}
}

func TestLimitByIP_429WithRetryAfterOverBudget(t *testing.T) {
	t.Parallel()

	calls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	})
	h := LimitByIP(next, NewIPBudgetLimiter(1, time.Minute, nil))

	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/players/me", nil))
	if got, want := first.Code, http.StatusOK; got != want {
		t.Fatalf("first status = %d, want %d", got, want)
	}

	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/players/me", nil))
	if got, want := second.Code, http.StatusTooManyRequests; got != want {
		t.Errorf("second status = %d, want %d", got, want)
	}
	if got, want := second.Header().Get("Retry-After"), "60"; got != want {
		t.Errorf("Retry-After = %q, want %q", got, want)
	}
	if got, want := calls, 1; got != want {
		t.Errorf("next calls = %d, want %d (blocked request must not reach next)", got, want)
	}
}
