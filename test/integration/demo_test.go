package integration_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/session"
)

// TestDemo_EnterClearsHostGates is the full-stack seam test for demo mode: with
// DEMO_MODE_ENABLED=true the -seed-demo command seeds a verified Host, POST
// /demo/enter logs the visitor into that Host, and the resulting session
// satisfies the RequireGameHost + RequireVerifiedEmail gates on /admin/quizzes.
// TestDemo_EnterClearsHostGates cannot use t.Parallel because it mutates the
// process environment via t.Setenv; demo.Enabled() reads os.Getenv directly.
//
//nolint:paralleltest // t.Setenv + t.Parallel are incompatible.
func TestDemo_EnterClearsHostGates(t *testing.T) {
	// demo.Enabled() reads os.Getenv directly, not the server getenv callback,
	// so we set the real OS env variable via t.Setenv (restored on test cleanup).
	t.Setenv("DEMO_MODE_ENABLED", "true")

	ctx, srv := startServer(t, nil)
	baseURL := srv.BaseURL

	// Seed the demo baseline explicitly (the server no longer seeds at boot).
	// APP_ENV=development lets config.Parse mint an ephemeral session key so the
	// seed command needs no SESSION_KEY; demo mode is on via the t.Setenv above.
	mediaDir := t.TempDir()
	if err := app.SeedDemo(ctx, func(key string) string {
		switch key {
		case "APP_ENV":
			return "development"
		case "DB_URI":
			return srv.DBURI
		case "MEDIA_DIR":
			return mediaDir
		case "DEMO_SEED_ARCHIVE":
			return "../../dev/fixtures/demo-quiz.zip"
		default:
			return ""
		}
	}, io.Discard); err != nil {
		t.Fatalf("SeedDemo: %v", err)
	}

	client := authClient(t)

	// POST /demo/enter: expect 303 to /admin/quizzes and a session cookie.
	enterResp := httpPostEmpty(ctx, t, client, baseURL+"/demo/enter")
	defer enterResp.Body.Close() //nolint:errcheck // cleanup.

	if got, want := enterResp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("POST /demo/enter status = %d, want %d", got, want)
	}
	if got, want := enterResp.Header.Get("Location"), "/admin/quizzes"; got != want {
		t.Errorf("POST /demo/enter Location = %q, want %q", got, want)
	}

	var sawSession bool
	for _, c := range enterResp.Cookies() {
		if c.Name == session.CookieName && c.Value != "" {
			sawSession = true
		}
	}
	if !sawSession {
		t.Fatal("POST /demo/enter set no session cookie, want one")
	}

	// GET /admin/quizzes: cookie jar holds the session; expect 200, proving the
	// demo Host clears both RequireGameHost and RequireVerifiedEmail.
	snap := doGet(ctx, t, client, baseURL+"/admin/quizzes")
	if got, want := snap.StatusCode, http.StatusOK; got != want {
		t.Errorf("GET /admin/quizzes after demo enter status = %d, want %d", got, want)
	}
}

// TestDemo_RoutesAbsentWhenDisabled pins the pass-through invariant: when
// DEMO_MODE_ENABLED is not set, GET /demo is a plain 404 from the mux (the
// routes are never registered).
func TestDemo_RoutesAbsentWhenDisabled(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	baseURL := srv.BaseURL

	snap := doGet(ctx, t, authClient(t), baseURL+"/demo")
	if got, want := snap.StatusCode, http.StatusNotFound; got != want {
		t.Errorf("GET /demo (disabled) status = %d, want %d", got, want)
	}
}

// TestDemo_HomeAffordancePresentWhenEnabled asserts that when
// DEMO_MODE_ENABLED=true the demo block (containing the /demo/enter form
// action and the "resets daily" notice) is rendered on GET /. Cannot use
// t.Parallel because it mutates the process environment via t.Setenv.
//
//nolint:paralleltest // t.Setenv + t.Parallel are incompatible.
func TestDemo_HomeAffordancePresentWhenEnabled(t *testing.T) {
	t.Setenv("DEMO_MODE_ENABLED", "true")

	ctx, srv := startServer(t, nil)

	body := getBody(ctx, t, srv.BaseURL+"/")
	if got, want := strings.Contains(body, `action="/demo/enter"`), true; got != want {
		t.Errorf("demo home affordance /demo/enter present in GET / = %v, want %v", got, want)
	}
	if got, want := strings.Contains(body, "resets daily"), true; got != want {
		t.Errorf("demo home affordance 'resets daily' present in GET / = %v, want %v", got, want)
	}
}

// TestDemo_HomeAffordanceAbsentWhenDisabled asserts that when
// DEMO_MODE_ENABLED is unset the demo block does NOT appear on GET /.
func TestDemo_HomeAffordanceAbsentWhenDisabled(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)

	body := getBody(ctx, t, srv.BaseURL+"/")
	if got, want := strings.Contains(body, `action="/demo/enter"`), false; got != want {
		t.Errorf("demo home affordance /demo/enter present in GET / = %v, want %v", got, want)
	}
}

// TestDemo_ProfileLockedInDemoMode asserts that GET /profile returns 404 when
// demo mode is on because addProfileRoutes is not called and the route is never
// registered. Cannot use t.Parallel because it mutates the process environment
// via t.Setenv.
//
//nolint:paralleltest // t.Setenv + t.Parallel are incompatible.
func TestDemo_ProfileLockedInDemoMode(t *testing.T) {
	t.Setenv("DEMO_MODE_ENABLED", "true")
	ctx, srv := startServer(t, nil)
	snap := doGet(ctx, t, authClient(t), srv.BaseURL+"/profile")
	if got, want := snap.StatusCode, http.StatusNotFound; got != want {
		t.Errorf("GET /profile (demo mode) status = %d, want %d", got, want)
	}
}

// TestDemo_ProfileAccessibleWhenDisabled asserts that GET /profile does not
// return 404 when demo mode is off (the route is registered and an
// unauthenticated request is redirected to /login, not 404'd).
func TestDemo_ProfileAccessibleWhenDisabled(t *testing.T) {
	t.Parallel()
	ctx, srv := startServer(t, nil)
	snap := doGet(ctx, t, authClient(t), srv.BaseURL+"/profile")
	if got := snap.StatusCode; got == http.StatusNotFound {
		t.Errorf("GET /profile (demo disabled) status = %d, want != 404", got)
	}
}
