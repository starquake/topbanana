package auth

import (
	"context"
	"errors"
	"time"
)

// ErrPlayerNotFound is returned when a player is not found by username.
var ErrPlayerNotFound = errors.New("player not found")

// ErrUsernameTaken is returned when a username is already in use.
var ErrUsernameTaken = errors.New("username taken")

// ErrEmailTaken is returned when an email is already in use.
var ErrEmailTaken = errors.New("email taken")

// ErrPlayerAlreadyClaimed is returned by ClaimPlayer when the target row
// already has a password_hash set, so it cannot be upgraded again.
var ErrPlayerAlreadyClaimed = errors.New("player already claimed")

// ErrPlayerNotAnonymous is returned by UpdatePlayerUsername when the target
// row exists but already carries a non-anonymous identity (password_hash IS
// NOT NULL). Username-only changes are reserved for anonymous rows; a
// credentialled account must change its name through a different flow.
var ErrPlayerNotAnonymous = errors.New("player is not anonymous")

// ErrUsernameEmpty is returned by UpdatePlayerUsername when the supplied
// username trims to the empty string. Surfaced as a store-level sentinel
// so callers can map it to a 400 without re-validating themselves.
var ErrUsernameEmpty = errors.New("username is empty")

// ErrIdentityAlreadyLinked is returned by LinkProviderIdentity when the
// (provider, subject) pair already exists. Distinct from ErrUsernameTaken
// so the Google callback can distinguish a race between two concurrent
// first-time sign-ins (handled by retrying the lookup) from a true
// collision (handled as a 500).
var ErrIdentityAlreadyLinked = errors.New("identity already linked")

// Player represents an authenticated user (admin or player).
type Player struct {
	ID       int64
	Username string
	Email    string
	// EmailVerifiedAt is nil until the address is verified. OAuth-linked
	// rows are stamped at link time because the provider attests the email.
	EmailVerifiedAt *time.Time
	PasswordHash    string
	Role            string
	CreatedAt       time.Time
	UsernameClaimed bool
	// SessionVersion is bumped on password reset so every in-flight
	// cookie (which carries the version it was issued at) becomes
	// invalid the moment the reset commits (#112).
	SessionVersion int64
	// IsSuperAdmin marks a player who holds elevated powers on top of the
	// admin role (#319). Super admin is a strict superset of admin: a
	// super admin always has admin powers via [Player.IsAdmin] even if
	// the role column drifted, and is allowed to edit / delete / reset
	// scores on any quiz regardless of creator.
	IsSuperAdmin bool
}

// IsAdmin reports whether the player has admin powers. Super admin is a
// strict superset of admin, so a super admin is always an admin even if
// the role column drifted away from "admin". Use this rather than a raw
// Role == RoleAdmin comparison so the superset relationship holds in one
// place.
func (p *Player) IsAdmin() bool {
	return p.Role == RoleAdmin || p.IsSuperAdmin
}

// IsEmailVerified reports whether the player's email has been verified.
func (p *Player) IsEmailVerified() bool {
	return p.EmailVerifiedAt != nil
}

// IsAnonymous reports whether the player has no credentials set yet (no
// password_hash) AND is not an admin. Anonymous rows are created by
// EnsurePlayer for visitors who arrive without a session and may later be
// upgraded by ClaimPlayer.
//
// The admin-role exclusion guards the seeded admin row (id=1), which also
// has a NULL password_hash but must never be treated as a claimable
// anonymous row by HandleRegisterSubmit's claim path.
func (p *Player) IsAnonymous() bool {
	return p.PasswordHash == "" && p.Role != RoleAdmin
}

// HasCustomName reports whether the player has explicitly picked their
// username. Anonymous rows start with HasCustomName=false (the username
// is an auto-generated petname); they flip to true on a successful
// PATCH /api/players/me or via the register flow. Distinct from
// IsAnonymous() (which is credential-level: no password): a
// claimed-but-passwordless visitor has HasCustomName=true and
// IsAnonymous()=true simultaneously.
func (p *Player) HasCustomName() bool {
	return p.UsernameClaimed
}

