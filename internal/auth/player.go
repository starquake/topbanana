package auth

import (
	"context"
	"errors"
	"time"
)

// ErrPlayerNotFound is returned when a player is not found by displayName.
var ErrPlayerNotFound = errors.New("player not found")

// ErrDisplayNameTaken is returned when a displayName is already in use.
var ErrDisplayNameTaken = errors.New("displayName taken")

// ErrEmailTaken is returned when an email is already in use.
var ErrEmailTaken = errors.New("email taken")

// ErrPlayerAlreadyClaimed is returned by ClaimPlayer when the target row
// already has a password_hash set, so it cannot be upgraded again.
var ErrPlayerAlreadyClaimed = errors.New("player already claimed")

// ErrPlayerNotAnonymous is returned by UpdatePlayerDisplayName when the target
// row exists but already carries a non-anonymous identity (password_hash IS
// NOT NULL). DisplayName-only changes are reserved for anonymous rows; a
// credentialled account must change its name through a different flow.
var ErrPlayerNotAnonymous = errors.New("player is not anonymous")

// ErrDisplayNameEmpty is returned by UpdatePlayerDisplayName when the supplied
// displayName trims to the empty string. Surfaced as a store-level sentinel
// so callers can map it to a 400 without re-validating themselves.
var ErrDisplayNameEmpty = errors.New("displayName is empty")

// ErrIdentityAlreadyLinked is returned by LinkProviderIdentity when the
// (provider, subject) pair already exists. Distinct from ErrDisplayNameTaken
// so the Google callback can distinguish a race between two concurrent
// first-time sign-ins (handled by retrying the lookup) from a true
// collision (handled as a 500).
var ErrIdentityAlreadyLinked = errors.New("identity already linked")

// ErrLastAdmin is returned by DemoteAdmin when the change would strip Admin
// from the only remaining Admin. The store enforces the invariant atomically
// so two concurrent demotions cannot both pass and leave zero admins; the
// handler maps this to the "promote another first" refusal.
var ErrLastAdmin = errors.New("cannot remove the last admin")

// Player represents an authenticated user (admin or player).
type Player struct {
	ID          int64
	DisplayName string
	Email       string
	// EmailVerifiedAt is nil until the address is verified. OAuth-linked
	// rows are stamped at link time because the provider attests the email.
	EmailVerifiedAt    *time.Time
	PasswordHash       string
	Role               string
	CreatedAt          time.Time
	DisplayNameClaimed bool
	// SessionVersion is bumped on password reset so every in-flight
	// cookie (which carries the version it was issued at) becomes
	// invalid the moment the reset commits (#112).
	SessionVersion int64
}

// IsAdmin reports whether the player holds the top (Admin) tier: full access
// to player management, role changes, account creation, email diagnostics,
// settings, and edit/delete/reset on any quiz regardless of creator (#538).
func (p *Player) IsAdmin() bool {
	return p.Role == RoleAdmin
}

// CanHost reports whether the player may reach the dashboard and create /
// manage games. True for Host and Admin (Admin is a superset of Host); own-game
// ownership is still checked separately via the quiz's created_by_player_id.
func (p *Player) CanHost() bool {
	return p.Role == RoleHost || p.Role == RoleAdmin
}

// IsEmailVerified reports whether the player's email has been verified.
func (p *Player) IsEmailVerified() bool {
	return p.EmailVerifiedAt != nil
}

// IsAnonymous reports whether the player has no credentials set yet (no
// password_hash, no email) AND holds the default Player tier.
//
// The email exclusion keeps an OAuth-claimed row (email set, no password) from
// reading as anonymous: it is a real account, so migrateGamesAfterSignIn must
// not reattribute its game history onto a different account.
//
// The non-player exclusion guards the seeded admin row (id=1), which has a NULL
// password_hash but holds the Host tier after the #538 remap; it must never be
// treated as a claimable anonymous row by HandleRegisterSubmit's claim path.
func (p *Player) IsAnonymous() bool {
	return p.PasswordHash == "" && p.Email == "" && p.Role == RolePlayer
}

// HasCustomName reports whether the player has explicitly picked their
// displayName. Anonymous rows start with HasCustomName=false (the displayName
// is an auto-generated petname); they flip to true on a successful
// PATCH /api/players/me or via the register flow. Distinct from
// IsAnonymous() (which is credential-level: no password): a
// claimed-but-passwordless visitor has HasCustomName=true and
// IsAnonymous()=true simultaneously.
func (p *Player) HasCustomName() bool {
	return p.DisplayNameClaimed
}

