---
description: Go style rules for the topbanana project
globs:
  - "**/*.go"
---

## Assertion style

**Write each value once.** Name the value under test `got` and the expected value `want`, and have both the condition and the failure message use those names. Then a copied assertion that is later edited cannot end up checking one value while reporting another.

```go
// BAD: x and 5 each appear twice; change one copy and the message goes stale
if x != 5 {
    t.Errorf("x = %d, want %d", x, 5)
}

// GOOD: each appears once
if got, want := x, 5; got != want {
    t.Errorf("x = %d, want %d", got, want)
}
```

The format string counts too: `"x = %d, want 5"` writes the expected value a second time, so format `want` instead.

Declare `got, want` in the `if` statement by default. That scopes them to one assertion, so the next assertion can reuse the names. The declaration may span several lines when an expression is long: the rule is about repetition, not line count. When the same expected value is checked more than once, declare `want` once above those checks instead.

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
