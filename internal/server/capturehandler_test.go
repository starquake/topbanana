package server_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// captureHandler is a slog.Handler that records every record so a test can
// assert on the lines a middleware emitted. It honours WithAttrs and
// WithGroup: slog.With binds attributes at the handler level (via WithAttrs),
// not on the Record, so a handler that returned the receiver unchanged would
// silently drop With-bound fields like the request id. The accumulated prefix
// attrs are merged into each captured record's own attrs, under any active
// group prefix, so the assertions see the same key=value pairs a real handler
// would format.
type captureHandler struct {
	mu      *sync.Mutex
	records *[]capturedRecord
	attrs   []slog.Attr
	groups  []string
}

// capturedRecord is one record flattened to its attribute map, with the
// With-bound prefix attrs already merged in.
type capturedRecord struct {
	message string
	attrs   map[string]slog.Value
}

func newCaptureHandler() captureHandler {
	return captureHandler{mu: &sync.Mutex{}, records: &[]capturedRecord{}}
}

func (captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h captureHandler) Handle(_ context.Context, r slog.Record) error {
	flat := make(map[string]slog.Value, len(h.attrs)+r.NumAttrs())
	for _, a := range h.attrs {
		flat[a.Key] = a.Value
	}
	r.Attrs(func(a slog.Attr) bool {
		flat[h.qualify(a.Key)] = a.Value

		return true
	})

	h.mu.Lock()
	defer h.mu.Unlock()
	*h.records = append(*h.records, capturedRecord{message: r.Message, attrs: flat})

	return nil
}

func (h captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	next = append(next, h.attrs...)
	for _, a := range attrs {
		next = append(next, slog.Attr{Key: h.qualify(a.Key), Value: a.Value})
	}
	h.attrs = next

	return h
}

func (h captureHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	h.groups = append(append([]string(nil), h.groups...), name)

	return h
}

// qualify joins any active group prefix onto an attribute key, mirroring how
// a real handler namespaces grouped attrs as "group.key".
func (h captureHandler) qualify(key string) string {
	if len(h.groups) == 0 {
		return key
	}

	return strings.Join(h.groups, ".") + "." + key
}

// attrsFor returns the attribute map of the first captured record with the
// given message, failing the test if none was captured.
func (h captureHandler) attrsFor(t *testing.T, msg string) map[string]slog.Value {
	t.Helper()

	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range *h.records {
		if r.message == msg {
			return r.attrs
		}
	}
	t.Fatalf("no captured record with message %q", msg)

	return nil
}

// attrValuesFor returns the string value of attr key for every captured
// record with the given message, in capture order.
func (h captureHandler) attrValuesFor(msg, key string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, r := range *h.records {
		if r.message == msg {
			out = append(out, r.attrs[key].String())
		}
	}

	return out
}
