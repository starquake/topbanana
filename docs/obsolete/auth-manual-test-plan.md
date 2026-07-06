# Auth manual test plan

Manual end-to-end test plan for all auth / login functionality. Run through it
before a release or after auth changes. The checkboxes are a fresh template:
copy this list (or tick in place on a branch) for each pass; an unticked box in
`main` does not mean the case fails, only that this is the reusable checklist.

## How to use the per-test email identifier

Each case has an **identifier**. Use a unique email per case via plus-addressing
on your real inbox: `info+<identifier>@example.com` (e.g. `info+reg-ok@example.com`).
They all land in one inbox, but each account is distinct, so cases don't collide
and you can find the right verification / reset email by its `+` tag.

Constants to know: password is **13-72 characters**; verify-email link valid
**24 h**; reset link valid **30 min**; invite link valid **7 days**; login
cooldown **3 s per IP**; verify-resend cooldown **60 s**.

Setup notes: registration must be enabled (`REGISTRATION_ENABLED=true`) for the
register cases; Google cases need OAuth configured; email cases need SMTP
configured (otherwise links are logged, not sent).

---

## 1. Registration (`/register`)

- [ ] **reg-ok** - register with `info+reg-ok@example.com`, a name, and a valid
  password (>= 13 chars), confirm matching. Expect: account created but **not
  signed in** (hard verify gate); the "Verify your email" confirmation page
  shows and a verification email arrives. Gated pages stay unreachable until you
  verify and log in.
- [ ] **reg-shortpw** - register `info+reg-shortpw@example.com` with a 12-char
  password. Expect: rejected on submit with a min-length message and no account
  created; while typing a too-short value you also see a live "Must be at least
  13 characters." hint under the field.
- [ ] **reg-mismatch** - register `info+reg-mismatch@example.com` with password
  != confirm. Expect: rejected with a mismatch message.
- [ ] **reg-dupemail** - register `info+reg-ok@example.com` again (same as
  reg-ok). Expect: the **same neutral "Verify your email" confirmation a fresh
  signup shows** - registration is account-existence-opaque, so there is no
  "already registered" message. No second account is created, and the existing
  owner gets a "someone tried to register with your email" notice instead.
- [ ] **reg-dupname** - register `info+reg-dupname@example.com` reusing reg-ok's
  name. Expect: rejected ("display name already taken") - a name is a public
  handle, not an enumeration secret, so this still reports the collision.
- [ ] **reg-disabled** - if `REGISTRATION_ENABLED=false`, visit `/register`.
  Expect: not reachable (404 / redirect).

## 2. Email verification (`/verify-email`, `/verify-email/pending`, `/verify-email/resend`, `/verify-email/request`)

- [ ] **verify-ok** - using reg-ok's verification email, click the link. Expect:
  email confirmed; you can now log in and reach gated pages.
- [ ] **verify-gate** - register `info+verify-gate@example.com` (an admin email,
  see roles) and WITHOUT verifying, try to reach an admin page. Expect: bounced
  to `/verify-email/pending`.
- [ ] **verify-resend** - on the pending page, click resend. Expect: a fresh
  email; clicking resend again within 60 s is rate-limited with a "slow down"
  message.
- [ ] **verify-expired** - use a verification link older than 24 h (or a consumed
  one). Expect: rejected as invalid / expired.
- [ ] **verify-request** - go to `/verify-email/request`, enter
  `info+verify-gate@example.com`. Expect: a new verification email; an unknown
  email gets the same neutral response (no enumeration).

## 3. Login (`/login`)

- [ ] **login-ok** - log in as reg-ok (verified) with the correct password
  (email is the credential). Expect: signed in, landed on the role landing page.
- [ ] **login-badpass** - log in as reg-ok with a wrong password. Expect:
  rejected, no session.
- [ ] **login-unknown** - log in with `info+login-unknown@example.com` (no
  account). Expect: rejected, with no enumeration difference vs a bad password.
- [ ] **login-unverified** - log in as a registered-but-unverified account with
  the CORRECT password. Expect: sign-in refused, a fresh verification email is
  sent, and you're directed to verify.
- [ ] **login-deeplink** - while logged out, open a deep link (e.g.
  `/admin/email`); you're sent to `/login?next=...`; log in. Expect: you land on
  the originally requested page, not the default landing.
- [ ] **login-cooldown** - submit two logins from the same browser within 3 s.
  Expect: the second shows "Too many attempts" (per-IP cooldown).

## 4. Logout (`/logout`)