// IsAuthenticated reports whether the visitor has signed in. True for
// password rows, OAuth-linked rows, and the seeded admin. Distinct
// from [Player.IsEmailVerified] - the email-verify gate is a separate
// predicate so an unverified password row can still be session-bearing.
func (p *Player) IsAuthenticated() bool {
	return p.PasswordHash != "" || p.Email != "" || p.Role == RoleAdmin
}

// AnonymousGameMigrator carries an anonymous visitor's game data onto
// the account they just signed into. Implemented by store.GameStore;
// defined here so the auth package can call into it without importing
// internal/game or internal/store, keeping the dep graph narrow and
// the post-login migration testable with a small stub.
//
// ReattributeGames moves every game_answers + game_participants row
// from fromPlayerID onto toPlayerID, skipping quizzes the destination
// player has already played (UNIQUE (player_id, quiz_id) on
// game_participants). Both moves run in a single transaction; the
// return value is the number of participant rows that actually
// moved (zero is a valid "nothing to do" outcome).
type AnonymousGameMigrator interface {
	ReattributeGames(ctx context.Context, fromPlayerID, toPlayerID int64) (int64, error)
}

// PlayerListRow is one row in the admin players list (#423/#450).
// Mirrors the shape of the underlying SQL more closely than auth.Player
// - adds the derived OAuth-link state and the onboarding-state bucket
// so the handler does not have to re-derive them per row. FinishedCount
// and LastFinishedAt come from a separate PlayerStats lookup so the
// page query stays simple.
type PlayerListRow struct {
	ID              int64
	Username        string
	Email           string
	Role            string
	HasPassword     bool
	HasOAuth        bool
	OAuthProvider   string
	CreatedAt       time.Time
	EmailVerifiedAt *time.Time
	// OnboardingState is the SQL-derived bucket label (#450). One of
	// [OnboardingStateAnonymous], [OnboardingStateUnverified],
	// [OnboardingStateOAuth], [OnboardingStateVerified]. The branch
	// order in the underlying CASE makes each row land in exactly one
	// bucket; admin status is orthogonal and exposed separately via
	// [Player.Role].
	OnboardingState string
}

// OnboardingState label constants. Kept in lockstep with the CASE
// expressions in internal/queries/admin.sql; if a new bucket is added
// there, add it here and update the admin handler's tab strip too.
const (
	// OnboardingStateAnonymous is a petname-only row with no
	// password_hash and no linked identity. The visitor has played
	// games but never claimed an account.
	OnboardingStateAnonymous = "anonymous"
	// OnboardingStateUnverified is a credentialled row (password set)
	// whose email_verified_at is still NULL. The post-#492 verify gate
	// will refuse this row at sign-in until an admin marks-verified or
	// the player clicks the resend link.
	OnboardingStateUnverified = "unverified"
	// OnboardingStateOAuth is any row with a player_identities link.
	// Treated as verified because the provider attests the email; the
	// label is distinct so the admin can spot OAuth-only accounts at
	// a glance.
	OnboardingStateOAuth = "oauth"
	// OnboardingStateVerified is a password-bearing row whose
	// email_verified_at is set. The admin's "stable, signed-in account"
	// bucket.
	OnboardingStateVerified = "verified"
	// OnboardingStateAll is the no-op filter value; pass it to the
	// list/count queries to disable the WHERE predicate.
	OnboardingStateAll = "all"
)

// OnboardingStateValues returns the user-facing filter buckets in the
// order the admin tab strip renders them. "all" is excluded - it lives
// in the template as the default tab rather than a derived state.
func OnboardingStateValues() []string {
	return []string{
		OnboardingStateAnonymous,
		OnboardingStateUnverified,
		OnboardingStateOAuth,
		OnboardingStateVerified,
	}
}

// PlayerStats is the per-player finished-quiz aggregate the admin
// list renders alongside each row. LastFinishedAt is nil for a
// player who has never finished a quiz.
type PlayerStats struct {
	PlayerID       int64
	FinishedCount  int64
	LastFinishedAt *time.Time
}

