# #213 — HTMX migration: pros, cons, options

Research notes to support a decision on the remaining phases of [#213](https://github.com/starquake/topbanana/issues/213) (adopt HTMX as the unified frontend stack).

## TL;DR

Three viable paths, in order of cost:

- **Path A — SSE leaderboard only.** 1–2 days. Adds real-time leaderboard, keeps everything else. Leaves #213 phases 4 + 5 open.
- **Path C — Tailwind on player + SSE.** 3–5 days. Adds real-time leaderboard and unifies the CSS toolchain. Keeps Alpine and `/api/*`. Leaves #213 partially open.
- **Path B — Full HTMX migration (#213 as written).** 2–3 weeks. Drops Alpine, drops player-only `/api/*`, drops Bulma. Closes #213.

## Factual baseline

### Player client (what would be touched / removed)

| File | Lines | Role |
|---|---|---|
| `index.html` | 278 | Three-state shell (select / question / leaderboard) + claim-name modal |
| `GameApp.js` | 418 | Main state machine, polling, timer, anime.js animations |
| `ClaimNameForm.js` | 55 | Reusable modal form |
| `GameService.js` + `QuizService.js` + `PlayerService.js` | 47 + 68 + 64 = 179 | Thin fetch wrappers |
| `synthwave.css` + `tokens.css` | 463 | Player skin |
| `app.js` | 35 | Alpine init + mobile-keyboard viewport sync |
| **Total** | **1,368** | |

### JSON API surface (`/api/*`)

Ten endpoints. **All player-only.** Zero external consumers — no docs promise these, no integration tests treat them as a public contract.

| Endpoint | Method | Today's caller | HTMX replacement (Path B) |
|---|---|---|---|
| `/api/players/me` | GET | `PlayerService.getMe()` | Cookie / session lookup, no fetch |
| `/api/players/me` | PATCH | `PlayerService.claimName()` | `POST /play/.../claim` returning HTML |
| `/api/quizzes` | GET | `QuizService.getQuizzes()` | Server-render `<select>` in `/play` |
| `/api/quizzes/{slugID}` | GET | (none directly) | Server-render `/play/{slugID}` |
| `/api/quizzes/{slugID}/my-game` | GET | `GameService.getMyGameForQuiz()` | Server-render with query param |
| `/api/quizzes/{slugID}/leaderboard` | GET | `GameService.getQuizLeaderboard()` | **SSE stream** |
| `/api/games` | POST | `GameService.startGame()` | `POST /play/{slugID}/start` → HTML |
| `/api/games/{gameID}/questions/next` | GET | `GameService.getNextQuestion()` | HTML-fragment response |
| `/api/games/{gameID}/questions/{questionID}/answers` | POST | `GameService.submitAnswer()` | HTML-fragment response (feedback) |
| `/api/games/{gameID}/results` | GET | (none directly) | Server-render results page |

### What is actually latency-sensitive client-side

Exactly one thing: the **100 ms countdown-timer interval** that drives the progress bar. Everything else is "click → wait for server answer → react." There is no optimistic UI; correctness is server-resolved. The Alpine reactivity is convenient but not load-bearing for sub-second feedback.

### Timer fairness: a pre-existing concern that HTMX doesn't change

The "answered in time" decision is made server-side at INSERT time. `AnsweredAt` is set by SQLite's `CURRENT_TIMESTAMP` when the `CreateAnswer` row commits (see `internal/queries/games.sql:28`). `internal/game/game.go:594` then checks `a.AnsweredAt.After(a.Question.ExpiredAt)` to decide timeout.

The pipeline:
```
tap → client render delay → network uplink → server auth/decode/insert → CURRENT_TIMESTAMP
```

Every leg adds latency. A mobile player who taps with 200 ms left on the visible timer can have their answer stamped 300 ms *after* `ExpiredAt` and be marked as timed-out — invisible to the player but the actual reason their score is 0.

This is true today on the JSON API. **HTMX doesn't fix or worsen it** — both architectures pay the same round-trip. The fix is a separable ticket: have the client POST a `tappedAt` and clamp it server-side so client time can only pull the recorded answer time *earlier*, never later. Tracked as a separate issue (see the ticket filed alongside this doc).

### Non-trivial things to recreate under HTMX

- **anime.js entrance / shake / pop animations.** They hit CSS selectors (not Alpine reactivity), but they're *triggered* by Alpine's `x-if` lifecycle. HTMX swaps need explicit `htmx:afterSwap` hooks.
- **Mobile-keyboard viewport-height sync** (`app.js`, 35 lines). Survives untouched.
- **Reactive state binding for the claim modal.** Becomes manual event binding.

## Path A — SSE leaderboard only

### What changes

- Add `GET /api/quizzes/{slugID}/leaderboard/stream` returning `text/event-stream`.
- Drop a `new EventSource(...)` subscription into `GameApp.js` (~15 lines).
- Keep Alpine, keep `/api/*`, keep Bulma on the player.

### Cost

- **~146 LOC added, 0 removed.**
- **Files touched:** `internal/clientapi/clientapi.go`, `internal/client/static/js/components/GameApp.js`, `internal/server/routes.go`, `test/integration/gameplay_test.go`.
- **Effort:** 1–2 days for a solo dev.

### Risks

- SSE connection drops on network transition (phone switches WiFi). Need stale-data detection + refetch fallback.
- Timer behaviour unaffected.

### Reversibility

Excellent. Delete the endpoint and the `EventSource` call; everything else continues to work.

### Closes #213?

No. Leaves phase 4 / phase 5 open.

## Path B — Full HTMX migration (#213 as written)

### What changes

- Server-render player pages as `html/template`; admin-style partials.
- Drop Alpine.js, drop player-only `/api/*`, drop Bulma on the player.
- Ship a 40-line vanilla-JS timer module per the ticket's plan.
- Rewrite `test/integration/gameplay_test.go` (currently 1,005 LOC against the JSON contract).

### Cost

- **~+1,135 LOC added, −930 LOC removed = +205 net.**
- "Real complexity" is much higher than the net LOC suggests: a new player template + handler layer, a new test suite (~400 LOC for `player_api_test.go`), and partial rewrites of all 5 e2e suites.

#### Files added / modified (16 total)

*Removed:* `index.html`, `GameApp.js`, `ClaimNameForm.js`, all three service files.

*Added:* `internal/web/tmpl/player/pages/play.gohtml` (~180), `partials/question.gohtml` (~60), `partials/leaderboard.gohtml` (~120), `partials/feedback.gohtml` (~35), `layouts/base.gohtml` (~25), `internal/playerapi/playerapi.go` (~250), `internal/client/static/js/timer.js` (~40), `internal/client/static/js/modal.js` (~30), `test/integration/player_api_test.go` (~400).

*Rewritten:* `test/integration/gameplay_test.go`, `test/e2e/tests/player.spec.ts`, `test/e2e/tests/claim.spec.ts`.

### Effort

2–3 weeks for a solo dev who knows the codebase. Testing alone is ~1 week.

### Risks

- The timer is now in vanilla JS without Alpine — small risk of subtle bugs.
- anime.js animations need re-engineering via `htmx:afterSwap`.
- Image loading within questions needs the new feedback partial to handle img errors without Alpine `@error` directives.
- Anonymous-player session creation moves from "first `/api/` call" to "first `/play/*` POST."

### Reversibility

Poor. Once `/api/*` and the client services are deleted, rollback costs another ~1 week (re-add Alpine + services, rewrite tests again).

### Closes #213?

Yes.

## Path C — Tailwind on player + SSE; keep Alpine and `/api/*`

### What changes

- Migrate `synthwave.css` + Bulma references in the player to Tailwind utility classes. Unifies the CSS toolchain across admin and player surfaces.
- Add the SSE leaderboard stream from Path A.
- Keep the existing JSON API and Alpine state management.

### Cost

- Template surgery on `index.html` + the SSE endpoint.
- **Effort:** 3–5 days.

### Risks

- Some visual regressions during the Bulma → Tailwind translation. Mockup parity for the player surface isn't established yet.

### Reversibility

Excellent. Pure markup + new endpoint.

### Closes #213?

No, but ticks the "visual consistency" box that's usually the most visible part of unification.

## Decision matrix

| Concern | Path A | Path B | Path C |
|---|---|---|---|
| Real-time leaderboard | ✓ | ✓ | ✓ |
| One CSS toolchain across surfaces | ✗ | ✓ | ✓ |
| One rendering story (server-rendered) | ✗ | ✓ | ✗ |
| Drops Alpine | ✗ | ✓ | ✗ |
| Drops `/api/*` (player-only routes) | ✗ | ✓ | ✗ |
| Sub-100 ms answer feedback | wash | wash | wash |
| Risk of regressing the timer | none | low–medium | none |
| Animations re-engineering required | no | yes (`htmx:afterSwap`) | no |
| Reversibility | excellent | poor | excellent |
| Estimated effort | 1–2 days | 2–3 weeks | 3–5 days |
| Closes #213 | no | yes | no |

## Non-obvious considerations

1. **The "API mental model" is real but unused.** Nothing external consumes `/api/*`. If a native app ever ships, you'd reintroduce a JSON API tuned for *that* client's needs, not preserve the current one. Keeping `/api/*` "because APIs are good" pays maintenance now for a hypothetical future that you'd likely redesign anyway.

2. **The `gameplay_test.go` rewrite is the hidden cost of Path B.** 1,005 lines tied to JSON parsing. The +1,135 *includes* this rewrite. If that test suite is the safety net for the gameplay loop, expect a chunk of Path B time to be re-establishing equivalent assertions against the HTML responses.

3. **HTMX is mature (2.0 stable) but the ecosystem is narrower** than React/Vue. Onboarding a contributor would require more explaining than "we use a JSON API + a thin client." Context cost, not a technical one.

4. **`anime.js` and HTMX co-exist fine** via `htmx:afterSwap`, but the current animations were tuned against Alpine's lifecycle. Expect some visual polish loss-and-recover in Path B.

5. **The SSE leaderboard is a feature on its own**, independent of architecture. Today the leaderboard renders once and goes stale. Path A delivers that win for ~1 day of work with zero commitment to anything else.

6. **Path A and Path B are not mutually exclusive.** Do A now and B later if the appetite returns. If the SSE event payload is designed as an HTML fragment from day one, Path A's endpoint converges with Path B's leaderboard partial.

## What's not covered here

- Performance numbers on real mobile hardware. The "wash" calls in the table are based on architecture, not measurement.
- What the `htmx:afterSwap` integration actually looks like in code.
- What the Go SSE handler would look like (probably ~80 LOC including connection-cleanup boilerplate).
- The native-app question. Both paths can later host a native client by adding a versioned JSON API; nothing in this decision forecloses that.
