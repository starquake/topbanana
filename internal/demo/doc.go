// Package demo implements an optional public demo mode for a dedicated demo
// deployment (demo.topbanana.app). It is self-contained and removable.
//
// Removal recipe:
//  1. rm -rf internal/demo/
//  2. rm .github/workflows/demo-reset.yml
//  3. revert the lines tagged "// DEMO MODE" (the route conditionals in
//     internal/server/routes.go, the SeedDemo command in
//     cmd/server/app/commands.go, and the -seed-demo flag, its
//     tooManyModes entry, and its switch case in cmd/server/main.go).
//
// grep -r "DEMO MODE" finds every touchpoint. The demo *deployment* (the
// deploy-demo job, the demo GitHub environment, deployments/app/
// docker-compose.demo.yml) is separate infrastructure, torn down independently.
package demo
