package integration_test

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/quiz"
)

// TestAnonMigration_Integration covers #406: an anonymous visitor's
// game history follows them onto the account they sign into. Drives
// the full HTTP stack against a real server: register an admin,
// publish a quiz, log out, play through anonymously, then log back
// in as an existing player and verify the just-played game now
// belongs to the signed-in player.
func TestAnonMigration_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	stores := setup.Stores

	// Seed an admin + a quiz the anonymous visitor will play.
	quizRow := &quiz.Quiz{
		Title: "Migration Test Quiz", Slug: "migration-test-quiz",
		Published:         true,
		Description:       "Carry me across the auth boundary.",
		CreatedByPlayerID: seededAdminID,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "a", Correct: true}, {Text: "b"}}},
		},
	}
	if err := stores.Quizzes.CreateQuiz(ctx, quizRow); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}

	// Register the destination account so the anonymous visitor can sign
	// INTO it later. Use a throwaway client: the #574 hard gate means
	// register hands out no session, and the migration is driven by the
	// anonymous client logging in below, not by this jar.
	registerForPending(ctx, t, authClient(t), baseURL, "migration-dest", "correct-battery-13")
	destPlayer, err := stores.Players.GetPlayerByDisplayName(ctx, "migration-dest")
	if err != nil {
		t.Fatalf("lookup destination player err = %v, want nil", err)
	}
	// Stamp email_verified_at so the post-#492 verify-gate at /login
	// lets the destination account through; the migration this test
	// pins is downstream of a successful sign-in.
	verifyPlayerEmail(ctx, t, setup.DBURI, "migration-dest")

	// Anonymous play. A fresh client gets an EnsurePlayer petname row
	// the first time it hits the API.
	anonClient := authClient(t)
	primeAnonymousPlayer(ctx, t, anonClient, baseURL)
	anonPlayer := lookupAnonPlayer(ctx, t, setup.Stores.Players, "migration-dest", "admin")
	if anonPlayer.ID == destPlayer.ID || anonPlayer.ID == seededAdminID {
		t.Fatalf("anonymous player id (%d) clashed with a known seeded id", anonPlayer.ID)
	}
	finishGameInt(t, stores.Games, anonPlayer.ID, quizRow)

	// Anonymous now has one finished game on quizRow; destination has zero.
	requirePlayerGameCount(t, setup.DBURI, anonPlayer.ID, 1)
	requirePlayerGameCount(t, setup.DBURI, destPlayer.ID, 0)

	// Sign anonClient in as the destination account via POST /login.
	loginAnonAsDest(ctx, t, anonClient, baseURL, "migration-dest", "correct-battery-13")

	// After sign-in: the game should now belong to destination.
	requirePlayerGameCount(t, setup.DBURI, anonPlayer.ID, 0)
	requirePlayerGameCount(t, setup.DBURI, destPlayer.ID, 1)
}

// primeAnonymousPlayer touches GET /api/players/me so EnsurePlayer
// mints an anonymous row + sets the session cookie on the client's
// jar. The body is drained + closed inside.
func primeAnonymousPlayer(ctx context.Context, t *testing.T, client *http.Client, baseURL string) {
	t.Helper()
	resp := httpGet(ctx, t, client, baseURL+"/api/players/me")
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("prime /api/players/me status = %d, want %d", got, want)
	}
}

// lookupAnonPlayer finds the most recently created players row that
// is not one of the names the test already knows (seed admin + the
// destination account). Used because EnsurePlayer assigns a random
// petname, so the test can't ask for it by name.
func lookupAnonPlayer(ctx context.Context, t *testing.T, players auth.PlayerStore, excluded ...string) *auth.Player {
	t.Helper()
	// The seed admin is always id=1 and the destination is id=2 in
	// this test's narrow universe; the anonymous row is therefore
	// id=3 (or higher if the test grows). Probe by ascending id
	// until we hit a row whose displayName is not in the excluded set.
	for id := int64(2); id <= 20; id++ {
		p, err := players.GetPlayerByID(ctx, id)
		if err != nil {
			continue
		}
		known := slices.Contains(excluded, p.DisplayName)
		if !known {
			return p
		}
	}
	t.Fatal("could not find an anonymous player row to migrate from")

	return nil
}

// loginAnonAsDest POSTs the credentials with the anonymous client's
// jar carrying the EnsurePlayer session cookie, so the login handler
// sees a prior anonymous session and triggers the migration. The
// displayName argument is converted to "<displayName>@example.test" - the
// integration suite's convention for the email auto-assigned during
// registration, and the post-#446 login credential.
func loginAnonAsDest(ctx context.Context, t *testing.T, client *http.Client, baseURL, displayName, password string) {
	t.Helper()

	csrfToken := primeLoginCSRF(ctx, t, client, baseURL)

	form := url.Values{
		"csrf_token": {csrfToken},
		"email":      {displayName + "@example.test"},
		"password":   {password},
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/login", strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("login Do err = %v, want nil", err)
	}
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
		t.Fatalf("login status = %d, want %d", got, want)
	}
}

// primeLoginCSRF GETs /login on the supplied client (so the nonce
// cookie lands on its jar) and returns the csrf_token value rendered
// on the form. Body is drained and closed inside the helper so
// callers don't have to manage the Response lifetime.
func primeLoginCSRF(ctx context.Context, t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	resp := httpGet(ctx, t, client, baseURL+"/login")
	defer closeBody(t, resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /login body err = %v, want nil", err)
	}
	matches := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(string(body))
	if len(matches) < 2 {
		t.Fatalf("csrf token missing from /login body (excerpt: %.200q)", string(body))
	}

	return matches[1]
}

// requirePlayerGameCount opens the test DB and asserts the
// game_participants count for the given player matches want.
func requirePlayerGameCount(t *testing.T, dbURI string, playerID int64, want int) {
	t.Helper()
	db, err := sql.Open("sqlite", dbURI)
	if err != nil {
		t.Fatalf("sql.Open err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v, want nil", cerr)
		}
	})

	var got int
	if err := db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM game_participants WHERE player_id = ?`, playerID,
	).Scan(&got); err != nil {
		t.Fatalf("count game_participants err = %v, want nil", err)
	}
	if got != want {
		t.Errorf("game_participants for player %d = %d, want %d", playerID, got, want)
	}
}