- [ ] **logout-ok** - while logged in (reg-ok), click Log out. Expect: session
  cleared, redirected to `/login`; revisiting a gated page redirects to login.

## 5. Forgot / reset password (`/forgot-password`, `/reset-password`)

- [ ] **reset-ok** - `/forgot-password` with `info+reset-ok@example.com` (a
  verified account); use the emailed link; set a new valid password. Expect:
  password changed, **auto-logged-in**, landed on the role page. Old password no
  longer works; other sessions invalidated. The cooldown button on
  `/forgot-password` counts down and re-enables itself without a reload.
- [ ] **reset-expired** - use a reset link older than 30 min (or already used).
  Expect: "link no longer valid".
- [ ] **reset-unknown** - `/forgot-password` with `info+reset-unknown@example.com`
  (no account). Expect: the same neutral confirmation (no enumeration); no email.
- [ ] **reset-shortpw** - on the reset form, submit a 12-char password. Expect:
  rejected with the length message (and the live under-field hint); token not
  consumed (link still works for a retry). Password fields have visible borders.

## 6. Change password (`/profile/password`)

- [ ] **chpw-ok** - while logged in, change password with the correct current
  password + a valid new one. Expect: success; other signed-in sessions are
  signed out (session_version bump).
- [ ] **chpw-wrongcurrent** - change password with the WRONG current password.
  Expect: rejected; password unchanged.
- [ ] **chpw-shortnew** - correct current, but a too-short new password. Expect:
  rejected (with the live under-field hint while typing).

## 7. Change email (`/profile/email`)

- [ ] **chemail-ok** - while logged in as `info+chemail-ok@example.com`, change
  email to `info+chemail-new@example.com`, providing the **current password**.
  Expect: a verification email to the NEW address; the change applies only after
  confirming via that link.
- [ ] **chemail-wrongpw** - attempt the change with the wrong current password.
  Expect: rejected; email unchanged.
- [ ] **chemail-taken** - change email to one already in use by another account.
  Expect: the same neutral "we sent a link" confirmation (no enumeration); the
  link is never sent, so the change cannot complete.

## 8. Google OAuth (`/login/google`) - only if configured

- [ ] **google-new** - sign in with Google using an address with no existing
  account. Expect: an account is created and you're signed in (Google-verified
  email).
- [ ] **google-existing** - sign in with Google using an address that maps to an
  existing account. Expect: signed into that account.
- [ ] **google-deeplink** - open a deep link while logged out, choose Google
  sign-in. Expect: you land on the deep-linked page after the callback.

## 9. Admin invite + accept (`/admin/invites`, `/accept-invite`)

- [ ] **invite-ok** - as an admin, send an invite to `info+invite-ok@example.com`;
  use the emailed accept link; pick a name + password. Expect: account created
  **already verified**, auto-logged-in, lands on the role page.
- [ ] **invite-existing** - try to invite `info+reg-ok@example.com` (already has
  an account). Expect: rejected ("an account already exists - sign in instead").
- [ ] **invite-resend** - resend a pending invite. Expect: the OLD link no longer
  works; the NEW emailed link does.
- [ ] **invite-revoke** - revoke a pending invite, then click its link. Expect:
  "link no longer valid"; it drops from the pending list.
- [ ] **invite-reused** - accept an invite, then click the same link again.
  Expect: rejected (single-use).
- [ ] **invite-nav** - as an admin, reach invite management from the navbar's
  "Invites" link (not just the dashboard card).

## 10. Roles & gating (Player / Host / Admin)

- [ ] **role-player** - as a plain Player, try `/admin`. Expect: 403
  access-denied (dashboard existence not secret).
- [ ] **role-host** - as a Host, reach `/admin` + quiz / round management; but
  `/admin/players`, `/admin/email`, `/admin/settings`, `/admin/invites` return
  404 (existence hidden).
- [ ] **role-admin** - as an Admin, all of the above are reachable (200).
- [ ] **role-admin-emails** - register an address listed in `ADMIN_EMAILS`.
  Expect: promoted to Admin on registration.

## 11. Session invalidation

- [ ] **sess-reset** - log in on two browsers; reset the password on one. Expect:
  the other browser's session is invalidated on its next request.
- [ ] **sess-chpw** - same, but via change-password. Expect: the other session is
  signed out.

## 12. CSRF / hardening (spot checks)

- [ ] **csrf** - submit a login / register / reset POST with a missing or bad
  CSRF token. Expect: 403.
- [ ] **next-openredirect** - craft `/login?next=https://evil.example`. Expect:
  after login you land on the safe default, NOT the external URL.
