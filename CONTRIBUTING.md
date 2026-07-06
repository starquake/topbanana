# Contributing

How AI is used on this project (the ownership rule for any change, how it applies
to pull requests and issues, and how the maintainer works) lives in
[`AI_POLICY.md`](AI_POLICY.md). Read it first: it sets the bar every contribution
is held to.

## Issues

Describe the problem in your own words: what is broken or missing, and why it
matters. Short and specific beats long.

## Practical notes

- [`docs/development.md`](docs/development.md) covers building from source, the
  project layout, the dev server, and running the test suite.
- `CLAUDE.md` is the source of truth for how the code is built, tested, and
  structured. Read it before a non-trivial change.
- Run `make check` (and `make test-e2e` for UI changes) before marking a PR
  ready.
- Keep each PR focused on one change.
