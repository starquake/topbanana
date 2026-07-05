package admin_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/locale"
	"github.com/starquake/topbanana/internal/mailer"
)

// seedPlayerWithRole creates a player and stamps the given role, so the
// role-change handler's current-role diff and last-admin guard read real
// rows. A fresh migrated DB has no admins (the legacy seeded row backfills
// to 'player' in migration 20260529160000), so admin count is whatever
// these helpers create.
func (e *adminEnv) seedPlayerWithRole(t *testing.T, displayName, role string) int64 {
	t.Helper()

	id := e.seedPlayer(t, displayName)
	if err := e.admin.SetPlayerRole(t.Context(), id, role); err != nil {
		t.Fatalf("SetPlayerRole(%d, %q) err = %v, want nil", id, role, err)
	}

	return id
}

// postRole drives HandlePlayerSetRole against the target player with the
// desired role, returning the response recorder. The send path is wired
// to a sender that drops every message, so the existing role-change
// tests do not assert on mail.
func postRole(
	t *testing.T, env *adminEnv, targetID int64, desired string,
) *httptest.ResponseRecorder {
	t.Helper()

	rec, _ := postRoleWith(t, env, targetID, url.Values{"role": {desired}}, stubVerifyEmailSender{}, true)

	return rec
}

// postRoleWith drives HandlePlayerSetRole with an explicit form and
// sender, returning the recorder and the flash the handler stashed. The
// caller supplies the form so the opt-in checkbox can be toggled, the
// sender so a spy can record (or block) the async dispatch, and
// mailConfigured so the no-SMTP path can be exercised.
func postRoleWith(
	t *testing.T, env *adminEnv, targetID int64, form url.Values,
	sender auth.VerifyEmailSender, mailConfigured bool,
) (*httptest.ResponseRecorder, auth.SignedFlashRead) {
	t.Helper()

	return postRoleWithLang(t, env, targetID, form, sender, mailConfigured, "")
}

