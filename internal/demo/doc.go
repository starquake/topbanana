// Package demo implements the optional public demo mode for the dedicated
// demo.topbanana.app deployment. Demo mode is turned on by Config.DemoMode
// (DEMO_MODE_ENABLED); config's applyDemoMode then forces the locked-down
// posture (no profile, no registration, no Google sign-in), so handlers gate
// on the resulting Config flags rather than reading the env var themselves.
//
// This package provides the two demo-only behaviours: HandleEnter, the
// one-click login into the shared demo Host mounted at POST /demo/enter, and
// SeedIfEnabled, the idempotent seeding the -seed-demo command runs to restore
// the shared demo Host and the demo quiz set read from DEMO_SEED_ARCHIVE_DIR.
package demo