// PlayerLister is the read-only persistence interface the admin
// players page consumes (#423/#450). Kept separate from PlayerStore so
// the admin-facing surface does not bleed into the auth flow; the same
// concrete store satisfies both interfaces (mirrors how PlayerStore
// and OAuthIdentityStore share an instance).
type PlayerLister interface {
	// ListPlayersByOnboardingState returns one page of players ordered
	// by created_at DESC, filtered to rows whose derived onboarding
	// state matches state. Pass [OnboardingStateAll] to disable the
	// filter. The returned rows carry the SQL-derived OnboardingState
	// label so the handler does not have to compute it.
	ListPlayersByOnboardingState(
		ctx context.Context, state string, limit, offset int64,
	) ([]*PlayerListRow, error)
	// CountPlayersInOnboardingState returns the total number of rows
	// matching state for the pagination math. Pass
	// [OnboardingStateAll] to count every row.
	CountPlayersInOnboardingState(ctx context.Context, state string) (int64, error)
	// CountPlayersByOnboardingState returns a (state -> count) map
	// across every bucket in one round-trip. Powers the tab-strip
	// labels on the admin players list (#450).
	CountPlayersByOnboardingState(ctx context.Context) (map[string]int64, error)
	// ListPlayerFinishStats returns the finished-quiz aggregate for the
	// supplied player ids. A player with no finished games is absent
	// from the result; the caller should treat missing entries as
	// (count = 0, last = nil).
	ListPlayerFinishStats(ctx context.Context, playerIDs []int64) ([]*PlayerStats, error)
}

// PlayerDetail backs the admin per-player detail view (#450). Carries
// every column the list shows plus email_verified_at and a recent-game
// excerpt. Distinct from [PlayerListRow] so the list query can stay
// narrow.
type PlayerDetail struct {
	ID              int64
	Username        string
	Email           string
	Role            string
	HasPassword     bool
	HasOAuth        bool
	OAuthProvider   string
	CreatedAt       time.Time
	EmailVerifiedAt *time.Time
	OnboardingState string
	// IsSuperAdmin marks a player holding super-admin powers (#319/#527).
	// Surfaced on the detail view so the role selector can preselect the
	// current privilege level (player / admin / super admin).
	IsSuperAdmin bool
}

// RecentFinishedGame is one row in the "Last 5 finished games" section
// of the admin per-player detail view (#450).
type RecentFinishedGame struct {
	GameID    string
	QuizID    int64
	QuizTitle string
	CreatedAt time.Time
}

// AdminAuditEntry is one row in the admin_audit table, rendered on the
// per-player detail view's "Recent admin actions" section (#450). The
// raw Payload JSON is passed through as a string; the template decodes
// the few well-known actions on the way out. ActorPlayerID is 0 and
// ActorUsername is "" when the actor row has since been deleted (the
// actor FK is ON DELETE SET NULL, so the audit row outlives its actor).
type AdminAuditEntry struct {
	ID             int64
	ActorPlayerID  int64
	ActorUsername  string
	TargetPlayerID int64
	Action         string
	Payload        string
	CreatedAt      time.Time
}

// SuperAdminEntry is one row in the super-admin list rendered on the
// admin settings page (#320). Email is empty when the row has no address
// on file.
type SuperAdminEntry struct {
	ID       int64
	Username string
	Email    string
	// PromotedAt is when the player was promoted to super admin. Nil for
	// rows promoted before the super_admin_since column existed.
	PromotedAt *time.Time
}

// Admin action labels written to admin_audit.action. Match the spec in
// the #450 ticket; new actions belong here so the writers and the
// detail-view renderer share one set of constants.
const (
	AdminActionVerify             = "verify"
	AdminActionEmailSet           = "email_set"
	AdminActionPasswordSet        = "password_set"
	AdminActionCreated            = "created"
	AdminActionResendVerification = "resend_verification"
	AdminActionPromoteSuper       = "promote_super"
	AdminActionDemoteSuper        = "demote_super"
	AdminActionPromoteAdmin       = "promote_admin"
	AdminActionDemoteAdmin        = "demote_admin"
)

