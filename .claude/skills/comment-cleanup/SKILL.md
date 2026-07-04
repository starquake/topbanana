---
name: comment-cleanup
description: >
  Reviews and rewrites the code comments in the current branch's diff, applying
  the nine StackOverflow "Best practices for writing code comments" rules plus
  this repo's CLAUDE.md Comments rules. Invoke it right after a backend-dev or
  frontend-dev agent reports its code is done, as part of self-review before
  opening or updating a PR, or any time comments look verbose, redundant,
  duplicative of the code, or stale. It edits comments in place (never logic)
  and leaves `make check` green. Use it whenever new or heavily-changed code has
  landed on a branch, even if nobody explicitly says "clean up the comments".
---

You are doing a comment-only cleanup pass over the code on the current branch.
Dev agents (and humans in a hurry) tend to over-comment: they narrate what the
code does, restate the plan, and write multi-sentence rationale where one line
would do. Your job is to leave every comment either **short and load-bearing**
or **gone**, without touching behaviour.

The guiding idea, from Jeff Atwood: *code tells you how, comments tell you why.*
A comment earns its place only when the *why* is non-obvious. If the code already
makes it clear, the comment is noise and should be deleted.

## How to run

1. Get the diff for the branch: `git diff main...HEAD` (or the range under review).
   Only touch comments this branch **added or changed** -- do not sweep the whole
   repo.
2. For each changed file, read enough surrounding code (Read tool) to judge
   whether each comment is pulling its weight. Comments live in Go (`//`, `/* */`),
   SQL (`--`), gohtml templates (`{{/* */}}`), and client JS (`//`, `/* */`).
3. Edit comments in place: shorten, merge, or delete. When in doubt, delete.
4. Verify: `make check` must still pass (build + tests + js-check + tailwind-check
   + lint). See Constraints for why this should stay green without rebuilding.

## The nine rules (StackOverflow)

Apply these as your rubric. The source is
https://stackoverflow.blog/2021/12/23/best-practices-for-writing-code-comments/

1. **Comments should not duplicate the code.** Delete anything that just restates
   what the next line plainly does (`// increment i`, `// loop over rows`,
   `// set published to true`). This is the most common thing to cut.
2. **Good comments do not excuse unclear code.** If a comment is propping up a
   confusing name or shape, prefer fixing the *name* over keeping the comment --
   but stay within scope: if the rename is risky, leave the code and flag it
   rather than silently rewriting logic in a comment-only pass.
3. **If you can't write a clear comment, there may be a problem with the code.**
   A comment you can't state in one clear sentence is a smell. Trim it to the one
   real point; note (don't fix) the underlying code smell if one surfaces.
4. **Comments should dispel confusion, not cause it.** Remove comments that are
   vague, hedging, or now contradict the code. A wrong comment is worse than none.
5. **Explain unidiomatic code in comments.** KEEP (and keep concise) the comments
   that explain a deliberately non-obvious choice -- a workaround, a defer/ordering
   requirement, a SQLite quirk -- so a future reader doesn't "simplify" it back
   into a bug.
6. **Provide links to the original source of copied code.** Keep such links.
7. **Include links to external references where they help.** Keep load-bearing
   references (issue links, spec URLs) at the point they're needed.
8. **Add comments when fixing bugs.** KEEP the one-line "why" on a bug workaround,
   ideally with the issue number (`// see #165`). Don't delete these as "obvious".
9. **Use comments to mark incomplete implementations.** Keep `TODO`/`FIXME`
   markers; don't invent new ones.

## Project overlay (CLAUDE.md)

This repo's rules are stricter than the general advice above -- when they conflict,
these win. Read `CLAUDE.md` sections "Comments" and "Comments that reach across
files" for the full text; the load-bearing points:

- **Keep every comment to one short line.** A real *why* fits in one sentence.
  Multi-sentence rationale, step-by-step narration, and the same reason repeated
  on adjacent lines or sibling declarations all get cut to a single line or
  removed. Be aggressive.
- **Delete task/PR-scoped narration.** `// added for #1192`, `// a frontend agent
  will wire this`, `// used by X` -- this belongs in the PR description and rots
  in the code. An issue *reference* that stays accurate (`// see #165`) is fine;
  the narration around it is not.
- **Rewrite cross-file "why" as a self-contained local why.** A comment that
  explains what the *other* side of the system expects goes stale silently. Recast
  it to explain the local rationale only (e.g. not "the frontend gates X on this"
  but "defaults false so callers can tell auto-generated from claimed").
- **Exported Go doc comments stay in doc form.** Shorten them, but keep the
  leading symbol name and a complete sentence (`// HandleFoo does X.`).

## Constraints

- **Comments only.** Do not change logic, control flow, identifiers, SQL, string
  literals, error messages, test assertions, or user-facing copy. This is a
  low-risk pass precisely because behaviour is untouched -- keep it that way.
- **ASCII only in `.go` and `.sql`.** No em dash, en dash, or smart quotes in Go
  or SQL comments (a Unicode em dash in a SQL comment breaks sqlc). `make lint-ascii`
  catches this; so does `make check`.
- **Don't rebuild bundles unless drift forces it.** Minification strips JS/CSS
  comments and gohtml `{{/* */}}` never reaches output, so trimming source
  comments should leave `js-check`/`tailwind-check` green with no rebuild. If
  `make check` reports bundle drift anyway, run `make js` / `make tailwind` and
  include the regenerated artifact.
- **Stage explicitly.** `git add` the changed files by path; commit one clean
  slice, subject `Trim verbose comments to concise whys` (or similar), no body,
  no attribution trailer.

## Output

After the pass, report briefly: how many files and roughly how many comments you
trimmed or removed, which comments you deliberately KEPT and why (the load-bearing
whys / bug refs / unidiomatic-code notes), and the `make check` result. Keep the
report itself concise -- practice what the skill preaches.
