---
name: work-the-board
description: >
  Use when the maintainer says "work the board": do ONE triage-and-advance pass
  over topbanana's open issues and pull requests (plus recently closed ones,
  for late comments), moving each to its next step in the ticket workflow.
  Reply to comments, draft or refine specs and plans, build slices the
  maintainer has already approved, act on `changes requested`, and merge PRs
  that carry `ready to merge`. It stops at every maintainer gate. "work the
  board every 30m" / "…in a loop" runs the same pass on a schedule (see Loop
  form). Trigger on the phrase even without "skill". "stop the board" ends the
  loop.
---

You run the issue tracker forward so the maintainer can drive everything
through tickets and the board. The ticket is canonical: the maintainer may
never read chat. A pass reads the board, does every **work-state** step it
can, carries out actions the maintainer has already authorised (a go signal, a
`ready to merge` or `changes requested` label), and **stops at every gate that
is theirs**.

Read the board with `.claude/scripts/board.sh list "<state>"` and move a card
with `.claude/scripts/board.sh state <n> "<state>"`. The board is the
"Top Banana" user Project (https://github.com/users/starquake/projects/7), so a
hand-written query goes through `user(login:"starquake")`, never
`organization(…)`. Every command below takes `R=starquake/topbanana`.

**Two kinds of state, and the name tells you which:**

- **Gate states** (`Your input`, `Your sign-off`, `Your review`): work *stops*.
  The maintainer decides. You may ask; you never answer for them.
- **Work states** (`Spec`, `Plan`, `Build`): work *proceeds*. The state is the
  instruction, and acting on it needs no further permission.

**If it says "your", it is a gate.**

**Do not re-ask permission on a work state, however big or risky it looks.**
A load-bearing decision inside a `Build` is made and **surfaced in the PR** for
review; the PR review plus `ready to merge` is the sign-off. Converting a work
state back into "shall I start?" re-invents a gate the maintainer already
opened.

**But a work state is not a promise that the ticket is workable.** When a
ticket lacks the information to do the work (an empty body, a symptom with no
reproduction), do every bit of investigation that doesn't need the answer, then
send it back to `Your input` with **specific** questions and your findings
attached.

**Never ship a fix on a reading-only diagnosis when the reproduction fails.**
Reading code tells you what CAN happen, not what DID. Publish the mechanism you
found and let the maintainer confirm the shape first. A flaky test that will
not reproduce gets its mechanism written up, not a speculative fix.

## The autonomy contract: do not cross

- **Never decide design direction.** Open questions go TO the maintainer
  (`Your input`).
- **Never add `ready to merge` or `changes requested`**, and never merge
  without `ready to merge` (CLAUDE.md, "Merging"). Re-read the label from the
  API at the moment you merge, never from a notification: a label can be added
  and withdrawn within a minute. The same goes for CI: green before a push is
  not green after it.
- **Dependabot PRs**: `.github/workflows/dependabot-auto-merge.yml` lands patch
  and minor bumps by itself. Majors, `/tools` direct requires and GitHub
  Actions bumps wait for the maintainer's `ready to merge` like any PR. Your
  part is keeping them mergeable (see the table).
- **Build only what's authorised**: a card the maintainer moved to `Build`, a
  `go` / `approved` comment on a `Your sign-off` ticket, or a **bug** or
  **tweak** small enough to need no plan. Never build a `Your input` or
  `Your sign-off` ticket.
- **Skip anything labelled `hold`** entirely: no comment, no build, no merge.
- **Every issue, comment and PR description you post ends with the
  attribution trailer** `_— Claude Code, for @starquake_` (CLAUDE.md,
  "Attribution"), posted with `--body-file`. The monitor tells your comments
  from the maintainer's by that trailer, so an unmarked comment reads as their
  answer. Commits carry none.
- **Never bring a branch forward on the server.** No merging `main` into a
  branch, no `gh pr update-branch`, no "Update branch" button: those make
  unsigned commits, which the ruleset refuses. Rebase locally and
  `--force-with-lease` (`merge-pr`, Step 4). Never `git stash`: stashes are
  shared across worktrees.
- **At most ONE build per pass** (see the cap).
- **Sweep for closed issues not in `Done`.** `Item closed → Done` is a GitHub
  automation, and when it breaks nothing announces it:

  ```bash
  gh api graphql --paginate -f query='query($endCursor: String) { user(login:"starquake"){ projectV2(number:7){
    items(first:100, after:$endCursor){ pageInfo{ hasNextPage endCursor } nodes{
    content{ ... on Issue { number state } }
    fieldValueByName(name:"Status"){ ... on ProjectV2ItemFieldSingleSelectValue { name } }
  }}}}}' --jq '.data.user.projectV2.items.nodes[]
    | select(.content.number != null) | select(.content.state=="CLOSED")
    | select(.fieldValueByName.name != "Done") | .content.number'
  ```

  Move what it returns to `Done`. If it returns anything, say so: an automation
  is off, and fixing it is the maintainer's job (Project → ⋯ → Workflows).

## The pass

**A red `main` comes before all of it.** After any merge, yours, the
maintainer's or Dependabot's, confirm `main` still builds and deployed to
staging:

```bash
gh run list -R "$R" --branch main --limit 3 --json workflowName,conclusion,headSha
```

The ruleset is strict, so two PRs cannot land on each other's stale base.
`main` can still go red from what no PR's checks ran: a job that runs only on
`main` (the staging deploy among them), a flake, or a dependency that moved
underneath. Nothing else notices this.

**Fixing a red `main` is authorised work, not a question.** The change that
broke it is known, the fix is not a design decision, and every branch cut
while it is red inherits the breakage. Fix it in a PR, say what it was, and
carry on. A flake that turned `main` red gets an issue (CLAUDE.md, "Testing").

**Standing authorised work goes first.** Read every work lane (`Build`, then
`Plan`, then `Spec`) and work from there. Work does not earn priority by being
new; the only pre-emptions are a red `main`, a PR carrying `ready to merge` or
`changes requested`, and a direct maintainer request.

0. **Reconcile before anything else.** Never trust what you remember, or what
   a summary says, about whether a pull request is still open.

   ```bash
   gh pr list -R "$R" --state merged --limit 10 --json number,title,mergedAt,mergeCommit
   git fetch origin --quiet && git log --oneline -5 origin/main
   ```

   For anything merged since the last pass: check `main` is still good (above),
   close out its ticket and card, and rebase any open branch that was cut
   before it (the strict ruleset refuses it otherwise).

1. **Enumerate.** `gh issue list --state open`, `gh pr list --state open`, and
   a recently-closed sweep for comments that landed after close. Drop anything
   labelled `hold`. **Re-read state from the API**, not from memory.

   **Read the comments, not just the states.** For every ticket at a gate,
   fetch its comments and look for the maintainer's answer block or go signal
   *after your last comment*. Scope that read by your last comment, never by a
   wall-clock window. An answer is a @starquake comment WITHOUT the
   `— Claude Code, for @starquake` trailer.

   **Read a ticket before answering anything about it**, including your own
   earlier comments. After a context break especially: a summary carries
   decisions, not what was already posted, so re-deriving and re-posting is the
   failure. If duplication already happened, own it in a NEW comment; never
   edit or delete the old one.
2. **Classify and act**, with **merges before builds** (work built before a
   pending merge lands on a stale base):

   1. cheap advancement (replies, specs, plans, reminders)
   2. every PR carrying `ready to merge`: merge them all (`merge-pr`), then
      re-pull; every PR carrying `changes requested`: rework it
   3. then the pass's one build, branched off the freshly merged `main`

   Hand each item to the skill that owns that step:

| State | Action | Owner |
|---|---|---|
| unanswered comment (issue/PR) | reply: factual auto-post, substantive draft for OK | `issue-comment-replies` |
| new issue, in no lane yet | add it to `Backlog` (it is the maintainer's to rank). A flaky-test ticket you filed yourself goes to `Backlog` too, with its mechanism in the body | `board.sh` |
| **bug** in `Build` | reproduce → root-cause → **green PR** | debug → PR |
| `Spec` | **choose the route first** (see Routing): a bug or tweak needing no plan → comment the route → `Build`; no design question left → `Plan`; otherwise draft the spec + its open questions → `Your input` | `design-slice` |
| `Your input` | **stop**, UNLESS a new maintainer comment answers the block → write the answers into the body → nothing left open → `Plan` | `design-slice` |
| `Plan` | write the plan → `Your sign-off`. A plan already in the body? Check it against the code, say in the Next-steps comment what does not fit, and move on to `Your sign-off` | `design-slice` |
| `Your sign-off` | **stop**, UNLESS the maintainer signalled go (a `go`/`approved` comment, OR moved it to `Build`) → build | Build row |
| `Build` | build the slice per CLAUDE.md ("Branches", "Self-review"): delegate to `backend-dev` / `frontend-dev`, draft PR with `Closes #N`, run the full self-review, mark it ready → `Your review`. No plan in the body? Write it there first and build it: the move was the go | dev agents, `review-diff` |
| `Your review` | **stop**: only `ready to merge` moves it. Do keep the PR mergeable: CI green, rebased locally if behind, and no thread left open that Claude has acted on (the ruleset refuses to merge with one) | `merge-pr` |
| reply to one of Claude's review comments | `fix`: fix, push, reply with the commit, **resolve the thread**; `skip`: acknowledge, resolve; `ticket`: file a `Backlog` issue, reply with the link, resolve; anything else: answer in the thread | `review-diff` |
| PR carrying `changes requested` | read its review comments, fix, push, remove `changes requested` (CLAUDE.md, "Merging") | rework |
| PR carrying `ready to merge` | **merge it** (label + green CI + threads + title + squash) | `merge-pr` |
| Dependabot PR (no issue, no card) | **never merged by hand without `ready to merge`**: patch/minor bumps land through the auto-merge workflow. Behind `main`: rebase it locally and `--force-with-lease`, never `update-branch`. Red only because a committed generated file drifted: regenerate it on the branch, commit, push, watch CI. Red on anything else: comment what broke on the PR | rework |

3. **Post a Next-steps comment on every ticket whose state you moved** (below).
4. **The build cap: at most one build per pass, and ZERO is a valid pass.**
   Replies, advancement, reworks and merges are unlimited; do only the single
   highest-priority build (an approved slice, else a bug fix, else a tweak).
   If nothing is authorised, build nothing and say so. Never manufacture work
   to fill the slot.
5. **A ticket you FILE gets the same treatment as one you find, in the same
   action**: a Status naming whose turn it is, the routing reason in the body,
   and a Next-steps comment. A filed ticket with no next step is close to not
   having been filed.

## Routing a ticket

Every ticket you route states its route, with the reason:

```
Routing: straight to Build — a tweak, no unexamined assumption.
Routing: Spec first — the join screen's wording changes what every player sees.
```

The maintainer disagrees by dragging the card elsewhere, and that is the
override. **Never add a Status option to carry a property**: reordering a
single-select replaces every option and clears every card's value. A property
of a ticket is a label (`hold`, `bug`, `security`).

**A card the maintainer drags to `Spec` means "your call, go"**. The first
step there is choosing the route, not writing a spec: comment the route with
its reason, and move a bug or tweak straight on to `Build` (and build it,
within the one-build cap), or a ticket with no design question left on to
`Plan`. Only a real design question gets a spec.

## Which backlog ticket is next

**The order of the `Backlog` column is the priority**, top first; the
maintainer ranks it by dragging. A pass never pulls from `Backlog` on its own:
a card leaves it only when the maintainer moves it, or asks in chat or a
comment to "pick up the next one", which means the **top** card. Every board
query pages (`--paginate`): a single page stops at 100 items and silently
misses the rest.

```bash
gh api graphql --paginate -f query='query($endCursor: String) { user(login:"starquake"){ projectV2(number:7){
  items(first:100, after:$endCursor, orderBy:{field:POSITION, direction:ASC}){ pageInfo{ hasNextPage endCursor } nodes{
    content{ ... on Issue { number title } }
    fieldValueByName(name:"Status"){ ... on ProjectV2ItemFieldSingleSelectValue { name } } } } } } }' \
  --jq '.data.user.projectV2.items.nodes[] | select(.fieldValueByName.name=="Backlog") | "\(.content.number) \(.content.title)"' | head -1
```

Picking it up moves it to `Spec`, where the route is decided as above. Post a
Next-steps comment saying it was picked up and by whose request.

## The Next-steps comment: a new one whenever the state moves

Every open ticket off `Backlog` carries a comment headed `**Next steps**`,
stating where the ticket is and what moves it, ending with the trailer.

**Post a NEW comment each time the state changes; never edit the previous
one.** The thread is the ticket's history. Nothing changed? Post nothing.
Write it as a reply ("folded your answers in; the plan's in the body; one thing
I didn't decide for you"), not a dashboard. Keep it short (CLAUDE.md, "User-facing copy").

**Posting an answer block IS a `Your input` move.** Set the state in the same
step, never later.

Content by state (a state line, then "Next:" naming the action and who takes
it):

- `Your input`: *Next: paste the answer block back filled in (or answer in
  chat and I'll write it back).* **Always include the block**: fenced, headed
  `# keep your pick, delete the rest`, one line per question with its options
  and a `(rec)`, then `notes =`.
- `Your sign-off`: *Next: drag the card to `Build` or comment `go`; a pass
  builds it into a PR.*
- `Spec` / `Plan`: *Next: Claude drafts it and hands back; nothing needed from
  you.*
- `Build`: *Next: Claude builds a PR; then add `ready to merge` once you've
  reviewed it.*
- `Your review`: *Next: review the PR and add `ready to merge` (or
  `changes requested`); a pass acts on it.*

## Reporting

End the pass with: **what moved** (and to what state), **what you built,
reworked or merged** (and what Dependabot merged), and above all **what's now
waiting on the maintainer** (the gate queue), with PR URLs. Report what you
chose not to do as plainly as what you did: a build declined for lack of
authorisation, a decision refused, a flake you couldn't reproduce. In a loop,
this becomes a push notification only when something needs them.

## Loop form

"work the board every `<interval>`" / "…in a loop" runs this same pass on a
schedule (`/board` is the short form). Same contract, same one-build cap.

### Prefer a Monitor over polling

A persistent Monitor runs the poll in the shell, outside the agent's context,
and only a printed line wakes the agent: quiet minutes cost nothing. Arm it at
the start of a loop session (check it isn't already running first). What it
watches is derived from **what the pass acts on**:

| signal | why the pass cares |
|---|---|
| issue/PR conversation comments without the Claude trailer | the maintainer's answers and go signals |
| PR review comments (a different endpoint) | inline diff feedback, `fix`/`skip`/`ticket` replies |
| `ready to merge` added | the only merge authorisation |
| `changes requested` added | rework authorisation |
| `hold` added / lifted | an override that stops or releases work |
| board Status transitions | a move to `Build` authorises a build |
| `main` going red | pre-empts everything |
| `main`'s head moving | something merged (Dependabot's included), and nobody is going to say so |
| standing work lanes (level-triggered) | authorised work that is merely waiting |

```bash
# pipefail is LOAD-BEARING: every snapshot ends in `| sort`, and without it a
# failed `gh` returns 0 with no output, so every guard silently passes.
set -o pipefail
R=starquake/topbanana
MARK='Claude Code, for @starquake'
since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
SELF="${BOARD_SELF_SET_FILE:-${TMPDIR:-/tmp}/topbanana-board-selfset}"
GQ='query($endCursor: String) { user(login:"starquake"){ projectV2(number:7){
  items(first:100, after:$endCursor){ pageInfo{ hasNextPage endCursor } nodes{
  content{ ... on Issue { number } }
  fieldValueByName(name:"Status"){ ... on ProjectV2ItemFieldSingleSelectValue { name } }
}}}}}'
# No `|| true` on a snapshot: a failed call must FAIL so the diff is skipped.
snap_board(){ gh api graphql --paginate -f query="$GQ" --jq '.data.user.projectV2.items.nodes[]
  | select(.content.number != null) | "\(.content.number)|\(.fieldValueByName.name // "none")"' 2>/dev/null | sort -n; }
snap_label(){ gh pr list -R $R --state open --label "$1" --json number -q '.[].number' 2>/dev/null | sort; }
snap_hold(){ gh issue list -R $R --state open --label hold --json number -q '.[].number' 2>/dev/null | sort; }
snap_main(){ gh run list -R $R --branch main --workflow ci.yml --limit 1 \
  --json conclusion -q '.[0].conclusion // "none"' 2>/dev/null || echo none; }
snap_head(){ gh api repos/$R/commits/main --jq '.sha' 2>/dev/null; }

until prev_s=$(snap_board); do sleep 30; done
prev_l=$(snap_label "ready to merge"); prev_c=$(snap_label "changes requested")
prev_h=$(snap_hold); prev_main=$(snap_main); prev_head=$(snap_head); ticks=0
while true; do
  sleep 60
  now=$(date -u +%Y-%m-%dT%H:%M:%SZ)

  gh api "repos/$R/issues/comments?since=$since&per_page=30" \
    --jq ".[] | select(.user.type != \"Bot\") | select((.body | contains(\"$MARK\")) | not)
          | \"COMMENT on #\(.issue_url | split(\"/\") | last): \(.body | gsub(\"\n\"; \" \") | .[0:120])\"" 2>/dev/null || true
  gh api "repos/$R/pulls/comments?since=$since&per_page=30" \
    --jq ".[] | select(.user.type != \"Bot\") | select((.body | contains(\"$MARK\")) | not)
          | \"REVIEW COMMENT on #\(.pull_request_url | split(\"/\") | last) \(.path): \(.body | gsub(\"\n\"; \" \") | .[0:100])\"" 2>/dev/null || true

  # A set that was non-empty and is now empty is an outage until proven otherwise.
  if cur=$(snap_label "ready to merge") && { [ -n "$cur" ] || [ -z "$prev_l" ]; }; then
    comm -13 <(echo "$prev_l") <(echo "$cur") | grep -E '^[0-9]+$' | sed 's/^/READY TO MERGE: PR #/' || true
    prev_l=$cur
  fi
  if cur=$(snap_label "changes requested") && { [ -n "$cur" ] || [ -z "$prev_c" ]; }; then
    comm -13 <(echo "$prev_c") <(echo "$cur") | grep -E '^[0-9]+$' | sed 's/^/CHANGES REQUESTED: PR #/' || true
    prev_c=$cur
  fi
  if cur=$(snap_hold) && { [ -n "$cur" ] || [ -z "$prev_h" ]; }; then
    comm -13 <(echo "$prev_h") <(echo "$cur") | grep -E '^[0-9]+$' | sed 's/^/HOLD ADDED: #/' || true
    comm -23 <(echo "$prev_h") <(echo "$cur") | grep -E '^[0-9]+$' | sed 's/^/HOLD LIFTED: #/' || true
    prev_h=$cur
  fi
  cur=$(snap_main)
  [ "$cur" != "$prev_main" ] && [ "$cur" = "failure" ] && echo "MAIN IS RED: CI failed on main"
  prev_main=$cur

  if cur=$(snap_head) && [ -n "$cur" ]; then
    [ "$cur" != "$prev_head" ] && echo "MAIN MOVED to ${cur:0:8}: something merged, reconcile and check it builds"
    prev_head=$cur
  fi

  # Board Status, every transition. Moves board.sh made are consumed and skipped.
  if ! cur_s=$(snap_board) || { [ -z "$cur_s" ] && [ -n "$prev_s" ]; }; then since=$now; continue; fi
  printf '%s\n' "$cur_s" | while IFS='|' read -r n st; do
    [ -z "$n" ] && continue
    was=$(printf '%s\n' "$prev_s" | awk -F'|' -v k="$n" '$1==k{print $2}')
    [ "$was" = "$st" ] && continue
    if [ -f "$SELF" ] && grep -qxF "$n|$st" "$SELF"; then
      grep -vxF "$n|$st" "$SELF" > "$SELF.tmp" || true; mv "$SELF.tmp" "$SELF"; continue
    fi
    if [ -z "$was" ]; then echo "BOARD: #$n added as '$st'"; else echo "BOARD: #$n moved $was -> $st"; fi
  done
  prev_s=$cur_s

  # Level-triggered heartbeat: edge signals can't see work that is only waiting.
  ticks=$((ticks + 1))
  if [ $((ticks % 30)) -eq 0 ]; then
    waiting=""
    for lane in Build Plan Spec; do
      ids=$(printf '%s\n' "$cur_s" | awk -F'|' -v L="$lane" '$2==L{printf "#%s ", $1}')
      [ -n "$ids" ] && waiting="$waiting$lane: $ids"
    done
    [ -n "$waiting" ] && echo "STANDING work, outranks anything newly arrived — $waiting"
  fi
  since=$now
done
```

Why it's shaped like this:

- **Use the targeted GraphQL query, never `gh project item-list`, in a loop.**
  The targeted query costs 1 point; `item-list` costs about 100, and at a 60 s
  poll that exceeds the 5,000-points-an-hour GraphQL budget.
- **A failed poll is not an empty board.** Keep `|| true` on output that is
  emitted, and drop it from snapshots that are compared.
- **Never report your own board writes.** `board.sh state` records each write
  in `$BOARD_SELF_SET_FILE`, and the monitor consumes one matching entry per
  transition. Consuming, rather than just matching, keeps a later genuine move
  to the same state visible.

With a Monitor armed, the scheduled tick is insurance only: about an hour.

### Pacing without a Monitor

In dynamic `/loop` mode, pick the next delay from what this pass found:

| This pass… | Next delay |
|---|---|
| merged, built, or is watching CI it pushed | ~5 min |
| folded in answers, replied, advanced a ticket | ~10 min |
| found new maintainer comments but nothing to do yet | ~15 min |
| found nothing (1st, 2nd, 3rd+ quiet pass) | ~20, ~40, 60 min |

Any activity resets to the top. After three quiet passes in a row, say so and
offer to stop.
