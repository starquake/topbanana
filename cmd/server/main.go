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

// modeFlags holds the parsed mode-flag pointers. At most one may be set; more
// than one is a usage error rejected before dispatch.
type modeFlags struct {
	resetPasswordFor *string
	promoteAdminFor  *string
	verifyEmailFor   *string
	createAdminFor   *string
	checkOnly        *bool
	healthcheckOnly  *bool
	seedDemo         *bool
}

func main() {
	f := registerModeFlags()
	flag.Parse()

	ctx := context.Background()

	// Reject more than one mode flag: resolving by switch order would silently
	// run a different recovery action than the operator asked for.
	if tooManyModes(
		*f.resetPasswordFor != "",
		*f.promoteAdminFor != "",
		*f.verifyEmailFor != "",
		*f.createAdminFor != "",
		*f.checkOnly,
		*f.healthcheckOnly,
		*f.seedDemo,
	) {
		if _, err := fmt.Fprintln(os.Stderr,
			"error: -reset-password, -promote-admin, -verify-email, -create-admin, -check,"+
				" -healthcheck, and -seed-demo are mutually exclusive"); err != nil {
			panic(err)
		}

		os.Exit(1)
	}

	database.SetupGoose()

	var err error
	switch {
	case *f.resetPasswordFor != "":
		err = app.ResetPassword(ctx, os.Getenv, os.Stdin, os.Stdout, os.Stderr, *f.resetPasswordFor)
	case *f.promoteAdminFor != "":
		err = app.PromoteAdmin(ctx, os.Getenv, os.Stdout, os.Stderr, *f.promoteAdminFor)
	case *f.verifyEmailFor != "":
		err = app.VerifyEmail(ctx, os.Getenv, os.Stdout, os.Stderr, *f.verifyEmailFor)
	case *f.createAdminFor != "":
		err = app.CreateAdmin(ctx, os.Getenv, os.Stdin, os.Stdout, os.Stderr, *f.createAdminFor)
	case *f.checkOnly:
		err = app.Check(ctx, os.Getenv, os.Stdout)
	case *f.healthcheckOnly:
		err = app.Healthcheck(ctx, os.Getenv)
	case *f.seedDemo:
		err = app.SeedDemo(ctx, os.Getenv, os.Stderr)
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

// registerModeFlags registers the mode flags and returns their pointers. The
// caller runs [flag.Parse] before reading them.
func registerModeFlags() modeFlags {
	return modeFlags{
		checkOnly: flag.Bool(
			"check",
			false,
			"validate startup (parse config, open DB, run migrations) and exit without serving HTTP",
		),
		resetPasswordFor: flag.String(
			"reset-password",
			"",
			"reset the password for the given email; reads the new password from stdin and exits."+
				" The server should not be running concurrently against the same database."+
				" Mutually exclusive with the other mode flags",
		),
		promoteAdminFor: flag.String(
			"promote-admin",
			"",
			"promote the player with the given email to admin (sets role=admin) and exits."+
				" Break-glass recovery tool for when every admin is locked out; the first admin"+
				" normally comes from the first registration. The server should not be running"+
				" concurrently against the same database",
		),
		verifyEmailFor: flag.String(
			"verify-email",
			"",
			"mark the player with the given email as verified (stamps email_verified_at) and exits."+
				" Break-glass path for a self-hoster with no SMTP configured, where the mailed"+
				" verification link is a no-op. The server should not be running concurrently"+
				" against the same database. Mutually exclusive with the other mode flags",
		),
		createAdminFor: flag.String(
			"create-admin",
			"",
			"create a verified admin account for the given email; reads the new password from"+
				" stdin and exits. Bootstraps the first admin without opening registration or"+
				" configuring SMTP. Refuses an email that already exists (use -promote-admin /"+
				" -verify-email for that). The server should not be running concurrently against"+
				" the same database",
		),
		healthcheckOnly: flag.Bool(
			"healthcheck",
			false,
			"probe http://127.0.0.1:$PORT/healthz and exit 0/1 based on the response."+
				" Designed for Dockerfile HEALTHCHECK on distroless images (#344) so the image"+
				" doesn't need a separate wget/curl binary",
		),
		seedDemo: flag.Bool(
			"seed-demo",
			false,
			"seed the demo baseline (requires DEMO_MODE_ENABLED) and exit."+
				" The server should not be running concurrently against the same database."+
				" Mutually exclusive with the other mode flags",
		),
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