// AdminPlayerStore is the read+write persistence interface the admin
// per-player detail view + actions consume (#450). Lives in auth so
// the admin handlers can import this package without pulling in
// internal/store; the concrete PlayerStore satisfies it alongside the
// existing PlayerStore / PlayerLister / OAuthIdentityStore slots.
type AdminPlayerStore interface {
	// GetPlayerDetail returns the per-player view payload for the
	// admin detail page. Returns ErrPlayerNotFound when no row matches.
	GetPlayerDetail(ctx context.Context, id int64) (*PlayerDetail, error)
	// ListRecentFinishedGamesForPlayer returns at most limit
	// finished-game rows owned by player, newest-first. Empty slice
	// when the player has never finished a quiz.
	ListRecentFinishedGamesForPlayer(
		ctx context.Context, playerID, limit int64,
	) ([]*RecentFinishedGame, error)
	// SetPlayerEmailVerifiedNow stamps email_verified_at to the
	// current wall-clock time. Idempotent against an already-verified
	// row (the timestamp is refreshed). Returns ErrPlayerNotFound when
	// the id matches no row.
	SetPlayerEmailVerifiedNow(ctx context.Context, playerID int64) error
	// SetPlayerEmail rewrites players.email and clears email_verified_at
	// so the changed address must be re-proven. Returns ErrEmailTaken on
	// a UNIQUE collision, ErrPlayerNotFound when the id matches no row.
	SetPlayerEmail(ctx context.Context, playerID int64, email string) error
	// CreatePlayerByAdmin inserts a fresh credentialled row with
	// email_verified_at stamped. role is fixed to 'player'. Returns
	// ErrUsernameTaken / ErrEmailTaken on UNIQUE collisions.
	CreatePlayerByAdmin(
		ctx context.Context, username, email, passwordHash string,
	) (*Player, error)
	// SetPlayerSuperAdmin flips is_super_admin on the row identified by
	// id (#319). Promoting (super = true) also forces role to 'admin'
	// because super admin is a strict superset of admin; demoting
	// (super = false) leaves the admin role intact. Returns
	// ErrPlayerNotFound when no row matches.
	SetPlayerSuperAdmin(ctx context.Context, playerID int64, super bool) error
	// SetPlayerRoleAndSuperAdmin sets both role and is_super_admin on the
	// row identified by id in one statement (#527), so the id-based role
	// selector can move a player to any privilege level. The caller passes
	// the resolved (role, super) pair: player -> ("player", false),
	// admin -> ("admin", false), super admin -> ("admin", true).
	// super_admin_since is stamped when super is true and cleared
	// otherwise. Returns ErrPlayerNotFound when no row matches.
	SetPlayerRoleAndSuperAdmin(ctx context.Context, playerID int64, role string, super bool) error
	// CountSuperAdmins returns the number of current super admins. The
	// demote handler uses it to refuse a demote that would leave zero
	// super admins.
	CountSuperAdmins(ctx context.Context) (int64, error)
	// InsertAdminAudit records one admin action. payload is a
	// pre-serialised JSON blob (use "{}" when there is nothing to
	// record).
	InsertAdminAudit(
		ctx context.Context, actorPlayerID, targetPlayerID int64, action, payload string,
	) error
	// ListAdminAuditForTarget returns the most-recent audit entries
	// targeting the given player, newest first.
	ListAdminAuditForTarget(
		ctx context.Context, targetPlayerID, limit int64,
	) ([]*AdminAuditEntry, error)
}

// SuperAdminStore is the persistence interface the super-admin settings
// page (#320) consumes. It embeds AdminPlayerStore for the shared role
// writers + InsertAdminAudit and adds the one read the settings page
// needs on top: listing the current super admins. The concrete
// PlayerStore satisfies it alongside the other interface slots.
type SuperAdminStore interface {
	AdminPlayerStore
	// ListSuperAdmins returns every current super admin ordered by
	// username. Empty slice when none exist yet.
	ListSuperAdmins(ctx context.Context) ([]*SuperAdminEntry, error)
}

