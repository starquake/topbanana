---
name: merge-pr
description: >
  Use whenever a topbanana pull request should be merged or landed: "merge
  #NN", "land this PR", "merge the ready PRs", or a board pass finding a PR
  that carries `ready to merge`. Runs this repo's merge checks: the
  `ready to merge` label (the maintainer's approval; NEVER merge without it),
  green required checks, every review thread resolved, a title that still
  describes the diff, and a local signed rebase if behind, then squash-merges
  and confirms `main` stays green. Trigger for any merge request even if the
  user doesn't say "skill".
---

**A PR is mergeable only when it carries the `ready to merge` label.** The
label *is* the maintainer's approval (GitHub won't let the author approve
their own PR, and `gh` acts as the maintainer). Without it you stop and say
so. Never add it yourself. A verbal "merge it" in chat without the label means:
surface that the label is missing, and let them add it.

**Anything that becomes permanent at merge is verified AT merge**: the label
can be withdrawn, CI can go stale on a push, and the title can be outrun by
its own diff. Re-read all three at the last moment.

The ruleset (CLAUDE.md, "CI required checks") is what the merge must satisfy:
signed commits, every review thread resolved, the branch up to date with
`main`, and the required checks green.

## Step 1: the label

```bash
gh pr view <n> --json labels,mergeable,mergeStateStatus \
  --jq '{labels: [.labels[].name], mergeable, state: .mergeStateStatus}'
```

No `ready to merge`? **STOP**, and tell the maintainer it's missing.

## Step 2: CI must be green

```bash
gh pr checks <n>
```

Every required check must pass on the PR's current head commit. Never merge
red or pending CI.

## Step 2b: every conversation resolved

The ruleset refuses to merge while a review thread is unresolved. A thread
still open is a finding or a comment not yet acted on: act on it first
(`review-diff`), or leave it for the maintainer. Resolve a thread only once it
is answered, never to get past the rule.

```bash
gh api graphql -f query='{ repository(owner:"starquake", name:"topbanana") {
  pullRequest(number:<n>) { reviewThreads(first:50) { nodes { isResolved path line } } } } }' \
  --jq '[.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved | not)]'
```

## Step 3: the title still describes the diff

The squash commit takes the PR title as its subject, permanently, so it is a
plain one-line capitalised subject with no `Closes #N`:

```bash
gh pr view <n> --json title,files -q '.title + "\n" + ([.files[].path] | join("\n"))'
```

If the scope changed since the PR was opened, fix it with
`gh pr edit <n> --title "…"`.

## Step 4: rebase if behind

If `mergeStateStatus` is `BEHIND`/`DIRTY` or `mergeable` is `CONFLICTING`,
rebase locally (in the branch's worktree if it has one; never `git stash`):

```bash
git fetch origin --quiet && git checkout <branch> && git rebase origin/main
make check
git push --force-with-lease
```

**Never merge `main` into the branch, and never `gh pr update-branch`** (or the
"Update branch" button): a server-side update makes unsigned commits, leaving
the PR `BLOCKED` with every check green. Don't `--admin` past it.

A conflict-free rebase keeps `ready to merge`. A rebase where you resolved
conflicts is a content change: remove `ready to merge` and hand it back.
Either way CI re-runs, so go back to Step 1.

## Step 5: merge

```bash
gh pr merge <n> --squash --match-head-commit "$(gh pr view <n> --json headRefOid -q .headRefOid)"
```

`--match-head-commit` refuses the merge if anything was pushed since you
checked. Then:

- **Check every issue the merge closed still deserved closing.** Read its
  plan: if any task is unticked, the `Closes` was wrong. Reopen it, move it to
  the state of whoever holds that task, and post a Next-steps comment naming
  it.
- Otherwise the board's `Item closed → Done` workflow moves the card. Check
  that it did (`.claude/scripts/board.sh get <issue>`); if it didn't, move it
  and report that the workflow is off.
- **Wait for `main`'s own CI and staging deploy and read them.** A green PR
  does not guarantee a green `main`. If `main` is red, fixing it comes before
  anything else and does not need asking about.

  Filter by the merge commit: right after the merge, the newest `main` run is
  still the previous commit's, already completed.

  ```bash
  sha=$(gh pr view <n> --json mergeCommit -q .mergeCommit.oid)
  until [ "$(gh run list -R starquake/topbanana --commit "$sha" --workflow ci.yml --limit 1 --json status -q '.[0].status')" = "completed" ]; do sleep 30; done
  gh run list -R starquake/topbanana --commit "$sha" --json workflowName,conclusion,status
  ```

**Merging several:** one at a time, re-checking the next PR after each merge.
Merging one puts every other open PR behind `main`: rebase the next one
locally (Step 4), wait for its CI, and re-read its label before merging it.
