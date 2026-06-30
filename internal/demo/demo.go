package demo

import (
	"os"
	"strconv"
)

// envEnabled is the env var that turns demo mode on for a deployment.
const envEnabled = "DEMO_MODE_ENABLED"

// Enabled reports whether demo mode is on, parsed from DEMO_MODE_ENABLED. An
// unset or unparseable value is off. The package owns its own flag so config.go
// is never touched; callers (the route conditionals in internal/server/routes.go
// and SeedIfEnabled) read it once at startup.
func Enabled() bool {
	on, _ := strconv.ParseBool(os.Getenv(envEnabled))

	return on
}