// PlayerStore is the persistence interface used by the auth package.
type PlayerStore interface {
	// GetPlayerByUsername returns the player with the given username.
	// Returns ErrPlayerNotFound when there is no match.
	GetPlayerByUsername(ctx context.Context, username string) (*Player, error)
	// GetPlayerByEmail returns the player with the given email.
	// Returns ErrPlayerNotFound when there is no match. Used by the
	// forgot-password flow's username-or-email lookup and the Google
	// OAuth link-by-email path.
	GetPlayerByEmail(ctx context.Context, email string) (*Player, error)
	// GetPlayerByID returns the player with the given ID.
	// Returns ErrPlayerNotFound when there is no match.
	GetPlayerByID(ctx context.Context, id int64) (*Player, error)
	// CreatePlayer creates a player with the given credentials. The
	// store trims + lowercases the email and atomically promotes the
	// first password-bearing registrant to admin. Returns
	// ErrUsernameTaken / ErrEmailTaken on UNIQUE collisions.
	CreatePlayer(ctx context.Context, username, email, passwordHash, requestedRole string) (*Player, error)
	// CreateAnonymousPlayer creates a row with the given username, no email,
	// no password_hash, and role = "player". Used by EnsurePlayer to back a
	// brand-new visitor with a real row before they can play. The caller
	// generates the username (commonly a GeneratePetname result, with an
	// xid-backed "anon-<xid>" form as the last-resort fallback) so the
	// store stays agnostic about the format.
	// Returns ErrUsernameTaken when the username collides on the UNIQUE
	// index; the petname caller treats this as a retry signal.
	CreateAnonymousPlayer(ctx context.Context, username string) (*Player, error)
	// ClaimPlayer upgrades an anonymous row (password_hash IS NULL) in place
	// with the supplied credentials and requested role. The store mirrors the
	// "first password-bearing registrant becomes admin" promotion logic of
	// CreatePlayer so the rule still triggers when a visitor's very first
	// sign-up flows through the claim path.
	// Returns ErrPlayerAlreadyClaimed when the target row already has a
	// password_hash, ErrUsernameTaken when the requested username collides
	// with another row, and ErrPlayerNotFound when the id does not exist.
	ClaimPlayer(
		ctx context.Context,
		playerID int64,
		username, email, passwordHash, requestedRole string,
	) (*Player, error)
	// SetPlayerPasswordHash overwrites the password_hash on the row identified
	// by email. Used by the operator-only -reset-password tool to rotate a
	// forgotten admin password; matching by email lines the operator's reset
	// target up with the post-#446 login credential. Returns ErrPlayerNotFound
	// when no row matches.
	SetPlayerPasswordHash(ctx context.Context, email, passwordHash string) error
	// ChangePlayerPassword atomically rotates password_hash and bumps
	// session_version on the row identified by id. The session_version
	// bump invalidates every other live cookie for the same account the
	// moment the transaction commits, mirroring the forgot-password
	// flow. Used by the signed-in /profile/password rotation; the
	// forgot-password flow still goes through ConsumeResetToken because
	// that path also marks the reset row consumed in the same
	// transaction. Returns ErrPlayerNotFound when no row matches.
	ChangePlayerPassword(ctx context.Context, playerID int64, passwordHash string) error
	// UpdatePlayerUsername renames an anonymous (password_hash IS NULL) row
	// in place. The session cookie keeps pointing at the same row, so the
	// caller stays "logged in" as the same player; the player remains
	// anonymous after the rename.
	// Returns ErrUsernameEmpty when the supplied username trims to the empty
	// string, ErrUsernameTaken when the requested username is already in use,
	// ErrPlayerNotAnonymous when the target row already carries a
	// password_hash (i.e. it is no longer a valid target for a username-only
	// update), and ErrPlayerNotFound when no row matches the id.
	UpdatePlayerUsername(ctx context.Context, playerID int64, username string) (*Player, error)
	// RenamePlayer changes the display name on any player row, regardless
	// of password_hash / email / role. Used by the profile-page rename
	// endpoint so authenticated players (password, OAuth, admin) can
	// update their own name; anonymous rows still go through
	// UpdatePlayerUsername.
	// Returns ErrUsernameEmpty for whitespace-only input, ErrUsernameTaken
	// on a UNIQUE collision, and ErrPlayerNotFound when the id does not
	// exist.
	RenamePlayer(ctx context.Context, playerID int64, username string) (*Player, error)
}
