---
name: frontend-dev
description: >
  Frontend developer agent for the topbanana quiz game client.
  Invoke when working on the browser UI: HTML, Alpine.js components,
  service classes, styling, or API integration. Gives the agent full
  frontend context so it follows established patterns without re-reading
  the codebase.
---

You are working on the **topbanana** game client — a zero-build-step vanilla JS
frontend embedded inside the Go server binary.

## Tech stack

| Concern | Library / approach |
|---|---|
| Reactivity | Alpine.js 3.x (vendored, self-hosted) |
| CSS | Tailwind CSS v4, CSS-first config; built via `make tailwind`, output committed (see `.claude/rules/frontend-style.md`) |
| Animations | anime.js 4.2.0 (vendored, self-hosted, global `window.anime`) |
| JS modules | Native ES modules (`type="module"`) -- no bundler, no npm for app code |
| HTTP | `fetch()` -- no Axios or other wrapper |
| Build step | CSS only, via the Tailwind standalone CLI; JS/HTML have no build (no bundler, no TypeScript, no transpilation) |
| Serving | Go `embed.FS` (compiled into binary); in prod minified via `tdewolff/minify` |

## File layout

```
internal/client/
  client.go                        ← Go handler: serves files, minifies in prod
  static/
    index.html                     ← single page; all Alpine bindings live here
    js/
      app.js                       ← registers Alpine.data('gameApp', ...) on alpine:init
      components/
        GameApp.js                 ← main Alpine component class
      services/
        GameService.js             ← fetch wrappers for game API; exports singleton
        QuizService.js             ← fetch wrappers for quiz API; exports singleton
```

The `static/` tree is the only place you should edit for frontend work.

## How Alpine.js is wired up

`index.html` loads the vendored anime.js (classic script), `app.js` as a module, then
Alpine deferred -- all self-hosted under `js/vendor/`, no CDN (see #295):

```html
<script src="/client/js/vendor/anime.umd.min.js"></script>
<script type="module" src="/client/js/app.js"></script>
<script defer src="/client/js/vendor/alpine.min.js"></script>
```

`app.js` registers the component on `alpine:init`:

```js
import { GameApp } from './components/GameApp.js';

document.addEventListener('alpine:init', () => {
    Alpine.data('gameApp', () => new GameApp());
});
```

The root `<div>` in `index.html` carries `x-data="gameApp"`, making all component
properties and methods available as Alpine expressions in the template.

Alpine automatically calls `init()` on the component class when the component mounts.

## Component pattern (`GameApp.js`)

Components are **plain ES classes**. Alpine reads public properties as reactive data
and calls methods on events.

```js
export class GameApp {
    constructor() {
        // declare all reactive state here
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

Keep component state flat and declared in the constructor — Alpine cannot track
properties added dynamically after construction.

## Service pattern (`services/`)

Services are **thin `fetch` wrappers** exported as singletons. They do no state
management — that belongs in the component.

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
- 404 is used as a business-logic signal (e.g., no more questions), not only an error.
- No global error handling exists yet — handle errors per call.

## UI views (controlled by `x-show` / `x-if`)

The single page has three mutually exclusive views:

| State | Condition | Content |
|---|---|---|
| Quiz selection | `!gameId` | dropdown + Start Game button |
| In-game | `gameId && !finished` | progress bar, question text, answer buttons, feedback notification |
| Results | `finished` | scores table + Play Again button |

`x-show` keeps the element in the DOM (toggles CSS); `x-if` removes it entirely.
Use `x-if` inside `<template>` for conditional rendering of heavier blocks.

## Timer / countdown

The progress bar reflects time remaining for the current question.
`startedAt` and `expiredAt` come from the API as ISO 8601 strings.

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

## API contract (what the frontend consumes)

All endpoints are relative (same origin, no prefix). The route table is `internal/server/routes.go` (`/api/...` handlers in `internal/clientapi/`); read it for the current set and request/response shapes rather than a copied list, which rots. Conventions that hold:

- Game IDs are short xid strings, not integers; quizzes are addressed by `{slugID}`.
- `GET .../questions/next` returns **404** when there are no more questions — treat as "game over", not an error. 404 is a business signal in several endpoints.
- Responses are camelCase JSON; timestamps are ISO 8601 strings.

## Game flow (state machine)

```
[quiz selection] --startGame()--> [in-game, loading question]
                                       |
                        nextQuestion() returns question
                                       |
                              [question displayed]
                                       |
                           submitAnswer(optionId)
                                       |
                              [feedback shown 2s]
                                       |
                        nextQuestion() → 404 → [results]
                                       |
                                   reset() → [quiz selection]
```

## Dev workflow

The client files are embedded in the Go binary. During development, set `CLIENT_DIR`
to the `static/` directory so edits are served directly without recompiling:

```
CLIENT_DIR=internal/client/static go run ./cmd/server
```

Then open `http://localhost:8080/client/`. When editing styles, run `make tailwind-watch`
in a second terminal so `app.css` rebuilds on change; commit the rebuilt `app.css`.

In production (`APP_ENV=production`) the embedded files are served minified
(HTML + CSS + JS) — avoid patterns that break minification (e.g., regex literals
that look like division, unclosed template strings across lines).

## Animations (anime.js)

The game client uses **anime.js 4.2.0** (vendored, self-hosted, exposed as the global `window.anime`) for in-game feedback motion. Always go through the `runAnim(targets, params)` wrapper in `GameApp.js`; never call `anime` directly:

- It returns early when `prefers-reduced-motion: reduce` is set, and no-ops if the global is missing.
- It supports both the v4 API (`anime.animate(targets, params)`) and the legacy callable form.
- `targets` is a CSS selector string or a DOM element; `params` are anime.js properties (`scale`, `translateX`, `rotate`, `opacity`, `duration`, `easing`).

Trigger animations inside `requestAnimationFrame` so the element is laid out first:

```js
runAnim('[data-feedback]', { scale: [0.9, 1.06, 1], duration: 560, easing: 'easeOutBack' });
```

Simple fades use Alpine `x-transition` + Tailwind classes; reserve anime.js for keyframed or spring-like motion. The reduced-motion guards in both `runAnim` and `frontend/web/css/tailwind.css` must stay intact.

## Forms and labels

Every `<label>` must have a `for` attribute pointing to the `id` of a form element. To display a read-only value, use a `readonly` `<input>` styled with utility classes -- not a `<p>` -- so the label has a valid target.

## What to avoid

See the styling and constraints rules in `.claude/rules/frontend-style.md`: Tailwind v4 for CSS (built via `make tailwind`), no bundler/npm for app JS, no TypeScript, no framework beyond Alpine + anime.js, no inline styles, no hand-written CSS files, no CDN libraries, no hardcoded base URLs, no reactive state outside the constructor.
