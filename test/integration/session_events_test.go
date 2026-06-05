package integration_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// sessionTickPayload mirrors the JSON shape of one SSE tick on the session
// event channel (MP-2 / #679). Local to the integration test so it pins the
// wire contract independently of the production type names. It must stay
// minimal: the side-channel carries no roster, quiz, or player data.
type sessionTickPayload struct {
	Version uint64 `json:"version"`
	Phase   string `json:"phase"`
}

// sessionEventStream is what openSessionEventStream hands back: a scanner
// over the live SSE body plus the response status and content-type the
// caller asserts on. Returning the metadata (rather than the whole
// *http.Response) keeps body-close ownership inside the helper, which
// registers the t.Cleanup that closes it.
type sessionEventStream struct {
	Scanner     *bufio.Scanner
	StatusCode  int
	ContentType string
}

// openSessionEventStream subscribes to GET /api/sessions/{code}/events on
// the given client and returns a scanner over its body plus the response
// metadata. The caller ends the stream by cancelling its own context; the
// body is closed via t.Cleanup.
func openSessionEventStream(
	ctx context.Context, t *testing.T, client *http.Client, baseURL, code string,
) sessionEventStream {
	t.Helper()

	streamURL := fmt.Sprintf("%s/api/sessions/%s/events", baseURL, code)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		t.Fatalf("NewRequest events err = %v, want nil", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("events Do err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if cerr := resp.Body.Close(); cerr != nil && !errors.Is(cerr, context.DeadlineExceeded) {
			t.Errorf("events Body.Close err = %v", cerr)
		}
	})

	return sessionEventStream{
		Scanner:     bufio.NewScanner(resp.Body),
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
	}
}

// readSessionTick consumes one `data: ...\n\n` event from a session event
// stream and decodes the tick. Comment (heartbeat) lines starting with ":"
// are skipped so an idle stream's keep-alive does not derail a tick read.
func readSessionTick(t *testing.T, scanner *bufio.Scanner) sessionTickPayload {
	t.Helper()

	var dataLine string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if dataLine == "" {
				continue
			}

			break
		}
		if after, ok := strings.CutPrefix(line, "data: "); ok {
			dataLine = after
		}
	}
	if dataLine == "" {
		t.Fatal("no session tick received before stream closed or timeout")
	}

	var payload sessionTickPayload
	if err := json.Unmarshal([]byte(dataLine), &payload); err != nil {
		t.Fatalf("Unmarshal session tick err = %v, body = %q", err, dataLine)
	}

	return payload
}

// TestSessionEvents_TickOnJoinAndReady drives the MP-2 happy path: a host
// opens a session, a participant subscribes to the event channel and drains
// the initial tick, then a second player joins and the host-side
// participant toggles ready. Each mutation must push a tick whose version
// strictly increments, and the phase stays "lobby".
func TestSessionEvents_TickOnJoinAndReady(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "events-happy")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "events-host", "events-host-pass-123")
	code := createSession(ctx, t, host, baseURL, qz.ID)

	// Alice joins first so she is a participant and may subscribe.
	alice := newAnonClient(t)
	joinSession(ctx, t, alice, baseURL, code, "Alice")

	streamCtx, streamCancel := context.WithTimeout(ctx, 10*time.Second)
	defer streamCancel()
	stream := openSessionEventStream(streamCtx, t, alice, baseURL, code)

	if got, want := stream.StatusCode, http.StatusOK; got != want {
		t.Fatalf("events status = %d, want %d", got, want)
	}
	if got, want := stream.ContentType, "text/event-stream"; !strings.HasPrefix(got, want) {
		t.Errorf("events Content-Type = %q, want prefix %q", got, want)
	}

	// Initial frame seeds the current version (Alice's join already bumped
	// it to 1) and the lobby phase.
	initial := readSessionTick(t, stream.Scanner)
	if got, want := initial.Phase, "lobby"; got != want {
		t.Errorf("initial tick phase = %q, want %q", got, want)
	}

	// Bob joins -> a tick with a higher version.
	bob := newAnonClient(t)
	joinSession(ctx, t, bob, baseURL, code, "Bob")
	afterJoin := readSessionTick(t, stream.Scanner)
	if got, prev := afterJoin.Version, initial.Version; got <= prev {
		t.Errorf("after-join version = %d, want > %d (join must bump the version)", got, prev)
	}
	if got, want := afterJoin.Phase, "lobby"; got != want {
		t.Errorf("after-join tick phase = %q, want %q", got, want)
	}

	// Bob marks ready -> another tick, version higher again.
	setReady(ctx, t, bob, baseURL, code, true)
	afterReady := readSessionTick(t, stream.Scanner)
	if got, prev := afterReady.Version, afterJoin.Version; got <= prev {
		t.Errorf("after-ready version = %d, want > %d (ready must bump the version)", got, prev)
	}
	if got, want := afterReady.Phase, "lobby"; got != want {
		t.Errorf("after-ready tick phase = %q, want %q", got, want)
	}
}

