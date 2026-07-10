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
	"github.com/starquake/topbanana/internal/mailer"
)

// raceApproveStore forces SetPlayerApprovedNow to report "nothing stamped",
// simulating a concurrent admin who approved between this request's pre-check and
// its write. A real store cannot express this race deterministically, so the
// double injects it; every other method delegates to the embedded real store.
type raceApproveStore struct {
	auth.AdminPlayerStore
}

func (raceApproveStore) SetPlayerApprovedNow(context.Context, int64) (bool, error) {
	return false, nil
}

// postApprove drives HandlePlayerApprove against the target with the supplied
// sender + mailConfigured flag, returning the recorder and the stashed flash.
func postApprove(
	t *testing.T, env *adminEnv, targetID int64, sender auth.VerifyEmailSender, mailConfigured bool,
) (*httptest.ResponseRecorder, auth.SignedFlashRead) {
	t.Helper()
	flash := auth.NewSignedFlash([]byte("test-key-test-key-test-key-32byt"), false, "flash", "/admin")
	handler := HandlePlayerApprove(
		slog.New(slog.DiscardHandler), env.admin, sender, mailConfigured, "https://tb.example", flash, nil,
	)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/admin/players/"+strconv.FormatInt(targetID, 10)+"/approve",
		strings.NewReader(url.Values{}.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("playerID", strconv.FormatInt(targetID, 10))
	req = req.WithContext(auth.WithPlayer(req.Context(), &auth.Player{ID: testAdminID, Role: auth.RoleAdmin}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec, readRoleFlash(t, flash, rec)
}

// approvedOf reports whether the target's approved_at is set.
func (e *adminEnv) approvedOf(t *testing.T, targetID int64) bool {
	t.Helper()
	detail, err := e.admin.GetPlayerDetail(t.Context(), targetID)
	if err != nil {
		t.Fatalf("GetPlayerDetail(%d) err = %v, want nil", targetID, err)
	}

	return detail.ApprovedAt != nil
}

func TestHandlePlayerApprove_ApprovesAuditsAndEmails(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	target := env.seedVerifiedNonAdminPlayer(t, "approve-target", "approve@example.test")
	spy := newRoleMailSpy()

	rec, flash := postApprove(t, env, target, spy, true)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if !env.approvedOf(t, target) {
		t.Error("target approved_at is nil after approve, want set")
	}
	entries := env.auditEntries(t, target)
	if got, want := len(entries), 1; got != want {
		t.Fatalf("audit entries = %d, want %d", got, want)
	}
	if got, want := entries[0].Action, auth.AdminActionApproved; got != want {
		t.Errorf("audit action = %q, want %q", got, want)
	}

	select {
	case msg := <-spy.sent:
		if got, want := msg.To, "approve@example.test"; got != want {
			t.Errorf("msg.To = %q, want %q", got, want)
		}
		if got, want := msg.Kind, mailer.KindApprovalGranted; got != want {
			t.Errorf("msg.Kind = %q, want %q", got, want)
		}
		if got, want := msg.Body, "https://tb.example/login"; !strings.Contains(got, want) {
			t.Errorf("msg.Body = %q, should contain the login link %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the approval-granted notice to dispatch")
	}

	if got, want := flash.Notice, "approved"; !strings.Contains(got, want) {
		t.Errorf("flash.Notice = %q, should contain %q", got, want)
	}
}

func TestHandlePlayerApprove_AlreadyApprovedRejected(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	target := env.seedVerifiedNonAdminPlayer(t, "already-target", "already@example.test")
	if _, err := env.admin.SetPlayerApprovedNow(t.Context(), target); err != nil {
		t.Fatalf("SetPlayerApprovedNow err = %v, want nil", err)
	}
	spy := newRoleMailSpy()

	rec, flash := postApprove(t, env, target, spy, true)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := len(env.auditEntries(t, target)), 0; got != want {
		t.Errorf("audit entries = %d, want %d when already approved", got, want)
	}
	select {
	case msg := <-spy.sent:
		t.Errorf("unexpected send to %q; an already-approved account must not re-notify", msg.To)
	default:
	}
	if got, want := flash.Err, "already approved"; !strings.Contains(got, want) {
		t.Errorf("flash.Err = %q, should contain %q", got, want)
	}
}

func TestHandlePlayerApprove_MailNotConfiguredSkipsSend(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	target := env.seedVerifiedNonAdminPlayer(t, "noconfig-approve", "noconfig-approve@example.test")
	spy := newRoleMailSpy()

	rec, flash := postApprove(t, env, target, spy, false)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if !env.approvedOf(t, target) {
		t.Error("target approved_at is nil, want set even when mail is off")
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

// TestHandlePlayerApprove_ConcurrentApproveNoOp pins the race guard: when the
// approve write stamps nothing (a concurrent admin already approved), the handler
// writes no audit row and sends no email, flashing "already approved" instead.
func TestHandlePlayerApprove_ConcurrentApproveNoOp(t *testing.T) {
	t.Parallel()

	env := newAdminEnv(t)
	target := env.seedVerifiedNonAdminPlayer(t, "race-target", "race@example.test")
	spy := newRoleMailSpy()
	flash := auth.NewSignedFlash([]byte("test-key-test-key-test-key-32byt"), false, "flash", "/admin")
	handler := HandlePlayerApprove(
		slog.New(slog.DiscardHandler), raceApproveStore{env.admin}, spy, true, "https://tb.example", flash, nil,
	)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/admin/players/"+strconv.FormatInt(target, 10)+"/approve",
		strings.NewReader(url.Values{}.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("playerID", strconv.FormatInt(target, 10))
	req = req.WithContext(auth.WithPlayer(req.Context(), &auth.Player{ID: testAdminID, Role: auth.RoleAdmin}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	fr := readRoleFlash(t, flash, rec)

	if got, want := rec.Code, http.StatusSeeOther; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
	if got, want := len(env.auditEntries(t, target)), 0; got != want {
		t.Errorf("audit entries = %d, want %d on a lost approve race", got, want)
	}
	select {
	case msg := <-spy.sent:
		t.Errorf("unexpected send to %q; a lost approve race must not notify", msg.To)
	default:
	}
	if got, want := fr.Err, "already approved"; !strings.Contains(got, want) {
		t.Errorf("flash.Err = %q, should contain %q", got, want)
	}
}
