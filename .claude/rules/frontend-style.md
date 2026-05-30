---
description: Frontend style rules for the topbanana client and web pages
globs:
  - "internal/client/**"
  - "internal/web/**"
---

## Styling: Tailwind CSS v4

Styling uses **Tailwind CSS v4**, configured CSS-first (there is no `tailwind.config.js`). The source is `internal/web/static/css/_tailwind.css`; the built, minified output `internal/web/static/css/app.css` is committed and served at `/assets/css/app.css` for both the web pages (`internal/web`) and the game client (`internal/client`).

### Build

- `make tailwind` builds `app.css` from `_tailwind.css` (with `--minify`). The output is committed to the repo.
- `make tailwind-watch` rebuilds on change during development.
- `make tailwind-check` fails when `app.css` is stale relative to its source and the scanned templates; it runs as part of `make check`. **Rebuild and commit `app.css` whenever you add or change classes** or CI will flag drift.

### Where things live (`_tailwind.css`)

- `@import "tailwindcss/..."` pulls in the preflight, theme, and utilities layers.
- `@source` lines list the directories Tailwind scans for class names (the admin/auth/home templates, web JS, and the client static tree). A new template directory must be added here or its classes get purged from the build.
- `@theme` defines the design tokens as CSS variables -- colours (`--color-bg`, `--color-surface`, `--color-accent`, `--color-cyan`, `--color-danger`, ...) and fonts (`--font-sans` Inter, `--font-display` Orbitron, `--font-mono`). Each token emits matching utilities, so `--color-accent` gives `bg-accent`, `text-accent`, `border-accent`.
- `@layer base` / `@layer components` hold base element styles and reusable component classes assembled with `@apply`.

### Best practices

- **Put utilities in the markup**, not bespoke CSS. Prefer theme tokens (`bg-bg`, `text-text`, `border-accent`) over raw hex values.
- Use responsive (`sm:`, `lg:`) and state (`hover:`, `focus:`, `disabled:`) variants instead of hand-written media queries.
- Arbitrary values are fine when no token fits: `max-w-[720px]`, `shadow-[0_0_0_3px_var(--color-cyan-soft)]`.
- Promote a repeated utility cluster into an `@layer components` class with `@apply` only when it actually recurs across templates; one-offs stay inline.
- Add a new colour or size to `@theme` rather than hardcoding it in many places.
- Respect `@media (prefers-reduced-motion: reduce)` -- transitions and animations are zeroed there; do not reintroduce motion that ignores it.
- Use `min-h-dvh` (not `min-h-screen` / `100vh`) for full-height layouts so mobile URL-bar collapse does not create dead scroll (see #308).

## What to avoid

- **No bundler or npm for application JS.** App code is plain ES modules plus vendored libraries; the only build step in the project is the Tailwind standalone CLI for CSS.
- No TypeScript -- plain `.js` only.
- No third-party component library or JS framework beyond Alpine + anime.js.
- No inline styles (`style="..."`) -- use utilities, or a `@theme` token / `@layer` component class.
- No hand-written `.css` files. Styling changes go through `_tailwind.css` (tokens, `@layer`), never a new stylesheet.
- No reactive state added outside an Alpine component constructor (Alpine will not track it).
- No hardcoded base URLs -- all fetch calls use relative paths.
- No CDN-loaded libraries -- Alpine and anime.js are vendored and self-hosted (see #295); keep them that way.
