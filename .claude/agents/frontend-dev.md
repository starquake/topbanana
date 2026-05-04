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
| Reactivity | Alpine.js 3.x (CDN) |
| CSS | Bulma 1.0.4 (CDN) |
| JS modules | Native ES modules (`type="module"`) — no bundler, no npm |
| HTTP | `fetch()` — no Axios or other wrapper |
| Build step | **None** — plain files, no TypeScript, no transpilation |
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

`index.html` loads Alpine deferred (`defer`), then `app.js` as a module:

```html
<script type="module" src="js/app.js"></script>
<script defer src="https://cdn.jsdelivr.net/npm/alpinejs@3.x.x/dist/cdn.min.js"></script>
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

## Styling conventions

Uses **Bulma** utility classes. Common patterns already in use:

| Element | Classes |
|---|---|
| Page wrapper | `section > .container` |
| Heading | `title`, `subtitle` |
| Button | `button is-primary` / `is-link` / `is-fullwidth` |
| Dropdown | `field > .control > .select > select` |
| Notification | `notification is-success` / `is-danger` |
| Progress bar | `progress is-primary` (with `:value` and `max="100"`) |
| Table | `table is-striped is-fullwidth` |

Do not add custom CSS unless Bulma has no equivalent. Inline styles are forbidden.

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

All endpoints are relative (no origin prefix needed — same origin).

### `GET /api/quizzes`
```json
[{ "id": 1, "title": "...", "slug": "...", "description": "...", "createdAt": "..." }]
```

### `POST /api/games`  ← `{ "quizId": 1 }`
```json
{ "id": "d5gi9kgn2facjitokja0" }
```
`Location` header is also set but not used by the client.

### `GET /api/games/:gameId/questions/next`
```json
{
  "id": 1,
  "text": "What is ...?",
  "options": [{ "id": 1, "text": "..." }, ...],
  "startedAt": "2026-05-01T12:00:00Z",
  "expiredAt": "2026-05-01T12:00:10Z"
}
```
Returns **404** when there are no more questions — treat as "game over", not an error.

### `POST /api/games/:gameId/questions/:questionId/answers`  ← `{ "optionId": 1 }`
```json
{ "optionId": 1, "correct": true, "score": 850 }
```

### `GET /api/games/:gameId/results`
```json
{
  "gameId": "...",
  "playerScores": [{ "playerId": 1, "score": 1700 }]
}
```

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

Then open `http://localhost:8080/client/`.

In production (`APP_ENV=production`) the embedded files are served minified
(HTML + CSS + JS) — avoid patterns that break minification (e.g., regex literals
that look like division, unclosed template strings across lines).

## What to avoid

- Do **not** introduce a build step (npm, Vite, webpack, etc.) — keep it build-free.
- Do **not** use TypeScript — plain `.js` only.
- Do **not** add a third-party component library or JS framework beyond Alpine + Bulma.
- Do **not** add reactive state outside the constructor (Alpine won't track it).
- Do **not** use inline styles — use Bulma classes.
- Do **not** hardcode base URLs — all fetch calls use relative paths.
- Do **not** add CSS files — add a `<style>` block to `index.html` only if Bulma truly has no equivalent.
