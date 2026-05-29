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
			" Takes precedence over -check when both are supplied",
	)
	promoteSuperFor := flag.String(
		"promote-super",
		"",
		"promote the player with the given username to super admin (sets is_super_admin and role=admin)"+
			" and exits. Use this to bootstrap the first super admin; they can promote others from the"+
			" admin UI afterwards. The server should not be running concurrently against the same database",
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

	database.SetupGoose()

	var err error
	switch {
	case *resetPasswordFor != "":
		err = app.ResetPassword(ctx, os.Getenv, os.Stdin, os.Stdout, os.Stderr, *resetPasswordFor)
	case *promoteSuperFor != "":
		err = app.PromoteSuper(ctx, os.Getenv, os.Stdout, os.Stderr, *promoteSuperFor)
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