// IsAuthenticated reports whether the visitor has signed in. True for
// password rows, OAuth-linked rows, and any non-Player tier (so the seeded
// admin, which is Host after the #538 remap, still counts). Distinct from
// [Player.IsEmailVerified] - the email-verify gate is a separate predicate so
// an unverified password row can still be session-bearing.
func (p *Player) IsAuthenticated() bool {
	return p.PasswordHash != "" || p.Email != "" || p.Role != RolePlayer
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
	DisplayName     string
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
	DisplayName     string
	Email           string
	Role            string
	HasPassword     bool
	HasOAuth        bool
	OAuthProvider   string
	CreatedAt       time.Time
	EmailVerifiedAt *time.Time
	OnboardingState string
}

// RecentFinishedGame is one row in the "Last 5 finished games" section
// of the admin per-player detail view (#450).
type RecentFinishedGame struct {
	GameID    string
	QuizID    int64
	QuizTitle string
	CreatedAt time.Time
}

// FinishedSessionPlay is one row in the "Live quiz plays" section of the
// admin per-player detail view: a quiz the player has played in a finished
// live session, with the most recent finish time. The reset affordance
// posts the quiz id back so an admin can clear the play.
type FinishedSessionPlay struct {
	QuizID     int64
	QuizTitle  string
	FinishedAt time.Time
}

// AdminAuditEntry is one row in the admin_audit table, rendered on the
// per-player detail view's "Recent admin actions" section (#450). The
// raw Payload JSON is passed through as a string; the template decodes
// the few well-known actions on the way out. ActorPlayerID is 0 and
// ActorDisplayName is "" when the actor row has since been deleted (the
// actor FK is ON DELETE SET NULL, so the audit row outlives its actor).
type AdminAuditEntry struct {
	ID               int64
	ActorPlayerID    int64
	ActorDisplayName string
	TargetPlayerID   int64
	Action           string
	Payload          string
	CreatedAt        time.Time
}

// AdminEntry is one row in the Admins list rendered on the admin settings
// page (#320/#538). Email is empty when the row has no address on file.
type AdminEntry struct {
	ID          int64
	DisplayName string
	Email       string
	// RoleChangedAt is when the player's role last changed (the promotion to
	// Admin, in practice). Nil for rows whose role predates the column.
	RoleChangedAt *time.Time
}

// Admin action labels written to admin_audit.action. Match the spec in
// the #450 ticket; new actions belong here so the writers and the
// detail-view renderer share one set of constants.
//
// AdminActionRoleChanged is the single role-change action new writes use
// (#538); its payload carries {"from":<role>,"to":<role>}. The four legacy
// promote/demote constants are kept ONLY so the detail view can still render
// historical audit rows - no new write emits them.
const (
	AdminActionVerify             = "verify"
	AdminActionEmailSet           = "email_set"
	AdminActionDisplayNameSet     = "display_name_set"
	AdminActionPasswordSet        = "password_set"
	AdminActionCreated            = "created"
	AdminActionResendVerification = "resend_verification"
	AdminActionRoleChanged        = "role_changed"
	AdminActionPromoteSuper       = "promote_super"
	AdminActionDemoteSuper        = "demote_super"
	AdminActionPromoteAdmin       = "promote_admin"
	AdminActionDemoteAdmin        = "demote_admin"
	AdminActionLiveQuizReset      = "live_quiz_reset"
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
	// ListFinishedSessionPlaysForPlayer returns at most limit live-quiz
	// plays the player has finished, newest-first, one row per quiz. Empty
	// slice when the player has never finished a live session.
	ListFinishedSessionPlaysForPlayer(
		ctx context.Context, playerID, limit int64,
	) ([]*FinishedSessionPlay, error)
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
	// email_verified_at stamped. role sets the new row's tier. Returns
	// ErrDisplayNameTaken / ErrEmailTaken on UNIQUE collisions.
	CreatePlayerByAdmin(
		ctx context.Context, displayName, email, passwordHash, role string,
	) (*Player, error)
	// SetPlayerRole sets the role on the row identified by id (#538), so the
	// id-based role selector can move a player to any tier. role is one of
	// RolePlayer / RoleHost / RoleAdmin; role_changed_at is stamped to the
	// current time. Returns ErrPlayerNotFound when no row matches.
	SetPlayerRole(ctx context.Context, playerID int64, role string) error
	// DemoteAdmin moves an Admin row to the supplied non-admin role, refusing
	// the change atomically when the row is the only remaining Admin so two
	// concurrent demotions cannot both pass and leave zero admins (#997).
	// Returns ErrLastAdmin when the change would empty the Admin tier, and
	// ErrPlayerNotFound when no Admin row matches the id (a missing row or a
	// row that is no longer Admin). role must be a non-admin tier.
	DemoteAdmin(ctx context.Context, playerID int64, role string) error
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
	// AdminRenamePlayer changes the display name on any row, leaving
	// display_name_claimed untouched so an admin tidying a guest's auto-petname
	// does not promote the row to a self-claimed account. Returns
	// ErrDisplayNameEmpty for whitespace-only input, ErrDisplayNameTaken on a
	// UNIQUE collision, and ErrPlayerNotFound when the id does not exist.
	AdminRenamePlayer(ctx context.Context, playerID int64, displayName string) (*Player, error)
	// ChangePlayerPassword atomically rotates password_hash and bumps
	// session_version on the row identified by id. The session_version bump
	// invalidates every other live cookie for the same account the moment
	// the transaction commits, so an admin-driven reset signs the target out
	// of their other sessions. Returns ErrPlayerNotFound when no row matches.
	ChangePlayerPassword(ctx context.Context, playerID int64, passwordHash string) error
}