// postRoleWithLang is postRoleWith with an explicit UI language: a
// non-empty lang sets the lang cookie so the handler resolves that locale.
func postRoleWithLang(
	t *testing.T, env *adminEnv, targetID int64, form url.Values,
	sender auth.VerifyEmailSender, mailConfigured bool, lang string,
) (*httptest.ResponseRecorder, auth.SignedFlashRead) {
	t.Helper()
	flash := auth.NewSignedFlash([]byte("test-key-test-key-test-key-32byt"), false, "flash", "/admin")
	handler := HandlePlayerSetRole(slog.New(slog.DiscardHandler), env.admin, sender, mailConfigured, flash, nil)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/admin/players/"+strconv.FormatInt(targetID, 10)+"/role",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if lang != "" {
		req.AddCookie(&http.Cookie{Name: locale.CookieName, Value: lang})
	}
	req.SetPathValue("playerID", strconv.FormatInt(targetID, 10))
	req = req.WithContext(auth.WithPlayer(req.Context(), &auth.Player{ID: testAdminID, Role: auth.RoleAdmin}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec, readRoleFlash(t, flash, rec)
}

// readRoleFlash replays the Set-Cookie the handler wrote back into a GET
// so the test can read the stashed flash banner. A missing cookie is a
// test-setup bug, not a behaviour bug, so the helper fatals.
func readRoleFlash(t *testing.T, flash *auth.SignedFlash, rec *httptest.ResponseRecorder) auth.SignedFlashRead {
	t.Helper()

	resp := rec.Result()
	defer resp.Body.Close() //nolint:errcheck // cleanup.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/players/1", nil)
	for _, c := range resp.Cookies() {
		req.AddCookie(c)
	}

	return flash.Read(httptest.NewRecorder(), req)
}

// roleOf reloads the target's persisted role.
func (e *adminEnv) roleOf(t *testing.T, targetID int64) string {
	t.Helper()

	detail, err := e.admin.GetPlayerDetail(t.Context(), targetID)
	if err != nil {
		t.Fatalf("GetPlayerDetail(%d) err = %v, want nil", targetID, err)
	}

	return detail.Role
}

// roleMailSpy records the messages HandlePlayerSetRole dispatches and
// signals each Send over a buffered channel, so a test can wait for the
// detached goroutine deterministically instead of sleeping. It is a
// legitimate outbound spy, not a tautological store stub.
type roleMailSpy struct {
	sent chan mailer.Message
}

func newRoleMailSpy() *roleMailSpy {
	return &roleMailSpy{sent: make(chan mailer.Message, 1)}
}

func (s *roleMailSpy) Send(_ context.Context, msg mailer.Message) error {
	s.sent <- msg

	return nil
}

// seedVerifiedNonAdminPlayer returns the id of a verified credentialled
// player that is NOT auto-promoted. CreatePlayer promotes the first
// password-bearing registrant to admin, so this seeds a throwaway
// credentialled admin first to absorb that promotion before creating the
// target as a plain Player.
func (e *adminEnv) seedVerifiedNonAdminPlayer(t *testing.T, displayName, email string) int64 {
	t.Helper()

	e.seedVerifiedPlayerID(t, displayName+"-firstreg", displayName+"-firstreg@example.test", auth.RolePlayer)

	return e.seedVerifiedPlayerID(t, displayName, email, auth.RolePlayer)
}

func TestHandlePlayerSetRole_NotifyVerifiedSendsEmail(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	target := env.seedVerifiedNonAdminPlayer(t, "notify-target", "notify@example.test")
	spy := newRoleMailSpy()

	form := url.Values{"role": {auth.RoleHost}, "notify_email": {"on"}}
	rec, flash := postRoleWith(t, env, target, form, spy, true)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := env.roleOf(t, target), auth.RoleHost; got != want {
		t.Errorf("role = %q, want %q", got, want)
	}

	select {
	case msg := <-spy.sent:
		if got, want := msg.To, "notify@example.test"; got != want {
			t.Errorf("msg.To = %q, want %q", got, want)
		}
		if got, want := msg.Kind, mailer.KindRoleChangeNotice; got != want {
			t.Errorf("msg.Kind = %q, want %q", got, want)
		}
		if got, want := msg.Body, "host"; !strings.Contains(got, want) {
			t.Errorf("msg.Body = %q, should name the new role %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the role-change notice to dispatch")
	}

	if got, want := flash.Notice, "A notification email was sent to the player."; !strings.Contains(got, want) {
		t.Errorf("flash.Notice = %q, should contain %q", got, want)
	}
}

// TestHandlePlayerSetRole_NotifyDutch pins that the role-change notice is
// localized to the acting admin's request locale, including the role word.
func TestHandlePlayerSetRole_NotifyDutch(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	target := env.seedVerifiedNonAdminPlayer(t, "notify-nl-target", "notify-nl@example.test")
	spy := newRoleMailSpy()

	form := url.Values{"role": {auth.RoleHost}, "notify_email": {"on"}}
	rec, _ := postRoleWithLang(t, env, target, form, spy, true, locale.LocaleNL)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}

	select {
	case msg := <-spy.sent:
		if got, want := msg.Subject, "De rol van je Top Banana!-account is gewijzigd"; got != want {
			t.Errorf("msg.Subject = %q, want %q", got, want)
		}
		if got, want := msg.Body, "Een beheerder heeft de rol"; !strings.Contains(got, want) {
			t.Errorf("msg.Body = %q, should contain %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the role-change notice to dispatch")
	}
}

// TestHandlePlayerSetRole_NotifyAdminRoleDutch pins the Dutch role word for
// an admin promotion, exercising the localizedRoleLabel admin branch.
func TestHandlePlayerSetRole_NotifyAdminRoleDutch(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	target := env.seedVerifiedNonAdminPlayer(t, "promote-nl-target", "promote-nl@example.test")
	spy := newRoleMailSpy()

	form := url.Values{"role": {auth.RoleAdmin}, "notify_email": {"on"}}
	postRoleWithLang(t, env, target, form, spy, true, locale.LocaleNL)

	select {
	case msg := <-spy.sent:
		if got, want := msg.Body, "gewijzigd naar beheerder"; !strings.Contains(got, want) {
			t.Errorf("msg.Body = %q, should contain %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the role-change notice to dispatch")
	}
}

func TestHandlePlayerSetRole_NoNotifyDoesNotSend(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	target := env.seedVerifiedNonAdminPlayer(t, "no-notify-target", "quiet@example.test")
	spy := newRoleMailSpy()

	// No notify_email field: opt-in is off, so no mail leaves the box.
	form := url.Values{"role": {auth.RoleHost}}
	rec, flash := postRoleWith(t, env, target, form, spy, true)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := env.roleOf(t, target), auth.RoleHost; got != want {
		t.Errorf("role = %q, want %q (role still changes without opt-in)", got, want)
	}
	select {
	case msg := <-spy.sent:
		t.Errorf("unexpected send to %q; opt-out must not dispatch", msg.To)
	default:
	}
	if got, want := flash.Notice, RoleChangeNotice(auth.RoleHost); got != want {
		t.Errorf("flash.Notice = %q, want %q (plain notice, no email mention)", got, want)
	}
}

func TestHandlePlayerSetRole_NotifyUnverifiedSkipsSend(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	// Anonymous player: no email on file, so the opt-in cannot send.
	target := env.seedPlayerWithRole(t, "unverified-target", auth.RolePlayer)
	spy := newRoleMailSpy()

	form := url.Values{"role": {auth.RoleHost}, "notify_email": {"on"}}
	rec, flash := postRoleWith(t, env, target, form, spy, true)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := env.roleOf(t, target), auth.RoleHost; got != want {
		t.Errorf("role = %q, want %q", got, want)
	}
	select {
	case msg := <-spy.sent:
		t.Errorf("unexpected send to %q; no verified email must not dispatch", msg.To)
	default:
	}
	if got, want := flash.Notice, "no notification was sent"; !strings.Contains(got, want) {
		t.Errorf("flash.Notice = %q, should contain %q", got, want)
	}
}

func TestHandlePlayerSetRole_NotifyWhenMailNotConfiguredSkipsSend(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	target := env.seedVerifiedNonAdminPlayer(t, "noconfig-target", "noconfig@example.test")
	spy := newRoleMailSpy()

	// Opt-in + a verified email, but SMTP is not configured: the handler
	// must not dispatch and must not claim a mail was sent.
	form := url.Values{"role": {auth.RoleHost}, "notify_email": {"on"}}
	rec, flash := postRoleWith(t, env, target, form, spy, false)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := env.roleOf(t, target), auth.RoleHost; got != want {
		t.Errorf("role = %q, want %q", got, want)
	}
	select {
	case msg := <-spy.sent:
		t.Errorf("unexpected send to %q; an unconfigured mailer must not dispatch", msg.To)
	default:
	}
	if got, want := flash.Notice, "Email is not configured"; !strings.Contains(got, want) {
		t.Errorf("flash.Notice = %q, should contain %q", got, want)
	}
}

func TestHandlePlayerSetRole_UnknownRoleRejected(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	target := env.seedPlayerWithRole(t, "target", auth.RolePlayer)

	rec := postRole(t, env, target, "wizard")

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := env.roleOf(t, target), auth.RolePlayer; got != want {
		t.Errorf("role = %q, want %q (no mutation on unknown role)", got, want)
	}
	if got, want := len(env.auditEntries(t, target)), 0; got != want {
		t.Errorf("audit entries = %d, want %d on unknown role", got, want)
	}
}

func TestHandlePlayerSetRole_LastAdminGuard(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	// The target is the only admin, so demoting it must be refused.
	target := env.seedPlayerWithRole(t, "only-admin", auth.RoleAdmin)

	rec := postRole(t, env, target, auth.RoleHost)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := env.roleOf(t, target), auth.RoleAdmin; got != want {
		t.Errorf("role = %q, want %q (refused demotion of the last admin)", got, want)
	}
	if got, want := len(env.auditEntries(t, target)), 0; got != want {
		t.Errorf("audit entries = %d, want %d when the guard fires", got, want)
	}
}

func TestHandlePlayerSetRole_NoOpWhenUnchanged(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	// A second admin exists so the no-op path is reached on its own merits,
	// not because the guard would otherwise block.
	env.seedPlayerWithRole(t, "other-admin", auth.RoleAdmin)
	target := env.seedPlayerWithRole(t, "target-admin", auth.RoleAdmin)

	rec := postRole(t, env, target, auth.RoleAdmin)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := env.roleOf(t, target), auth.RoleAdmin; got != want {
		t.Errorf("role = %q, want %q (unchanged)", got, want)
	}
	if got, want := len(env.auditEntries(t, target)), 0; got != want {
		t.Errorf("audit entries = %d, want %d on a no-op", got, want)
	}
}

func TestHandlePlayerSetRole_TransitionsWriteRoleChangedAudit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		from string
		// secondAdmin seeds an extra admin so the last-admin guard does not
		// block a demotion away from admin.
		secondAdmin bool
		desired     string
		wantRole    string
		wantDetail  string
	}{
		{
			name:       "player to host",
			from:       auth.RolePlayer,
			desired:    auth.RoleHost,
			wantRole:   auth.RoleHost,
			wantDetail: `"from":"player","to":"host"`,
		},
		{
			name:       "host to admin",
			from:       auth.RoleHost,
			desired:    auth.RoleAdmin,
			wantRole:   auth.RoleAdmin,
			wantDetail: `"from":"host","to":"admin"`,
		},
		{
			name:        "admin to host with another admin present",
			from:        auth.RoleAdmin,
			secondAdmin: true,
			desired:     auth.RoleHost,
			wantRole:    auth.RoleHost,
			wantDetail:  `"from":"admin","to":"host"`,
		},
		{
			name:       "host to player",
			from:       auth.RoleHost,
			desired:    auth.RolePlayer,
			wantRole:   auth.RolePlayer,
			wantDetail: `"from":"host","to":"player"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := newAdminEnv(t)
			if tc.secondAdmin {
				env.seedPlayerWithRole(t, "other-admin", auth.RoleAdmin)
			}
			target := env.seedPlayerWithRole(t, "target", tc.from)

			rec := postRole(t, env, target, tc.desired)

			if got, want := rec.Code, http.StatusSeeOther; got != want {
				t.Errorf("status = %d, want %d", got, want)
			}
			if got, want := env.roleOf(t, target), tc.wantRole; got != want {
				t.Errorf("role = %q, want %q", got, want)
			}
			entries := env.auditEntries(t, target)
			if got, want := len(entries), 1; got != want {
				t.Fatalf("audit entries = %d, want %d", got, want)
			}
			if got, want := entries[0].Action, auth.AdminActionRoleChanged; got != want {
				t.Errorf("audit action = %q, want %q", got, want)
			}
			if got, want := entries[0].Payload, tc.wantDetail; !strings.Contains(got, want) {
				t.Errorf("audit payload = %q, should contain %q", got, want)
			}
		})
	}
}
