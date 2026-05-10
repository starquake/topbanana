---
name: backend-dev
description: >
  Backend developer agent for the topbanana quiz game server.
  Invoke when implementing new features, endpoints, stores, migrations, or
  domain logic in this Go project. Gives the agent full project context so
  it can follow established patterns without re-reading the codebase.
---

You are working on **topbanana**, a Go backend for a real-time multiplayer quiz game.

## Tech stack

| Concern | Library / tool |
|---|---|
| HTTP server | stdlib `net/http` — no third-party framework |
| Database | SQLite via `modernc.org/sqlite` (pure Go, no CGO) |
| Migrations | `goose` (files live in `internal/migrations/`) |
| Query codegen | `sqlc` (SQL in `internal/queries/`, generated Go in `internal/db/`) |
| Structured logging | stdlib `log/slog` |
| Config | env vars parsed in `internal/config/config.go` |
| Module path | `github.com/starquake/topbanana` |

## Architecture layers

```
cmd/server/main.go          ← entrypoint, wires everything together
internal/config/            ← env-var config, parsed once at startup
internal/server/            ← http.Handler factory + route registration
internal/handlers/          ← shared HTTP helpers (EncodeJSON, DecodeJSON, ParseIDFromPath)
internal/admin/             ← HTML admin UI handlers
internal/clientapi/         ← JSON API handlers consumed by the game client
internal/client/            ← static file serving
internal/health/            ← /healthz handler
internal/game/              ← game domain: types, Service, Store interface
internal/quiz/              ← quiz domain: types, Store interface, validation
internal/store/             ← concrete SQLite store implementations
internal/db/                ← sqlc-generated CRUD (do not edit by hand)
internal/migrations/        ← goose SQL migrations
internal/dbtest/            ← shared test helper for in-memory SQLite DB
internal/testutil/          ← other test helpers
test/integration/           ← integration tests (build tag: integration)
```

## Handler pattern

Every handler is a **constructor that returns `http.Handler`**, not a method on a struct.
Dependencies are closed over, keeping the handler function stateless.

Write **one constructor per (method, path) pair**. Do not branch on `r.Method` inside a single handler — Go 1.22+ `http.ServeMux` already routes by method, so a `GET /foo` handler never sees a POST. For form pairs (GET to render, POST to submit), follow the existing admin pattern with two named constructors: e.g. `HandleQuizCreate` (GET form) + `HandleQuizSave` (POST submission).

```go
func HandleFoo(logger *slog.Logger, store SomeStore) http.Handler {
    type fooRequest  struct { ... }
    type fooResponse struct { ... }

    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        req, err := handlers.DecodeJSON[fooRequest](r)
        if err != nil {
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }
        // ... call store/service ...
        if err = handlers.EncodeJSON(w, http.StatusOK, res); err != nil {
            logger.ErrorContext(r.Context(), "error encoding response", slog.Any("err", err))
        }
    })
}
```

- Request/response structs are **defined inside** the handler constructor (unexported, local).
- Use `handlers.ParseIDFromPath(w, r, logger, "paramName")` for integer path params.
- Use `r.PathValue("name")` for string path params.
- Log errors with `logger.ErrorContext(r.Context(), "message", slog.Any("err", err))`.

## Route registration

Routes live in `internal/server/routes.go`. Add new routes to `addRoutes()`:
- Admin (HTML): `GET /admin/...` and `POST /admin/...`
- API (JSON): `GET /api/...`, `POST /api/...`, etc.

## Domain / service pattern

Domain logic lives in the domain package (`internal/game`, `internal/quiz`).
Each domain exposes:
1. **Types** — plain Go structs (no ORM tags).
2. **Store interface** — the set of persistence operations the domain needs.
3. **Service struct** — orchestrates business logic using the Store interface + other dependencies.

Store interfaces are defined in the domain package so domain code does not import `internal/store`.

## Adding a new feature (checklist)

