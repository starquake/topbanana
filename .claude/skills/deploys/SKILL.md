---
name: deploys
description: >
  How topbanana reaches staging and production: the two independent
  pipelines in .github/workflows/deploy.yml, when the image is built vs
  retagged, what "merged to main" actually means for what is live, and how
  the per-environment secrets and variables are assembled. Invoke when
  cutting a release, changing deploy.yml, reasoning about whether a change
  is live yet, or working out when a migration will run.
---

# Deploys

Staging and production are **independent pipelines** (`.github/workflows/deploy.yml`), not a soak-and-promote chain — production never auto-promotes from staging.

The image is **built once, after the suite is green**, and reused — the `CI` workflow's `docker-build` job `needs: [build, lint, e2e]`, so a published image always implies the tests passed for that commit (#630). `deploy.yml` keys on the `CI` workflow succeeding (not a separate Docker build), so a deploy only fires when tests + image are both green.

- **Staging** deploys on every merge to `main`: `docker-build` pushes `edge` + `sha-<commit>` tags after the suite passes, a successful `CI` run on `main` fires `deploy-staging`, goose runs pending migrations on container boot (12x5s health-check loop gates success).
- **Production** deploys when a `v*.*.*` tag is pushed: the `promote` job **retags the existing `sha-<commit>` image** (the one built and tested on `main`) to `{version}` (e.g. `2026.5.8`) + `{major}.{minor}` — no rebuild and no re-run of the suite. A successful `CI` run on the tag fires `deploy-production` pulling that exact version. Production is whatever the latest `v*.*.*` tag points at.
- **Manual**: `workflow_dispatch` with an `environment` input redeploys without a code change.

Consequences for work in flight: "merged to `main`" means live in **staging**, not production. Production stays on the last tag until a new one is cut, and all changes since the previous tag ship together when it is. A schema migration runs on staging at next container boot, on production at next tag deploy.

Both jobs build a fresh `.env` from GitHub **secrets** (masked in logs: `SESSION_KEY`, `GOOGLE_CLIENT_SECRET`, `SMTP_PASSWORD`, ...) and **variables** (unmasked: `BASE_URL`, `REGISTRATION_ENABLED`, `ADMIN_EMAILS`). Both are scoped per-environment — a value set on `staging` is not visible to `production`.
