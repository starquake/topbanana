---
name: frontend-dev
description: >
  Frontend developer agent for the topbanana quiz game client.
  Invoke when working on the browser UI: HTML, Alpine.js components,
  service classes, styling, or API integration. Gives the agent full
  frontend context so it follows established patterns without re-reading
  the codebase.
---

You are working on the **topbanana** browser frontend — Alpine.js + plain ES
modules, **bundled with esbuild** and embedded in the Go server binary. App JS
source lives under `frontend/`; the built, minified bundles are committed under
`internal/*/static/js/dist/` and served from there (#295 / #721 reversed the old
"no bundler" rule). Always read `.claude/rules/frontend-style.md` — it is the
authoritative styling + constraints rule and this file defers to it.

## Tech stack

| Concern | Library / approach |
|---|---|
| Reactivity | Alpine.js 3.x (vendored, self-hosted, `window.Alpine`) |
| CSS | Tailwind CSS v4, CSS-first (no `tailwind.config.js`); source `frontend/web/css/tailwind.css`, built via `make tailwind` to `internal/assets/static/css/app.css` (committed) |
| Animations | anime.js (vendored, self-hosted, global `window.anime`) — only via the `runAnim` wrapper in `frontend/shared/anim.js` |
| App JS | plain ES modules under `frontend/`, **bundled with esbuild** (`make js`); built bundles committed to `internal/*/static/js/dist/` |
| HTTP | `fetch()` with relative paths — no Axios or other wrapper |
| Drag-and-drop | SortableJS (admin-only, vendored at `internal/assets/static/js/vendor/sortable.min.js`) — an allowed extra lib, for reorder only (#199) |
| Audio | Howler.js (vendored at `internal/assets/static/js/vendor/howler.min.js`) — an allowed extra lib, for per-question audio playback only (#1088) |
| Serving | Go `embed.FS` (compiled in); the static trees hold only built output + vendored libs |

No TypeScript. No runtime npm dependency (esbuild + the Tailwind CLI are dev-only build tools). No CDN.

## Source vs served (where to edit)

**Edit the `frontend/` source — never the `internal/*/static/js/dist/*` bundles or `app.css`; those are generated.**

```
frontend/                         ← build-time SOURCE (edit here)
  client/                         ← player client: app.js + join.js entries, components/, services/, util/
  web/                            ← admin/host: host-bigscreen.js, share.js, standalone admin scripts
                                     (cooldown.js, copy-prompt.js, password-length.js, quiz-reorder.js); css/tailwind.css
  shared/                         ← cross-tree modules (anim.js, standings.js, rememberedSession.js),
                                     inlined into BOTH bundle trees via the @shared/ import alias
  vendor/                         ← vendored-lib source
internal/client/static/           ← SERVED player-client output (do NOT hand-edit)
  js/dist/{app,join}.js           ← esbuild bundles (committed)   | js/vendor/ ← Alpine, anime, htmx
  index.html, partials            ← HTML shells
internal/assets/static/           ← SERVED web/admin/host output (do NOT hand-edit)
  js/dist/*.js                    ← esbuild bundles (committed)   | js/vendor/ ← + sortable.min.js
  css/app.css                     ← built Tailwind output (committed)
internal/web/tmpl/                ← admin/host Go HTML templates (.gohtml)
```

## Build & dev workflow

- `make js` rebuilds both bundle trees; `make js-watch-client` / `make js-watch-web` rebuild one tree on change (one watcher per served `dist` dir). `make js-check` (wired into `make check`) fails when the committed bundles drift from source.
- `make tailwind` builds `app.css`; `make tailwind-watch` rebuilds on change; `make tailwind-check` (in `make check`) fails on drift.
- **Rebuild and commit the bundles / `app.css` whenever you change JS or Tailwind classes**, or CI flags drift.
- Dev loop: run the server (optionally `CLIENT_DIR=internal/client/static go run ./cmd/server` to serve the static tree without recompiling the binary), plus `make js-watch-client` and `make tailwind-watch` in separate terminals so the bundles and CSS rebuild on edit. One Make target per long-running process — never a combined supervisor.
- The built bundles are embedded in the distroless image, so production needs no Node.

## How Alpine + the bundle are wired

The served HTML shell (`internal/client/static/index.html`) loads the vendored libs as classic scripts and the **built** bundle as a module:

```html
<script src="/static/js/vendor/anime.umd.min.js"></script>
<script type="module" src="/client/js/dist/app.js"></script>
<script defer src="/static/js/vendor/alpine.min.js"></script>
```

The entry (`frontend/client/app.js`) registers components on `alpine:init`:

```js
import { GameApp } from './components/GameApp.js';

document.addEventListener('alpine:init', () => {
    Alpine.data('gameApp', () => new GameApp());
});
```

The root `<div>` carries `x-data="gameApp"`. Alpine calls the component's `init()` automatically on mount. Per-instance subcomponents (e.g. a form repeated on the page) are registered as factory functions (`Alpine.data('claimNameForm', claimNameForm)`) so each `x-data` gets its own state.

## Component pattern (`frontend/client/components/`)

Components are **plain ES classes**. Alpine reads public properties as reactive data and calls methods on events.

```js
export class GameApp {
    constructor() {
        // declare ALL reactive state here — Alpine cannot track
        // properties added dynamically after construction
        this.someValue = null;
    }

    async init() {
        // Alpine calls this automatically on mount
    }

    async someAction() {
        // event handlers — bind in HTML with @click="someAction()"
    }
}
```

## Service pattern (`frontend/client/services/`)

Services are **thin `fetch` wrappers** exported as singletons. They do no state management — that belongs in the component.

```js
export class FooService {
    async doThing(id) {
        const response = await fetch(`/api/foos/${id}`);
        if (response.status === 404) return null;   // signal missing resource
        return response.json();
    }
}

export const fooService = new FooService();
```

- Always import the singleton (`fooService`), not the class.
- 404 is used as a business-logic signal (e.g. no more questions), not only an error.
- No global error handling exists yet — handle errors per call.

## UI views (`x-show` / `x-if`)

`x-show` keeps the element in the DOM (toggles CSS); `x-if` (inside `<template>`) removes it entirely — use it for heavier conditional blocks. The solo page has three mutually exclusive views: quiz selection (`!gameId`), in-game (`gameId && !finished`), results (`finished`).

## Timer / countdown

The progress bar reflects time remaining for the current question; `startedAt` / `expiredAt` come from the API as ISO 8601 strings.

```js
startCountdown() {
    const start = new Date(this.question.startedAt).getTime();
    const end   = new Date(this.question.expiredAt).getTime();
    const total = end - start;
    this.progress = 100;
    this.timer = setInterval(() => {
        const remaining = end - Date.now();
        this.progress = Math.max(0, (remaining / total) * 100);
        if (this.progress <= 0) { clearInterval(this.timer); this.timer = null; }
    }, 100);
}
```

Always `clearInterval(this.timer)` before starting a new countdown and in `reset()`.

## Animations (anime.js v4)

All animation goes through **`runAnim(targets, params)` in `frontend/shared/anim.js`** (esbuild inlines it into each bundle; never call `window.anime` directly). It no-ops to the final state under `prefers-reduced-motion` or a missing global, calling `params.onComplete` so callers still land their end state.

anime.js is **v4** — a v3-style parameter is silently ignored and the animation snaps to its end, so use the v4 names:

- `ease`, not v3 `easing`; ease names drop the prefix (`outQuad`, not `easeOutQuad`; `outBack`, not `easeOutBack`).
- `on`-prefixed callbacks: `onUpdate` / `onComplete` / `onBegin`, not `update` / `complete` / `begin`.
- Always pass `onComplete` so `runAnim`'s reduced-motion skip lands the final state.

Trigger inside `requestAnimationFrame` so the element is laid out first. Reserve anime.js for keyframed / spring-like motion; simple fades use Alpine `x-transition` + Tailwind. Keep the reduced-motion guards intact (both `runAnim` and the `@media (prefers-reduced-motion)` block in `frontend/web/css/tailwind.css`).

```js
runAnim('[data-feedback]', { scale: [0.9, 1.06, 1], duration: 560, ease: 'outBack', onComplete: () => {} });
```

## API contract (what the frontend consumes)

All endpoints are relative (same origin, no prefix). The route table is `internal/server/routes.go` (`/api/...` handlers in `internal/clientapi/`); read it for the current set and request/response shapes rather than a copied list, which rots. Conventions that hold:

- Game IDs are short xid strings, not integers; quizzes are addressed by `{slugID}`.
- `GET .../questions/next` returns **404** when there are no more questions — treat as "game over", not an error. 404 is a business signal in several endpoints.
- Responses are camelCase JSON; timestamps are ISO 8601 strings.

## Forms and labels

Every `<label>` must have a `for` attribute pointing to the `id` of a form element. To display a read-only value, use a `readonly` `<input>` styled with utility classes — not a `<p>` — so the label has a valid target.

## E2E selectors

Anchor Playwright locators on stable `data-testid` attributes, not regex matches against rendered text. Prefer `page.getByTestId('upload-button')` over `page.getByRole('button', { name: /upload/i })` and over CSS attribute regexes — the text/label can drift, the testid will not. Add the attribute to the element in the template and reference it from the spec. The same rule covers asserting visible state: `expect(page.getByTestId('upload-banner')).toContainText('3 images uploaded')` is fine; assertion regexes on URLs / hrefs are still allowed where the structure is the contract.

## What to avoid

See `.claude/rules/frontend-style.md`. In short: **esbuild is the only bundler** (don't add another, and no runtime npm package); no TypeScript; no framework beyond Alpine + anime.js (SortableJS is the sole drag-and-drop exception, admin-only; Howler.js the sole audio-playback exception, #1088); no inline `style="..."`; no hand-written `.css` files (styling goes through `frontend/web/css/tailwind.css` tokens / `@layer`); no CDN-loaded libraries; relative fetch paths only; no reactive state added outside an Alpine component constructor.
