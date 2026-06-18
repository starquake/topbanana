# Contributing

## The rule that matters: own what you submit

What makes a change acceptable here is not *how* it was written -- it is that
**a human has read every line and stands behind it.** Use whatever tools you
like, AI included; the bar is ownership, not authorship.

That maps onto one mechanic:

- **A pull request marked "Ready for review" is your assertion: I have read
  every line of this change and I stand behind it as my own work.** Only ready
  PRs are reviewed for merge.
- **Draft pull requests are welcome** for anything that is not there yet --
  AI-assisted first passes, experiments, work in progress, or "is this
  direction worth it?". A draft is a conversation, not a submission; it will not
  be merged, and that is fine.

Do not mark a PR ready until you have reviewed it as if you had typed it by hand
-- because as far as the project is concerned, you did.

## Issues

Describe the problem in your own words: what is broken or missing, and why it
matters. Short and specific beats long.

Issues that are clearly unreviewed AI output -- generic, padded, or inaccurate
about the project -- will be closed. Not because a tool was used, but because no
human stood behind them.

## How the maintainer works (and why your bar is the same)

For transparency: I use AI heavily -- to draft code, open issues, and write the
first version of pull requests. The difference from a contributor is *when* the
human review happens, not whether it happens. I read every line before it
merges, and by merging I take ownership of it; nothing lands that I have not
vouched for.

So the standard is the same for everyone -- a human owns every merged line. Only
the sequencing differs: on my own PRs I review at merge time; you review before
you mark yours ready.

**This workflow is still a work in progress.** How I use AI, where the review
happens, and what I ask of contributors are all things I am still working out as
the project grows, so this document will change. If something here is unclear or
seems wrong, open an issue and say so.

## Practical notes

- `CLAUDE.md` is the source of truth for how the code is built, tested, and
  structured. Read it before a non-trivial change.
- Run `make check` (and `make test-e2e` for UI changes) before marking a PR
  ready.
- Keep each PR focused on one change.
