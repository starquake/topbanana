// Package demo implements an optional public demo mode for a dedicated demo
// deployment (demo.topbanana.app). It is self-contained and removable.
//
// Removal recipe:
//  1. rm -rf internal/demo/
//  2. rm .github/workflows/demo-reset.yml
//  3. revert the two lines tagged "// DEMO MODE" (the Guard wrap in
//     internal/server/server.go and the SeedIfEnabled call in
//     cmd/server/app/app.go).
//
// grep -r "DEMO MODE" finds every touchpoint. The demo *deployment* (the
// deploy-demo job, the demo GitHub environment, deployments/app/
// docker-compose.demo.yml) is separate infrastructure, torn down independently.
package demo