1. **Migration** — add a `.sql` file in `internal/migrations/` using `goose` up/down format.
   Name it `YYYYMMDDHHMMSS_description.sql`.
2. **SQL query** — add the query to the relevant file in `internal/queries/` using `sqlc` annotations.
   Run `sqlc generate` to regenerate `internal/db/`.
   Keep `--` comments ASCII-only: non-ASCII characters (em-dashes, curly apostrophes, etc.) in the comment block above a query confuse `sqlc`'s SQLite preprocessor and produce cryptic errors pointing at *unrelated* queries below. Plain `-` and `'` only.
3. **Domain types** — add or update structs in the relevant domain package.
4. **Store interface** — extend the interface in the domain package if new persistence ops are needed.
5. **Store implementation** — implement the interface method in `internal/store/`.
6. **Service method** — add business logic in the domain `Service`.
7. **Handler** — add the handler constructor in `internal/clientapi/` (API) or `internal/admin/` (UI).
8. **Route** — register the handler in `internal/server/routes.go`.
9. **Tests** — unit tests alongside the source file; integration tests under `test/integration/`.

## Testing conventions

- Unit tests use `internal/dbtest` to get an in-memory SQLite DB (already migrated).
- Integration tests use build tag `//go:build integration` and live in `test/integration/`.
- Always use `t.Context()` instead of `context.Background()` in tests — it is cancelled automatically when the test ends. This applies to `httptest.NewRequestWithContext`, `context.WithCancel`, store calls, and any other place a context is needed inside a test.
- When tests need to reach unexported package internals (private constructors, helpers, methods exposed via wrappers), use the `export_test.go` convention: a `package <pkg>` (same-package) file that aliases the unexported identifier as `Export<Name>`, e.g. `var ExportNewWithClock = newWithClock`. External `*_test.go` files (`package <pkg>_test`) then call `pkg.ExportNewWithClock(...)`. Mirrors `internal/server/export_test.go` and `internal/session/export_test.go`. Prefer this over `*_internal_test.go` files so all tests stay in the external test package and the test-only surface is itemized in one file.

## Key sentinel errors

| Package | Error | Meaning |
|---|---|---|
| `quiz` | `ErrQuizNotFound` | quiz ID does not exist → 404 |
| `quiz` | `ErrQuestionNotFound` | question ID does not exist → 404 |
| `game` | `ErrGameNotFound` | game ID does not exist → 404 |
| `game` | `ErrNoMoreQuestions` | all quiz questions answered → 404 |
| `game` | `ErrQuestionNotInGame` | question does not belong to this game → 400 |

Always use `errors.Is` to match these, never string comparison.

## Database schema (summary)

```
quizzes(id, title, slug, description, created_at)
  └─ questions(id, quiz_id, text, position)
       └─ options(id, question_id, text, is_correct)

players(id, username, email, created_at)

games(id[xid], quiz_id, created_at, started_at)
  ├─ game_participants(id, game_id, player_id, joined_at)
  ├─ game_questions(id, game_id, question_id, started_at, expired_at)
  └─ game_answers(id, game_id, player_id, game_question_id, option_id, answered_at)
```

- Game IDs are short random strings (`github.com/rs/xid`), not integers.
- Foreign keys are enforced (`_pragma=foreign_keys(1)`).
- WAL mode is enabled for concurrent reads.

## Config / env vars

| Variable | Default | Notes |
|---|---|---|
| `APP_ENV` | `development` | set to `production` to require `DB_URI` |
| `HOST` | `localhost` | |
| `PORT` | `8080` | |
| `DB_URI` | SQLite file `topbanana.sqlite` | mandatory in production |
| `DB_DRIVER` | `sqlite` | only supported value |
| `CLIENT_DIR` | `""` | path to compiled frontend assets (dev only) |

## Known tech debt

`playerID` is hardcoded to `1` in `internal/clientapi/` — authentication is not implemented yet. Do not add more hardcoded player IDs.
