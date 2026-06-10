package clientapi_test

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
	. "github.com/starquake/topbanana/internal/clientapi"
	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/handlers"
	"github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/store"
)

// boundCaptureHandler is a slog.Handler that records each record's attrs,
// merging any WithAttrs-bound prefix attrs (the request-scoped logger's
// request id, the per-handler player) so a test can assert a handler line
// inherited the bound correlation fields. slog.With binds attrs via
// WithAttrs, not on the Record, so a no-op WithAttrs would drop them.
type boundCaptureHandler struct {
	mu      *sync.Mutex
	records *[]map[string]slog.Value
	attrs   []slog.Attr
}

func newBoundCaptureHandler() boundCaptureHandler {
	return boundCaptureHandler{mu: &sync.Mutex{}, records: &[]map[string]slog.Value{}}
}

func (boundCaptureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h boundCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	flat := make(map[string]slog.Value, len(h.attrs)+r.NumAttrs())
	for _, a := range h.attrs {
		flat[a.Key] = a.Value
	}
	r.Attrs(func(a slog.Attr) bool {
		flat[a.Key] = a.Value

		return true
	})

	h.mu.Lock()
	defer h.mu.Unlock()
	*h.records = append(*h.records, flat)

	return nil
}

func (h boundCaptureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)

	return h
}

func (h boundCaptureHandler) WithGroup(string) slog.Handler { return h }

// hasRecordWith reports whether any captured record carries every wanted
// attribute key with a non-empty string value.
func (h boundCaptureHandler) hasRecordWith(keys ...string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, rec := range *h.records {
		ok := true
		for _, k := range keys {
			if _, present := rec[k]; !present {
				ok = false

				break
			}
		}
		if ok {
			return true
		}
	}

	return false
}

// sessionTestEnv bundles a live-session service over real stores on a
// dbtest DB so a session handler test hits real data.
type sessionTestEnv struct {
	db      *sql.DB
	service *livesession.Service
	players auth.PlayerStore
}

func newSessionTestEnv(t *testing.T) *sessionTestEnv {
	t.Helper()

	discard := slog.New(slog.DiscardHandler)
	conn := dbtest.Open(t)
	stores := store.New(conn, discard)
	service := livesession.NewService(stores.LiveSessions, stores.Quizzes, discard)

	return &sessionTestEnv{db: conn, service: service, players: stores.Players}
}

func (e *sessionTestEnv) seedAnonymousPlayer(t *testing.T, name string) int64 {
	t.Helper()

	p, err := e.players.CreateAnonymousPlayer(t.Context(), name)
	if err != nil {
		t.Fatalf("CreateAnonymousPlayer(%q) err = %v, want nil", name, err)
	}

	return p.ID
}

// TestHandleSessionState_LogLineInheritsRequestScopedFields pins the request-
// scoped logger adoption: a session handler pulls the logger off the context
// and binds the player id, so the line it emits on a store failure carries
// both the middleware-bound requestId and the handler-bound player without
// restating either at the call site.
func TestHandleSessionState_LogLineInheritsRequestScopedFields(t *testing.T) {
	t.Parallel()

	env := newSessionTestEnv(t)
	hostID := env.seedAnonymousPlayer(t, "host")

	sess, err := env.service.CreateSession(t.Context(), nil, hostID)
	if err != nil {
		t.Fatalf("CreateSession err = %v, want nil", err)
	}

	logs := newBoundCaptureHandler()
	// Mimic the middleware: a request-scoped logger carrying a request id,
	// stashed on the context exactly as requestLogger does in production.
	reqLogger := slog.New(logs).With(slog.String("requestId", "req-xyz"))
	ctx := handlers.WithLogger(t.Context(), reqLogger)
	ctx = withPlayer(ctx, hostID)

	// Close the DB so GetLobbyState fails, driving the writeInternalError
	// branch that logs through the request-scoped, player-bound logger.
	if cerr := env.db.Close(); cerr != nil {
		t.Fatalf("closing test DB: %v", cerr)
	}

	handler := HandleSessionState(env.service)
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/sessions/"+sess.JoinCode+"/state", nil)
	req.SetPathValue("code", sess.JoinCode)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusInternalServerError; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if !logs.hasRecordWith("requestId", "player") {
		t.Error("no log line carried both requestId and player; want the handler to inherit both")
	}
}
