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
		"reset the password for the given username; reads the new password from stdin and exits."+
			" The server should not be running concurrently against the same database."+
			" Takes precedence over -check when both are supplied",
	)
	flag.Parse()

	ctx := context.Background()

	database.SetupGoose()

	var err error
	switch {
	case *resetPasswordFor != "":
		err = app.ResetPassword(ctx, os.Getenv, os.Stdin, os.Stdout, os.Stderr, *resetPasswordFor)
	case *checkOnly:
		err = app.Check(ctx, os.Getenv, os.Stdout)
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
