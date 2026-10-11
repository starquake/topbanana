---
name: deploys
description: >
  How topbanana reaches development, staging and production: the
  independent pipelines in .github/workflows/deploy.yml, when the image is
  built vs retagged, what "merged to main" actually means for what is live,
  the deploy:dev PR preview, and how the per-environment secrets and
  variables are assembled. Invoke when cutting a release, changing
  deploy.yml, reasoning about whether a change is live yet, or working out
  when a migration will run.
---

# Deploys

Development, staging and production are **independent pipelines** (`.github/workflows/deploy.yml`), not a soak-and-promote chain — nothing auto-promotes from one environment to the next.

The image is **built once, after the suite is green**, and reused — the `CI` workflow's `docker-build` job `needs: [build, lint, e2e]`, so a published image always implies the tests passed for that commit (#630). `deploy.yml` keys on the `CI` workflow succeeding (not a separate Docker build), so a deploy only fires when tests + image are both green.

- **Development** deploys on every merge to `main`: `docker-build` pushes `edge` + `sha-<commit>` tags after the suite passes, a successful `CI` run on `main` fires `deploy-development`, goose runs pending migrations on container boot (12x5s health-check loop gates success). Gated on the repo variable `DEVELOPMENT_DEPLOY_ENABLED=true`.
- **Staging** deploys when a release-candidate tag `vYYYY.M.N-rc.K` is pushed: the `promote` job **retags the existing `sha-<commit>` image** to `{version}` only (e.g. `2026.10.0-rc.1`, never `latest` or `{major}.{minor}`), and a successful `CI` run on the tag fires `deploy-staging`.
- **Production** deploys when a release tag `vYYYY.M.N` is pushed: `promote` retags `sha-<commit>` to `{version}` + `{major}.{minor}` + `latest` — no rebuild and no re-run of the suite — and `deploy-production` pulls that exact version. Production is whatever the latest release tag points at. **Demo** follows the same release tags (gated on `DEMO_DEPLOY_ENABLED`).
- **Manual**: `workflow_dispatch` with an `environment` input redeploys without a code change. `version` is required for staging (an RC); production and demo take a release version or default to `latest`; development always takes the latest `main` build.

Consequences for work in flight: "merged to `main`" means live in **development**, not staging or production. Staging stays on the last RC and production on the last release until new tags are cut; all changes since the previous tag ship together. A schema migration runs on development at next `main` merge, on staging at next RC, on production at next release.

## PR preview (`deploy:dev`)

Adding the `deploy:dev` label to a same-repo PR builds its head as `pr-<n>` and deploys it into the **development slot** (same container name, `docker-compose.development-preview.yml`); each push to the labeled PR redeploys. The next `main` deploy takes the slot back. A preview runs with `APP_ENV=preview`, registration off, no Google or SMTP credentials, a session key derived per PR, and its own volume (wiped when the slot switches to a different PR); `INITIAL_ADMIN_EMAIL` / `INITIAL_ADMIN_PASSWORD` create the admin to log in with.

Every job builds a fresh `.env` from GitHub **secrets** (masked in logs: `SESSION_KEY`, `GOOGLE_CLIENT_SECRET`, `SMTP_PASSWORD`, ...) and **variables** (unmasked: `BASE_URL`, `REGISTRATION_ENABLED`, `ADMIN_EMAILS`). Both are scoped per-environment — a value set on `staging` is not visible to `production` or `development`.

Deployed environments never run with `APP_ENV=development` — that is local-machine mode (cookies lose the `Secure` flag). The development server uses `APP_ENV=dev`.