// TestSessionEvents_MultipleSubscribers pins that every subscribed
// participant receives the tick from a single mutation.
func TestSessionEvents_MultipleSubscribers(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "events-multi")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "events-multi-host", "events-multi-pass-123")
	code := createSession(ctx, t, host, baseURL, qz.ID)

	alice := newAnonClient(t)
	bob := newAnonClient(t)
	joinSession(ctx, t, alice, baseURL, code, "Alice")
	joinSession(ctx, t, bob, baseURL, code, "Bob")

	streamCtx, streamCancel := context.WithTimeout(ctx, 10*time.Second)
	defer streamCancel()

	// Both Alice and Bob subscribe and drain their initial frame.
	streamA := openSessionEventStream(streamCtx, t, alice, baseURL, code)
	streamB := openSessionEventStream(streamCtx, t, bob, baseURL, code)
	_ = readSessionTick(t, streamA.Scanner)
	_ = readSessionTick(t, streamB.Scanner)

	// The host joins as a player (a third roster row) -> one mutation, both
	// subscribers must see a tick.
	hostPlayer := newAnonClient(t)
	joinSession(ctx, t, hostPlayer, baseURL, code, "Carol")

	tickA := readSessionTick(t, streamA.Scanner)
	tickB := readSessionTick(t, streamB.Scanner)
	if got, want := tickA.Phase, "lobby"; got != want {
		t.Errorf("subscriber A tick phase = %q, want %q", got, want)
	}
	if got, want := tickB.Phase, "lobby"; got != want {
		t.Errorf("subscriber B tick phase = %q, want %q", got, want)
	}
	if got, want := tickA.Version, tickB.Version; got != want {
		t.Errorf("subscriber A version = %d, subscriber B version = %d, want equal", got, want)
	}
}

// TestSessionEvents_NonParticipantRejected pins that a stranger who knows
// the code but never joined is rejected with 404 from the event channel,
// so SSE subscription does not leak that the session exists.
func TestSessionEvents_NonParticipantRejected(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "events-authz")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "events-authz-host", "events-authz-pass-123")
	code := createSession(ctx, t, host, baseURL, qz.ID)

	t.Run("stranger with the code is 404", func(t *testing.T) {
		t.Parallel()
		stranger := newAnonClient(t)
		streamURL := fmt.Sprintf("%s/api/sessions/%s/events", baseURL, code)
		resp := httpGet(ctx, t, stranger, streamURL)
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("stranger events status = %d, want %d", got, want)
		}
		if got, want := resp.Header.Get("Content-Type"), "text/event-stream"; strings.HasPrefix(got, want) {
			t.Errorf("Content-Type = %q, must not start with %q (gate must not pose as SSE)", got, want)
		}
	})

	t.Run("unknown join code is 404", func(t *testing.T) {
		t.Parallel()
		stranger := newAnonClient(t)
		resp := httpGet(ctx, t, stranger, baseURL+"/api/sessions/NOPE99/events")
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("unknown-code events status = %d, want %d", got, want)
		}
	})
}

// TestSessionEvents_HeartbeatOnIdleStream pins that an idle event stream
// emits keep-alive comment frames past the HTTP server's 10s WriteTimeout,
// the same fix the leaderboard stream relies on. It opens a stream against
// a session with no further mutations, holds it past the 10s WriteTimeout
// AND past one 25s heartbeat tick, and asserts the connection stayed open
// and at least one heartbeat comment arrived.
func TestSessionEvents_HeartbeatOnIdleStream(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL

	qz := seedLiveQuiz(ctx, t, setup.Stores.Quizzes, "events-heartbeat")

	host := &http.Client{
		Jar:           mustJar(t),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	registerVerifyAndSignIn(ctx, t, host, baseURL, setup.DBURI, "events-hb-host", "events-hb-pass-123")
	code := createSession(ctx, t, host, baseURL, qz.ID)

	alice := newAnonClient(t)
	joinSession(ctx, t, alice, baseURL, code, "Alice")

	// 27s is past the 10s WriteTimeout AND past the 25s heartbeat interval,
	// so a working server emits at least one heartbeat inside the window.
	const window = 27 * time.Second
	streamCtx, streamCancel := context.WithTimeout(ctx, window)
	defer streamCancel()

	start := time.Now()
	stream := openSessionEventStream(streamCtx, t, alice, baseURL, code)
	if got, want := stream.StatusCode, http.StatusOK; got != want {
		t.Fatalf("events status = %d, want %d", got, want)
	}

	var heartbeatLines int
	var sawInitialData bool
	for stream.Scanner.Scan() {
		line := stream.Scanner.Text()
		switch {
		case strings.HasPrefix(line, "data: "):
			sawInitialData = true
		case strings.HasPrefix(line, ":"):
			heartbeatLines++
		default:
			// Blank separators and any framing lines - ignore.
		}
	}
	elapsed := time.Since(start)

	if !sawInitialData {
		t.Fatal("never received the initial-tick SSE event")
	}
	if elapsed < window-2*time.Second {
		t.Fatalf("stream closed after %v, want it to stay open the full %v window", elapsed, window)
	}
	if heartbeatLines == 0 {
		t.Errorf("got 0 heartbeat (`:` ...) lines in %v, want at least 1", window)
	}
}
