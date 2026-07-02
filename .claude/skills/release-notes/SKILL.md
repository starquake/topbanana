---
name: release-notes
description: >
  Drafts and ships a release-notes entry for Top Banana. Picks the next
  CalVer version, curates the commits since the previous tag into
  user-facing bullets, writes them to RELEASE_NOTES.md, and walks the
  branch / PR / tag / GitHub-release pipeline. Invoke when the user
  says "cut a release", "new release", "release notes for vNNN", or
  similar.
---

You are drafting end-user release notes for Top Banana and shipping the release. The output goes to `RELEASE_NOTES.md` at the repo root and gets paired with a git tag plus a GitHub release.

## What kind of doc this is

`RELEASE_NOTES.md` is for **players and quiz hosts**, not engineers — the audience can't read source. Apply the **"User-facing copy"** voice rules from `CLAUDE.md` in full: plain factual language with nothing to sell, neutral verbs over marketing ones, no framing words, no narrative story-arc intros, no second-person addressing, no trailing "so that X" rationale tails, no PR/issue numbers, and name the real limit of a fix rather than overclaiming. Release-doc specifics on top of those:

- A single factual lead sentence per release; no story-arc intro.
- Group by reader role only when it helps them find their entries: **Players / Hosts / Visual / chrome / Behind the scenes**. Drop a section when it would be empty.

## What goes in vs. what's skipped

**Include** — anything a player or host would notice:

- New player-facing surfaces (start page, share dialog, leaderboard view).
- Bug fixes the user could have seen (wrong score in a share, stalled round, missing UI affordance).
- Host-facing features (spoiler toggle, JSON import, reordering buttons).
- Visual / chrome changes (theme migration result, logo behaviour, button placement, favicon).
- Security or correctness changes the user can perceive (one-attempt-per-account, can't probe stranger's game URL).
- Operator-visible config — env vars, CLI flags — go under **Behind the scenes**.

**Skip** — engineering-only changes:

- Dependency bumps from Dependabot. Skip silently.
- Test-only changes (added integration test, restructured fixtures, e2e parallel split).
- Code-style sweeps and refactors with no behavioural change.
- CI workflow tweaks unless they change the operator's deploy story.
- Documentation-only PRs (`CLAUDE.md`, `docs/Vision.md`, this skill itself).
- Generated-code regens (`internal/db/*.sql.go`, `internal/assets/static/css/app.css`).
- Internal infra migrations (Bulma → Tailwind, htmx version bump) unless the *visible* result is a meaningful change — in which case describe the visible result, not the migration.

## Format

```
## v2026.M.N — YYYY-MM-DD

One factual lead sentence — what the release is about, no adjectives.

### Players
- Bullet.
- Bullet.

### Hosts
- Bullet.

### Visual / chrome
- Bullet.

### Behind the scenes
- Bullet.
```

Each bullet is a single declarative sentence. Use present tense (`now appears`, `links back to`, `shows`), matching the existing entries.

Single-section releases are fine — drop the section header in that case and just list bullets under the lead.

## Versioning

Top Banana uses **Calendar Versioning** (`YYYY.MM.MICRO`) from `v2026.5.0` onward. Mechanically:

1. Read the most recent tag with `git tag --sort=-creatordate | head -1`.
2. If today's month matches the tag's month, bump MICRO (`v2026.5.3` → `v2026.5.4`).
3. If today's month is later, start MICRO at 0 (`v2026.5.4` on May → `v2026.6.0` on June).
4. Year rolls the same way.

No need to ask the user for the version — derive it.

## Workflow

1. **Branch off main**.

   ```
   git checkout main && git pull --rebase
   git checkout -b release-vYYYY.M.N
   ```

2. **Gather the diff since the last release**.

   ```
   git log <previous-tag>..main --oneline
   ```

   Read each commit subject. For ambiguous ones, run `gh pr view <number>` to see what the PR actually shipped.

