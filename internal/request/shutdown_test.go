package request_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/starquake/topbanana/internal/request"
)

func TestStreamContext(t *testing.T) {
	t.Parallel()

	t.Run("ends when the server begins shutting down", func(t *testing.T) {
		t.Parallel()
		shutdown, beginShutdown := context.WithCancel(t.Context())
		defer beginShutdown()
		req := httptest.NewRequestWithContext(
			request.WithShutdown(t.Context(), shutdown), http.MethodGet, "/events", nil,
		)

		ctx, cancel := request.StreamContext(req.Context())
		defer cancel()
		if got := ctx.Err(); got != nil {
			t.Fatalf("ctx.Err() before shutdown = %v, want nil", got)
		}
		beginShutdown()
		<-ctx.Done()
		if got, want := ctx.Err(), context.Canceled; !errors.Is(got, want) {
			t.Errorf("ctx.Err() = %v, want %v", got, want)
		}
	})

	t.Run("ends when the request ends", func(t *testing.T) {
		t.Parallel()
		reqCtx, endRequest := context.WithCancel(t.Context())
		req := httptest.NewRequestWithContext(reqCtx, http.MethodGet, "/events", nil)

		ctx, cancel := request.StreamContext(req.Context())
		defer cancel()
		endRequest()
		<-ctx.Done()
		if got, want := ctx.Err(), context.Canceled; !errors.Is(got, want) {
			t.Errorf("ctx.Err() = %v, want %v", got, want)
		}
	})

	t.Run("cancel ends the stream without a shutdown", func(t *testing.T) {
		t.Parallel()
		shutdown, beginShutdown := context.WithCancel(t.Context())
		defer beginShutdown()
		req := httptest.NewRequestWithContext(
			request.WithShutdown(t.Context(), shutdown), http.MethodGet, "/events", nil,
		)

		ctx, cancel := request.StreamContext(req.Context())
		cancel()
		if got, want := ctx.Err(), context.Canceled; !errors.Is(got, want) {
			t.Errorf("ctx.Err() = %v, want %v", got, want)
		}
		if got := shutdown.Err(); got != nil {
			t.Errorf("shutdown.Err() = %v, want nil", got)
		}
	})
}
