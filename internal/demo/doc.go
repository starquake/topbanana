// Package demo implements an optional public demo mode for a dedicated demo
// deployment (demo.topbanana.app). It is self-contained and removable.
//
// The DEMO_MODE_ENABLED flag is read by internal/config (Config.DemoMode);
// when on, config's applyDemoMode forces the locked-down posture (no profile,
// no registration, no Google sign-in). Handlers gate on the resulting
// Config.DemoMode / Config.ProfileEnabled flags rather than reading the env
// var themselves.
//
// Removal recipe:
//  1. rm -rf internal/demo/
//  2. rm .github/workflows/demo-reset.yml
//  3. revert the lines tagged "// DEMO MODE" (the DemoMode/ProfileEnabled
//     fields, parsing, and applyDemoMode in internal/config/config.go; the
//     route conditionals in internal/server/routes.go; the demoMode wiring in
//     internal/home/home.go; the SeedDemo command in cmd/server/app/commands.go;
//     and the -seed-demo flag, its tooManyModes entry, and its switch case in
//     cmd/server/main.go).
//
// grep -r "DEMO MODE" finds every touchpoint. The demo *deployment* (the
// deploy-demo job, the demo GitHub environment, deployments/app/
// docker-compose.demo.yml) is separate infrastructure, torn down independently.
package demo
