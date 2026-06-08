---
description: Frontend style rules for the topbanana client and web pages
globs:
  - "internal/client/**"
  - "internal/web/**"
---

## Styling: Tailwind CSS v4

**Where source vs built output lives.** All build-time frontend source -- the
esbuild entry points and their modules, plus the Tailwind source -- lives under
the top-level `frontend/` tree: `frontend/client/` (player client), `frontend/web/`
(admin/host JS + `css/tailwind.css`), and `frontend/shared/` (cross-tree modules).
The `internal/client/static` and `internal/web/static` trees hold **only served
output**: the committed esbuild bundles (`js/dist/*`), the committed Tailwind
output (`css/app.css`), vendored libs (`js/vendor/*`), raw-served scripts, HTML
shells, partials, fonts, and images. Because the static trees are served-only,
each is embedded with a plain `//go:embed static/*` and the source never ships in
the binary (#756).

Styling uses **Tailwind CSS v4**, configured CSS-first (there is no `tailwind.config.js`). The source is `frontend/web/css/tailwind.css`; the built, minified output `internal/web/static/css/app.css` is committed and served at `/assets/css/app.css` for both the web pages (`internal/web`) and the game client (`internal/client`).

### Build

- `make tailwind` builds `app.css` from `frontend/web/css/tailwind.css` (with `--minify`). The output is committed to the repo.
- `make tailwind-watch` rebuilds on change during development.
- `make tailwind-check` fails when `app.css` is stale relative to its source and the scanned templates; it runs as part of `make check`. **Rebuild and commit `app.css` whenever you add or change classes** or CI will flag drift.

### Where things live (`frontend/web/css/tailwind.css`)

- `@import "tailwindcss/..."` pulls in the preflight, theme, and utilities layers.
- `@source` lines list the **only** directories Tailwind scans for class names (the admin/auth/home/host templates, the client static tree's HTML shells + built bundles, and the `frontend/client` / `frontend/web` / `frontend/shared` source). Automatic detection is off (`source(none)` on the utilities import), so docs / tests / mockups never leak phantom utilities into `app.css` -- but a new template or JS directory must be added here or its classes get purged.
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

## Application JS: esbuild bundles

App JS is bundled per entry point with **esbuild**; the built, minified bundles are committed (like `app.css`). The source stays plain ES modules under `frontend/` (`frontend/client/` has `components/`, `services/`, `util/`). This reversed the old "no bundler / no npm for application JS" rule (#295), via #721.

- esbuild is a dev-only build tool declared in the root `package.json` + `package-lock.json` (separate from `test/e2e`'s Playwright deps). Nothing new ships at runtime.
- `make js` rebuilds both bundle trees; `make js-watch-client` / `make js-watch-web` rebuild each tree on change (one watcher per served `dist` dir); `make js-check` (wired into `make check`, like `tailwind-check`) fails when the committed bundles drift from the source. **Rebuild and commit the bundles whenever you change client JS** or CI flags drift.
- The committed bundles are embedded in the Go binary, so the distroless image needs no Node.
- Both trees are bundled: the player client (source `frontend/client/`, entries `app.js` / `join.js`, output `internal/client/static/js/dist/`) and the web/host tree (source `frontend/web/`, entries `host-lobby.js` / `share.js` plus the standalone admin/auth scripts `cooldown.js` / `copy-prompt.js` / `password-length.js` / `quiz-reorder.js`, output `internal/web/static/js/dist/`). Cross-tree shared modules live in `frontend/shared/` and are inlined into each tree's bundles via the `@shared/` import alias. The served `internal/*/static/js/` trees hold only built bundles (`dist/`) and vendored libs (`vendor/`, `htmx.min.js`) -- no un-minified source.
- Vendored libraries (Alpine, anime.js) stay separate `<script>` tags referenced via `window.*` globals; the bundle does not include them.

### Animation (anime.js v4)

All animation goes through the shared `runAnim` wrapper (`frontend/shared/anim.js`), which no-ops to the final state under `prefers-reduced-motion` or a missing global. anime.js is **v4**: a v3-style parameter is silently ignored and the animation just snaps to its end, so use the v4 names.

- `ease`, not v3 `easing`; ease names drop the prefix (`outQuad`, not `easeOutQuad`).
- `on`-prefixed callbacks: `onUpdate` / `onComplete` / `onBegin`, not `update` / `complete` / `begin`.
- Pass `onComplete` so `runAnim`'s reduced-motion skip path can land the final state.

## What to avoid

- **No bundler beyond esbuild, and no npm for runtime dependencies.** App JS is bundled with esbuild and source stays plain ES modules (see "Application JS: esbuild bundles" above); the build steps are the Tailwind CSS CLI and esbuild, both dev-only. Don't pull in a framework bundler, a runtime npm package, or a second JS toolchain.
- No TypeScript -- plain `.js` only.
- No third-party component library or JS framework beyond Alpine + anime.js. The one allowed exception is SortableJS (a focused drag-and-drop utility, vendored self-hosted at `internal/web/static/js/vendor/sortable.min.js`, admin-only), added deliberately for the rounds/questions reorder (#199). It is not a general framework license: reach for it only for drag-and-drop, not as a wedge to pull in more libraries.
- No inline styles (`style="..."`) -- use utilities, or a `@theme` token / `@layer` component class.
- No hand-written `.css` files. Styling changes go through `frontend/web/css/tailwind.css` (tokens, `@layer`), never a new stylesheet.
- No reactive state added outside an Alpine component constructor (Alpine will not track it).
- No hardcoded base URLs -- all fetch calls use relative paths.
- No CDN-loaded libraries -- Alpine and anime.js are vendored and self-hosted (see #295); keep them that way.
