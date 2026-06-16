// Application server is the main server for the application
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/database"
)

func main() {
	checkOnly := flag.Bool(
		"check",
		false,
		"validate startup (parse config, open DB, run migrations) and exit without serving HTTP",
	)
	resetPasswordFor := flag.String(
		"reset-password",
		"",
		"reset the password for the given email; reads the new password from stdin and exits."+
			" The server should not be running concurrently against the same database."+
			" Mutually exclusive with the other mode flags",
	)
	promoteAdminFor := flag.String(
		"promote-admin",
		"",
		"promote the player with the given email to admin (sets role=admin) and exits."+
			" Break-glass recovery tool for when every admin is locked out; the first admin"+
			" normally comes from the first registration. The server should not be running"+
			" concurrently against the same database",
	)
	healthcheckOnly := flag.Bool(
		"healthcheck",
		false,
		"probe http://127.0.0.1:$PORT/healthz and exit 0/1 based on the response."+
			" Designed for Dockerfile HEALTHCHECK on distroless images (#344) so the image"+
			" doesn't need a separate wget/curl binary",
	)
	flag.Parse()

	ctx := context.Background()

	// Reject more than one mode flag: resolving by switch order would silently
	// run a different recovery action than the operator asked for.
	if tooManyModes(*resetPasswordFor != "", *promoteAdminFor != "", *checkOnly, *healthcheckOnly) {
		if _, err := fmt.Fprintln(os.Stderr,
			"error: -reset-password, -promote-admin, -check, and -healthcheck are mutually exclusive"); err != nil {
			panic(err)
		}

		os.Exit(1)
	}

	database.SetupGoose()

	var err error
	switch {
	case *resetPasswordFor != "":
		err = app.ResetPassword(ctx, os.Getenv, os.Stdin, os.Stdout, os.Stderr, *resetPasswordFor)
	case *promoteAdminFor != "":
		err = app.PromoteAdmin(ctx, os.Getenv, os.Stdout, os.Stderr, *promoteAdminFor)
	case *checkOnly:
		err = app.Check(ctx, os.Getenv, os.Stdout)
	case *healthcheckOnly:
		err = app.Healthcheck(ctx, os.Getenv)
	default:
		err = app.Run(ctx, os.Getenv, os.Stdout, nil)
	}

	if err != nil {
		if _, err2 := fmt.Fprintf(os.Stderr, "error: %v\n", err); err2 != nil {
			panic(err2)
		}

		os.Exit(1)
	}
}

// tooManyModes reports whether more than one mode flag is set.
func tooManyModes(modes ...bool) bool {
	set := 0
	for _, m := range modes {
		if m {
			set++
		}
	}

	return set > 1
}
