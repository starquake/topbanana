---
name: issue-comment-replies
description: >
  Use whenever the user wants to catch up on or respond to topbanana's GitHub
  issue or PR comments: "check the issues for new comments", "any comments to
  reply to?", "catch up on the tickets", or a board pass finding an unanswered
  comment. Scans open and recently closed issues and PRs for comments not yet
  answered, decides which merit a reply, AUTO-POSTS low-risk factual replies
  and DRAFTS substantive ones for the maintainer's OK. Every posted comment
  ends with the Claude Code attribution trailer. Trigger even if the user
  doesn't say "skill".
---

You triage new comments on this repo's issues and PRs and answer the ones
that merit it. Comments post under the maintainer's own account (@starquake)
through `gh`, so **every** reply ends with the attribution trailer, and a
public comment under someone else's name is hard to take back. Split replies
into *auto-post* (safe, factual) and *draft for approval* (substantive,
contestable). When unsure, it's a draft.

## The attribution trailer (never omit it)

Every comment you post ends with this exact line (CLAUDE.md, "Attribution"):

```
_— Claude Code, for @starquake_
```

Write the comment to a file and post with `gh issue comment <n> --body-file
<file>` (or `gh pr comment`).

## Step 1: find unanswered comments

Both the maintainer and Claude post as @starquake, so authorship can't tell
them apart; **the trailer can**. A maintainer comment is a @starquake comment
WITHOUT `— Claude Code, for @starquake`.

1. List candidate threads (open, and touched in the last ~2 weeks):
   `gh issue list -R starquake/topbanana --state all --limit 40 --json number,title,updatedAt,state`
   and the same for `gh pr list`.
2. Pull each thread: `gh issue view <n> --json title,body,comments`, plus PR
   review comments (`gh api repos/starquake/topbanana/pulls/<n>/comments`).
3. **Unanswered** means a human comment with no Claude-trailed comment after
   it. Answer blocks and go signals on gated tickets belong to
   `work-the-board` / `design-slice`: fold those in there, don't just reply.
   `fix` / `skip` / `ticket` replies on review threads belong to `review-diff`.
4. Skip bot noise (Dependabot's included), and threads where a Claude reply
   already came last.

Report what you found before replying: "thread → unanswered comment".

## Step 2: which merit a reply

Reply to a direct question, an @-mention, or a technical claim where a
code-grounded answer helps. Don't reply to acknowledgements ("thanks", "👍"),
restatements of recorded decisions, or anything only a specific person can
answer. Silence is a fine outcome.

## Step 3: auto-post or draft

- **Auto-post**: factual and grounded. What the code does, where something
  lives, "tracked in #NN", a status, a clarifying question back.
- **Draft for approval**: opinion, pushback, anything that could read as a
  **decision** (decisions are the maintainer's), anything security-sensitive,
  or anything you're not sure of. Show each draft with its thread, and post
  only the approved ones.

## Step 4: write it

Read the relevant code first, and never answer from memory. The answer goes in
the **first line**; reasoning goes below, kept short. Link the file, issue or
PR you cite.

## Guardrails

- The trailer goes on every posted comment, with no exceptions.
- Never post a design decision, only grounded observations.
- Never write in the maintainer's own voice.
- Don't double-reply. Prefer a few high-value replies over covering every
  thread.
