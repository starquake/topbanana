package handlers_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/handlers"
)

func TestLoggerFromContext_ReturnsStashedLogger(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	stashed := slog.New(slog.NewTextHandler(&buf, nil)).With(slog.String("requestId", "abc123"))

	ctx := WithLogger(t.Context(), stashed)
	LoggerFromContext(ctx).Info("line")

	if got, want := buf.String(), "requestId=abc123"; !strings.Contains(got, want) {
		t.Errorf("log output %q missing %q", got, want)
	}
}

func TestLoggerFromContext_FallsBackToDefault(t *testing.T) {
	t.Parallel()

	if got := LoggerFromContext(context.Background()); got == nil {
		t.Error("LoggerFromContext on a bare context = nil, want a usable logger")
	}
}