3. **Draft the new section** at the top of `RELEASE_NOTES.md`, directly under the `# Release notes` title and the paragraph that follows it. Insert above the previous release.

   **Also bump the `VERSION` file** at the repo root to the same version in bare form (no `v` prefix, single line, e.g. `2026.6.0`). The build reads this file and stamps it into the binary, so the admin footer and `/version` endpoint report the release (#663). Keep it in lockstep with the `## vYYYY.M.N` heading you just wrote, and stage it alongside `RELEASE_NOTES.md` in the release-notes commit.

4. **Audit the draft** before showing the user. Re-read the voice rules above. Common drift to catch:

   - Editorial words in the lead ("an audit pass that **tightens**", "the **big** one").
   - "so you can / lets you" framing in bullets.
   - Trailing "so that X doesn't Y" clauses where X and Y are already clear.
   - Overclaimed scope on security fixes — describe the rule that's enforced, not the threat it appears to defeat.
   - Issue / PR numbers leaked in.

5. **Show the diff to the user and ask for sign-off.** Standard "Did the review look OK?" gate.

6. **After sign-off**: commit, push, open PR with empty body (no `Closes #N` — there is no ticket), wait for checks, squash-merge, sync local main. Follow the existing project commit/PR conventions.

7. **Wait for `main`'s image build before tagging.** Merging the notes kicks off the post-merge `CI` run on `main`, whose `docker-build` job publishes the `sha-<commit>` (and `edge`) image. The tag's `promote` job does **not** rebuild — it *retags that exact `sha-<commit>` image* to the version. So the image must already exist when you push the tag; tag too soon and `promote` fails with `sha-<commit> not found; tag a commit that was built on main`, the run is marked failed, and the production deploy never fires (the tag + GitHub release still succeed, leaving a half-shipped release). Watch the post-merge run to completion and confirm the image exists before tagging:

   ```
   git checkout main && git pull --rebase
   # Wait for the post-merge CI run (build/lint/e2e -> docker-build) to finish.
   MAIN_CI=$(gh run list --branch main --workflow CI --limit 1 --json databaseId --jq '.[0].databaseId')
   gh run watch "$MAIN_CI" --exit-status
   # Confirm the docker-build job actually ran and succeeded - it is what
   # publishes the sha-<commit> image the tag's promote retags. (Use the job
   # result, not a GHCR query: the gh token usually lacks read:packages, so a
   # `gh api .../packages` lookup 403s and looks falsely empty.)
   gh run view "$MAIN_CI" --json jobs \
     --jq '.jobs[] | select(.name=="docker-build") | .conclusion' | grep -qx success \
     && echo "image published, safe to tag" || echo "docker-build did NOT succeed - do not tag yet"
   ```

   Also confirm `MAIN_CI` is the run for the notes commit (`gh run view "$MAIN_CI" --json headSha`), not a later push. If `docker-build` did not succeed, do not tag - resolve it first rather than pushing a tag the `promote` step cannot satisfy.

8. **Tag and ship the release** once the image is published:

   ```
   git tag -a vYYYY.M.N -m "vYYYY.M.N"
   git push origin vYYYY.M.N

   gh release create vYYYY.M.N \
     --title "vYYYY.M.N" \
     --notes-file <({ printf '## <short factual title from the lead sentence>\n\n'; awk '/^## vYYYY.M.N/{flag=1; next} /^## v/{flag=0} flag' RELEASE_NOTES.md; }) \
     --generate-notes \
     --notes-start-tag <previous-tag>
   ```

   **The GitHub release title is just the bare version (`vYYYY.M.N`)** so the releases list stays clean; the descriptive `<short factual title from the lead sentence>` becomes the **first heading of the body** (`## <title>`, version dropped since the release already carries it), not the release title. The `--notes-file` process substitution therefore prints that heading first, then the awk extract of the new release's section out of `RELEASE_NOTES.md`. `--generate-notes` is **mandatory**: gh prepends the curated `--notes-file` body and appends GitHub's auto-generated "What's Changed" PR list plus the Full Changelog link, so the release body reads `## <title>` then `curated notes` then `## What's Changed`. `--notes-start-tag <previous-tag>` (the same previous tag from step 2) scopes that PR list to this release's range. Every release must carry the PR list — it is the per-PR engineering history `RELEASE_NOTES.md` points readers to; omitting `--generate-notes` is the drift that left several releases without it. Use a process substitution (or a temp file) — `gh release create` reads `--notes-file` from a file path.

9. Confirm the release page renders the notes correctly: `gh release view vYYYY.M.N`. Then confirm the production deploy fired: the tag's `CI` run (its `promote` job) must go green, which is what triggers `deploy-production`. If `promote` failed because the image was not yet published when you tagged, re-run that job once the image exists (`gh run rerun <tag-CI-run-id> --failed`) - do not push a new tag.

## What not to do

- Do not invent features that aren't on `main`. Every bullet must trace back to a merged commit.
- Do not include unreleased work-in-progress from feature branches.
- Do not write a body for the PR that updates `RELEASE_NOTES.md` — there is no associated ticket.
- Do not tag before the release-notes PR has merged. The tag should point at the commit that contains the notes.
- Do not push the version tag before `main`'s `docker-build` has published the `sha-<commit>` image (step 7). The tag's `promote` retags that image; tagging early fails `promote` and the production deploy never fires.
- Do not bump CalVer to skip a number. Sequential micros within the month.
- Do not edit a tag once pushed. If the notes are wrong, ship a follow-up release.
