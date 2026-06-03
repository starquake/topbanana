package admin_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
)

// testAdminID is the player id the admin-handler tests pose as for
// owner-gated routes (#281). It matches the admin row seeded by
// migration 20260111110308_add_admin_player.sql, so quiz fixtures
// attributed to this id satisfy both the created_by_player_id foreign
// key and the requireQuizOwner gate. The handler request carries an
// auth.Player with this id and RoleAdmin via withTestAdmin.
const testAdminID int64 = 1

// withTestAdmin returns r with an auth.Player on its context. Owner-gated
// routes (#281) refuse the request when no Player is on context, so tests
// that bypass the auth middleware attach the seeded admin (testAdminID)
// directly; quiz fixtures attribute themselves to that id so the
// requireQuizOwner check passes.
//
// Defined in the untagged file so both the integration-tagged handler
// tests and the untagged render tests below share one helper.
func withTestAdmin(r *http.Request) *http.Request {
	signedIn := &auth.Player{ID: testAdminID, DisplayName: "admin", Role: auth.RoleAdmin}

	return r.WithContext(auth.WithPlayer(r.Context(), signedIn))
}

// errReader is an io.ReadCloser whose Read always fails, used to drive
// the "error parsing form" branch of the save handlers.
type errReader struct{ err error }

func (e errReader) Read(_ []byte) (int, error) { return 0, e.err }
func (errReader) Close() error                 { return nil }

// scrapeFormFields extracts (name, value) pairs from <input> and <textarea>
// elements in body the way a browser would when submitting the surrounding
// form: disabled fields are skipped, and unchecked checkboxes are excluded.
//
// Limitation: every <input type="submit"> is included. A real browser only
// sends the submit button that was actually clicked, so callers must ensure
// the rendered form has at most one submit input - otherwise the resulting
// POST will include both and the handler may not behave like production.
var (
	inputElementRe    = regexp.MustCompile(`<input\b([^>]*)>`)
	textareaElementRe = regexp.MustCompile(`(?s)<textarea\b([^>]*)>(.*?)</textarea>`)
	inputNameRe       = regexp.MustCompile(`\bname="([^"]+)"`)
	inputValueRe      = regexp.MustCompile(`\bvalue="([^"]*)"`)
	inputTypeRe       = regexp.MustCompile(`\btype="([^"]+)"`)
	disabledAttrRe    = regexp.MustCompile(`\bdisabled\b`)
	checkedAttrRe     = regexp.MustCompile(`\bchecked\b`)
)

func scrapeFormFields(t *testing.T, body string) url.Values {
	t.Helper()

	values := url.Values{}
	for _, match := range inputElementRe.FindAllStringSubmatch(body, -1) {
		attrs := match[1]
		// Browsers do not submit values from disabled fields.
		if disabledAttrRe.MatchString(attrs) {
			continue
		}
		nameMatch := inputNameRe.FindStringSubmatch(attrs)
		if nameMatch == nil {
			continue
		}
		name := nameMatch[1]

		var value string
		if v := inputValueRe.FindStringSubmatch(attrs); v != nil {
			value = v[1]
		}

		// Checkboxes: only included when checked.
		if typeMatch := inputTypeRe.FindStringSubmatch(attrs); typeMatch != nil && typeMatch[1] == "checkbox" {
			if !checkedAttrRe.MatchString(attrs) {
				continue
			}
		}

		values.Add(name, value)
	}

	// Textareas - browsers submit their inner text as the field value.
	for _, match := range textareaElementRe.FindAllStringSubmatch(body, -1) {
		attrs := match[1]
		if disabledAttrRe.MatchString(attrs) {
			continue
		}
		nameMatch := inputNameRe.FindStringSubmatch(attrs)
		if nameMatch == nil {
			continue
		}
		values.Add(nameMatch[1], match[2])
	}

	return values
}

func TestTemplateRenderer_Render_LogsError(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	renderer := NewTemplateRenderer(logger, nil, "admin/pages/quizview.gohtml")

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest error: %v", err)
	}
	rr := httptest.NewRecorder()

	data := struct{ UnknownField string }{"trigger"}

	renderer.Render(rr, req, http.StatusOK, data)
	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
	}
	log := buf.String()
	if got, want := log, "error executing template"; !strings.Contains(got, want) {
		t.Fatalf("got: %q, should contain: %q, log: %q", got, want, log)
	}
}

func TestHandleIndex(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	handler := HandleIndex(logger, nil)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest error: %v", err)
	}
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, withTestAdmin(req))

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Errorf("got status code %v, want %v", got, want)
	}
	// invariant pinned by #517: the landing page surfaces a tile for
	// each of the three top-level admin sections (matching the nav) so a
	// fresh admin can discover them without typing URLs. New/Import quiz
	// moved into the Quizzes page, so they are no longer dashboard tiles.
	body := rr.Body.String()
	for _, want := range []string{
		"Admin Dashboard",
		`href="/admin/quizzes"`,
		`href="/admin/players"`,
		`href="/admin/email"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestHandleQuizCreate(t *testing.T) {
	t.Parallel()

	buf := bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	handler := HandleQuizCreate(logger, nil)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/quizzes/create", nil)
	if err != nil {
		t.Fatalf("http.NewRequest error: %v", err)
	}
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, withTestAdmin(req))

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("got status code %v, want %v, log:\n%v", got, want, buf.String())
	}
	if got, want := rr.Body.String(), "Create Quiz"; !strings.Contains(got, want) {
		t.Fatalf("got: %q, should contain: %q", got, want)
	}
}

func TestHumanizeTime(t *testing.T) {
	t.Parallel()

	// Pad each delta a few seconds inside its bucket so test scheduling
	// jitter can't push us across a boundary.
	now := time.Now()
	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{"just now (5s ago)", now.Add(-5 * time.Second), "just now"},
		{"1 minute ago", now.Add(-1*time.Minute - 5*time.Second), "1 min ago"},
		{"5 minutes ago", now.Add(-5*time.Minute - 5*time.Second), "5 min ago"},
		{"1 hour ago", now.Add(-1*time.Hour - 5*time.Second), "1 hr ago"},
		{"3 hours ago", now.Add(-3*time.Hour - 5*time.Second), "3 hr ago"},
		{"1 day ago", now.Add(-24*time.Hour - 5*time.Second), "1 day ago"},
		{"5 days ago", now.Add(-5*24*time.Hour - 5*time.Second), "5 days ago"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got, want := HumanizeTime(tc.t), tc.want; got != want {
				t.Errorf("HumanizeTime() = %q, want %q", got, want)
			}
		})
	}
}
