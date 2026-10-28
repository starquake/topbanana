---
name: design-slice
description: >
  Use whenever a topbanana ticket needs its spec or plan: "let's design X",
  "spec out #NN", "pick up #NN" when #NN has no approved plan yet, or a ticket
  sitting in `Spec` or `Plan` on the board. Runs the design-first workflow: the
  spec and plan are written IN the GitHub issue, decisions are settled with the
  maintainer there through a copy-paste answer block, and building only starts
  after the maintainer's explicit OK. Trigger even if the user doesn't say
  "skill".
---

You design a change **in its GitHub issue**: the issue is the single design
record, and the maintainer may never read chat. The one rule that matters
most: **never auto-proceed from plan to implementation.** The maintainer's OK
(the card dragged to `Build`, or a `go` / `approved` comment) is the build
signal, and it must be explicit.

Move the card at each pause with `.claude/scripts/board.sh state <n> "<state>"`.
`Spec` and `Plan` are yours; `Your input` and `Your sign-off` are the
maintainer's. Every issue body and comment you post ends with the trailer
`_— Claude Code, for @starquake_` and goes through `--body-file`.

## Step 1: the issue

- **First, choose the route.** A card in `Spec` means "your call, go", not
  "write a spec": decide how much process it needs, and say so on the ticket
  with the reason.
  - **A bug or tweak** small enough to need no plan: comment *"Routing: straight
    to Build — …"*, move it to `Build`, and build it (`work-the-board`, the
    `Build` row). No spec.
  - **No design question left, but work that wants a plan**: move it to
    `Plan` and write the plan. No spec.
  - **A small decision**: a short spec with its answer block; the plan is a few
    lines in the same body once it's answered.
  - **A feature**: the full route below.

  The maintainer overrides by dragging the card back.
- **No issue yet?** Create one with `gh issue create --body-file`, in the
  sections below, and put it on the board.
- **The issue exists but is free-form?** Restructure its body into the
  sections below, keeping everything true that's already there.
- **The BODY is the living spec; the COMMENTS are the history.** Opposite
  editing rules:
  - The **body** is always current: rewrite it freely. When a question is
    answered, MOVE it into *Decisions* and DELETE it from *Open questions*. A
    body that still asks a settled question is the bug.
  - The **comments** are append-only: never edit one; post a new
    `**Next steps**` comment each time the state changes.
- Read the code before writing: name real symbols (the package under
  `internal/`, the query in `internal/queries/`, the template under
  `internal/web/tmpl` or the module under `frontend/`), not "the backend" or
  "the client".
- Set `Spec` while you're writing it, so the board shows it's your turn.

## Step 2: the spec

Sections: *Goal*, *Decisions*, *Open questions*, *Plan*.

- **Goal**: what ships, plus the one-line reason.
- **Decisions**: numbered, each with its why. Anything unsettled is a question
  TO the maintainer. Never decide design direction yourself.
- **Open questions end with a copy-paste answer block.** One line per
  question, the options inline, and **every line carrying a `(rec)`**:

  ~~~
  ```
  # keep your pick, delete the rest
  Q1 a late joiner sees: the current question (rec) / the next question
  Q2 the leaderboard shows: per-session scores (rec) / all-time scores
  notes =
  ```
  ~~~

  The recommendation belongs **in the block**, not in prose under it. No view?
  Write `discuss (rec)` on that line. Before posting, re-read the prose against
  the block: **a question missing from the block is effectively not asked.**

  Mirror the block in the Next-steps comment, so it can be answered from the
  thread.
- **Answers in chat are allowed; leaving them there is not.** When the
  maintainer answers conversationally, write the answers into the BODY
  (*Decisions*, dated, with a verbatim quote wherever the wording carries
  reasoning) and post a Next-steps comment saying so **before acting on
  them**. A fix to a missing or wrong block is a NEW comment, never an edit.
- **Hand off**: posting the block moves the ticket to `Your input` in the same
  step. Nothing open? Skip straight to the plan.

## Step 3: settle, then plan

Fill the plan only once the decisions are settled. Set `Plan` while you write
it. A plan already in the body is checked against the code rather than
rewritten. Tasks go in landing order, each one a green commit (`make check`);
name the test layer for each (unit, integration, e2e — CLAUDE.md, "Testing")
and any migration. The last task updates `README.md` / `CLAUDE.md` if anything
they say changed, and adds or updates the package's `CLAUDE.md` for a decision
that outlives the ticket (CLAUDE.md, "Design notes").

## Step 4: STOP

Set `Your sign-off`, post a Next-steps comment (*Next: drag the card to `Build`
or comment `go`*), and **end there**. The build starts only on the
maintainer's go.
