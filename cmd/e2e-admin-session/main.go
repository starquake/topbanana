// Command e2e-admin-session prints the value of a signed session cookie
// for the seed admin (players.id = 1, role admin, verified by migration
// 20260527140000) using SESSION_KEY. The Playwright suite runs one
// server + SQLite file per worker, all sharing the same SESSION_KEY and
// each carrying the identical seed-admin row from migrations, so a single
// minted cookie authenticates against every worker. globalSetup writes it
// into a shared storageState the admin specs reuse instead of driving
// register + verify + login in each spec. Test-only tooling, not built
// into the production image.
package main

import (
	"fmt"
	"net/http/httptest"
	"os"

	"github.com/starquake/topbanana/internal/session"
)

// seedAdminID is the players.id of the seed admin inserted by migration
// 20260111110308_add_admin_player.sql; session_version defaults to 0.
const seedAdminID = 1

func main() {
	key := os.Getenv("SESSION_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "e2e-admin-session: SESSION_KEY is required")
		os.Exit(1)
	}

	rec := httptest.NewRecorder()
	// secureCookies=false: the e2e servers serve plain HTTP on localhost.
	session.New([]byte(key), false).Set(rec, seedAdminID, 0)

	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		fmt.Fprintln(os.Stderr, "e2e-admin-session: no session cookie produced")
		os.Exit(1)
	}
	fmt.Print(cookies[0].Value)
}
