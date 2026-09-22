---
name: review-diff
description: >
  Reads a pull request's whole branch back against main as a reviewer would,
  before the PR is marked ready for review. Catches what the build, lint and
  test gates cannot see: leftovers from earlier iterations, drift from the
  ticket's decisions, unhandled states and inputs, tests that assert the
  implementation instead of the decisions, and moved contracts. Fixes defects
  in their own commits and posts judgement calls as line comments for the
  maintainer to answer with fix / skip / ticket. Invoke as the Self-review step
  before clearing a PR's draft status.
---

## Review the whole diff

Before handing a PR over, read the change back as a reviewer would: the whole branch against main (`git fetch origin && git diff origin/main...HEAD`), not just the last commit. The build, lint and test gates prove it compiles and passes. They cannot see:

- Leftovers: code, names, comments and docs from an earlier iteration, or from a design that changed along the way. A lint catches an unused function; it does not catch a comment describing behaviour that is gone.
- The ticket: each settled decision, and any approved mockup, against what the code actually does.
- States and inputs: what every key, button or call does in every state of the thing built; what crosses between threads or processes; what a held input does when the screen or state changes under it.
- Tests that assert the decisions, not the current implementation.
- Anything the project treats as a contract (compatibility, fidelity, public API) that the change could move.

Sort each finding into one of two kinds:

- A defect has one right answer: a bug, leftover code or a stale comment, a name from an earlier iteration, a test that does not test what it says. Fix it without asking, in its own commit.
- A judgement call changes what the user sees or how it behaves, departs from the ticket's decisions or an approved mockup, widens or narrows the scope, or touches a contract. Do not fix it; ask. When unsure which kind a finding is, it is a judgement call.

Ask by posting a review comment on the line the finding is about:

    gh api repos/starquake/topbanana/pulls/<n>/comments -f commit_id="$(git rev-parse HEAD)" -f path=<file> -F line=<line> -f side=RIGHT -f body="$(cat finding.md)"

The comment says what is wrong, gives the recommended fix, then lists the three words the maintainer can reply with, and ends with the attribution trailer `_— Claude Code, for @starquake_`:

- fix: Claude fixes it as recommended (or as the reply amends), pushes, replies with the commit, and resolves the thread.
- skip: Claude leaves it, replies to acknowledge, and resolves the thread.
- ticket: Claude files it as a backlog issue, replies with the link, and resolves the thread.

`main`'s ruleset blocks merging while any review thread is unresolved, so each of the three ends by resolving the thread. There is no `gh` subcommand for it; look up the thread's node id and resolve it through GraphQL:

    gh api graphql -f query='{repository(owner:"starquake",name:"topbanana"){pullRequest(number:<n>){reviewThreads(first:100){nodes{id isResolved comments(first:1){nodes{databaseId}}}}}}}'
    gh api graphql -f query='mutation($id:ID!){resolveReviewThread(input:{threadId:$id}){thread{isResolved}}}' -f id=<thread node id>

Any other reply is a question or an extra comment: answer it in the thread, and act on it only when it asks for a change. A finding with nothing to anchor to (something missing) goes on the file's first changed line, saying so. Watch the PR's review comments for replies (poll `repos/starquake/topbanana/pulls/comments?since=<time>`), rather than waiting to be told.

Every reply Claude posts in a thread ends with the same trailer. Claude and the maintainer both post as `starquake`, so the author cannot tell them apart: a polled comment ending with the trailer is Claude's own and is skipped, and only a comment without it is a reply to act on. Without this, the poll picks up Claude's own findings and replies and loops on them.

The PR description gets a "Found in review" section: each defect fixed, with its commit, and how many judgement calls are waiting as comments, or that there were none of either.
