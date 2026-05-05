---
description: Go style rules for the topbanana project
globs:
  - "**/*.go"
---

## Assertion style

Use the `got, want` inline declaration pattern for all assertions:

```go
// values
if got, want := qs.ImageURL, q.ImageURL; got != want {
    t.Errorf("GetQuestion ImageURL = %q, want %q", got, want)
}

// errors — sentinel
if got, want := err, quiz.ErrQuizNotFound; !errors.Is(got, want) {
    t.Errorf("err = %v, want %v", got, want)
}

// errors — substring
if got, want := err.Error(), "failed to delete options"; !strings.Contains(got, want) {
    t.Errorf("err.Error() = %q, should contain %q", got, want)
}
```

Never do `if err.Error() == "..."` or `if result != expected { t.Errorf(..., result, expected) }` inline — always use `got, want` declared in the `if` statement.

## Common linter pitfalls

### `nilnil` — pointer + nil error return

Returning `(nil, nil)` from a function whose first return type is a pointer triggers the `nilnil` linter. This most often happens in test stubs that implement an interface.

```go
// BAD — nilnil fires
func (*stubStore) GetQuiz(_ context.Context, _ int64) (*quiz.Quiz, error) {
    return nil, nil
}

// GOOD — return a non-nil error for methods that should not be called
func (*stubStore) GetQuiz(_ context.Context, _ int64) (*quiz.Quiz, error) {
    return nil, errors.ErrUnsupported
}
```

Slice return types (`[]*quiz.Quiz, error`) are fine with `nil, nil` — only pointer returns are flagged.

### `revive: receiver-naming` — unused receivers in stubs

When a method does not use its receiver, omit the name entirely. Using `_` as the receiver name triggers the linter.

```go
// BAD — revive fires
func (_ *stubStore) CreateQuiz(_ context.Context, _ *quiz.Quiz) error { return nil }

// GOOD — omit the name
func (*stubStore) CreateQuiz(_ context.Context, _ *quiz.Quiz) error { return nil }
```

Only name the receiver when you need to reference it (e.g. `func (s *stubQuizStore) Ping(...) error { return s.pingErr }`).

### `noctx` — httptest.NewRequest without context

`httptest.NewRequest` is banned by the `noctx` linter. Always use `httptest.NewRequestWithContext` and pass `t.Context()`.

```go
// BAD — noctx fires
req := httptest.NewRequest(http.MethodGet, "/items/42", nil)

// GOOD
req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/items/42", nil)
```

`t.Context()` is preferred over `context.Background()` in tests — it is cancelled automatically when the test ends. No extra import needed.