// AdminListStore is the persistence interface the admin settings page
// (#320/#538) consumes. It embeds AdminPlayerStore for the shared role writers
// + InsertAdminAudit and adds the one read the settings page needs on top:
// listing the current Admins. The concrete PlayerStore satisfies it alongside
// the other interface slots.
type AdminListStore interface {
	AdminPlayerStore
	// ListAdmins returns every current Admin ordered by displayName. Empty
	// slice when none exist yet.
	ListAdmins(ctx context.Context) ([]*AdminEntry, error)
}

// PlayerStore is the persistence interface used by the auth package.
type PlayerStore interface {
	// GetPlayerByDisplayName returns the player with the given display name.
	// Returns ErrPlayerNotFound when there is no match.
	GetPlayerByDisplayName(ctx context.Context, displayName string) (*Player, error)
	// GetPlayerByEmail returns the player with the given email.
	// Returns ErrPlayerNotFound when there is no match. Used by the
	// forgot-password flow's displayName-or-email lookup and the Google
	// OAuth link-by-email path.
	GetPlayerByEmail(ctx context.Context, email string) (*Player, error)
	// GetPlayerByID returns the player with the given ID.
	// Returns ErrPlayerNotFound when there is no match.
	GetPlayerByID(ctx context.Context, id int64) (*Player, error)
	// CreatePlayer creates a player with the given credentials. The
	// store trims + lowercases the email and atomically promotes the
	// first password-bearing registrant to admin. Returns
	// ErrDisplayNameTaken / ErrEmailTaken on UNIQUE collisions.
	CreatePlayer(ctx context.Context, displayName, email, passwordHash, requestedRole string) (*Player, error)
	// CreateAnonymousPlayer creates a row with the given displayName, no email,
	// no password_hash, and role = "player". Used by EnsurePlayer to back a
	// brand-new visitor with a real row before they can play. The caller
	// generates the displayName (commonly a GeneratePetname result, with an
	// xid-backed "anon-<xid>" form as the last-resort fallback) so the
	// store stays agnostic about the format.
	// Returns ErrDisplayNameTaken when the displayName collides on the UNIQUE
	// index; the petname caller treats this as a retry signal.
	CreateAnonymousPlayer(ctx context.Context, displayName string) (*Player, error)
	// ClaimPlayer upgrades an anonymous row (password_hash IS NULL) in place
	// with the supplied credentials and requested role. The store mirrors the
	// "first password-bearing registrant becomes admin" promotion logic of
	// CreatePlayer so the rule still triggers when a visitor's very first
	// sign-up flows through the claim path.
	// Returns ErrPlayerAlreadyClaimed when the target row already has a
	// password_hash, ErrDisplayNameTaken when the requested displayName collides
	// with another row, and ErrPlayerNotFound when the id does not exist.
	ClaimPlayer(
		ctx context.Context,
		playerID int64,
		displayName, email, passwordHash, requestedRole string,
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
	// UpdatePlayerDisplayName renames an anonymous (password_hash IS NULL) row
	// in place. The session cookie keeps pointing at the same row, so the
	// caller stays "logged in" as the same player; the player remains
	// anonymous after the rename.
	// Returns ErrDisplayNameEmpty when the supplied display name trims to the empty
	// string, ErrDisplayNameTaken when the requested display name is already in use,
	// ErrPlayerNotAnonymous when the target row already carries a
	// password_hash (i.e. it is no longer a valid target for a display-name-only
	// update), and ErrPlayerNotFound when no row matches the id.
	UpdatePlayerDisplayName(ctx context.Context, playerID int64, displayName string) (*Player, error)
	// RenamePlayer changes the display name on any player row, regardless
	// of password_hash / email / role. Used by the profile-page rename
	// endpoint so authenticated players (password, OAuth, admin) can
	// update their own name; anonymous rows still go through
	// UpdatePlayerDisplayName.
	// Returns ErrDisplayNameEmpty for whitespace-only input, ErrDisplayNameTaken
	// on a UNIQUE collision, and ErrPlayerNotFound when the id does not
	// exist.
	RenamePlayer(ctx context.Context, playerID int64, displayName string) (*Player, error)
}
