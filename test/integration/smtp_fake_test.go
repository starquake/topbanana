package integration_test

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
)

// fakeSMTP is a minimal in-process SMTP catch-all used by the configured
// email-diagnostics test. It speaks just enough of the protocol for
// go-mail's plain (NoTLS, no-auth) client to complete a DATA phase and
// get a 250, so the admin test-send button reports success without a
// real Mailpit. It records every accepted recipient so a test can assert
// the message actually reached the wire if it wants to.
//
// It is the integration-suite analogue of the e2e suite's Mailpit
// dependency: SMTP_TLS=false + no credentials matches the local dev path
// the smtpMailer's NoTLS branch dials.
type fakeSMTP struct {
	ln   net.Listener
	addr string

	mu    sync.Mutex
	rcpts []string
}

// startFakeSMTP binds a catch-all SMTP server on an ephemeral port and
// serves connections until the test ends. The listener is closed via
// t.Cleanup, which unblocks the accept loop.
func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()

	listenConfig := &net.ListenConfig{}
	ln, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fake smtp listen err = %v, want nil", err)
	}
	s := &fakeSMTP{ln: ln, addr: ln.Addr().String()}
	t.Cleanup(func() {
		if cerr := ln.Close(); cerr != nil {
			t.Logf("fake smtp listener close err = %v", cerr)
		}
	})

	go s.serve()

	return s
}

// host and port split the listener address for the SMTP_HOST / SMTP_PORT
// env vars.
func (s *fakeSMTP) host(t *testing.T) string {
	t.Helper()
	host, _, err := net.SplitHostPort(s.addr)
	if err != nil {
		t.Fatalf("fake smtp SplitHostPort err = %v, want nil", err)
	}

	return host
}

func (s *fakeSMTP) port(t *testing.T) string {
	t.Helper()
	_, port, err := net.SplitHostPort(s.addr)
	if err != nil {
		t.Fatalf("fake smtp SplitHostPort err = %v, want nil", err)
	}

	return port
}

func (s *fakeSMTP) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return // listener closed at test end.
		}
		go s.handle(conn)
	}
}

// handle walks one SMTP session: greet, accept the envelope, swallow the
// DATA block, and close on QUIT. Every reply is a success code so the
// only failure modes left for the test are wiring ones (wrong host/port,
// config not flipping to "configured").
func (s *fakeSMTP) handle(conn net.Conn) {
	defer conn.Close() //nolint:errcheck // best-effort close on a test connection.

	br := bufio.NewReader(conn)
	write := func(line string) bool {
		_, err := conn.Write([]byte(line + "\r\n"))

		return err == nil
	}

	if !write("220 fake.smtp.test ESMTP ready") {
		return
	}

	inData := false
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		trimmed := strings.TrimRight(line, "\r\n")

		if inData {
			if trimmed == "." {
				inData = false
				if !write("250 2.0.0 Ok: queued") {
					return
				}
			}

			continue
		}

		verb := strings.ToUpper(trimmed)
		switch {
		case strings.HasPrefix(verb, "EHLO"), strings.HasPrefix(verb, "HELO"):
			// 250-<host> continuation then a final 250 with no
			// extensions: keeps the client on plain SMTP (no STARTTLS,
			// no AUTH offered), which is what the NoTLS path expects.
			if !write("250-fake.smtp.test") || !write("250 OK") {
				return
			}
		case strings.HasPrefix(verb, "MAIL FROM"):
			if !write("250 2.1.0 Ok") {
				return
			}
		case strings.HasPrefix(verb, "RCPT TO"):
			s.recordRcpt(trimmed)
			if !write("250 2.1.5 Ok") {
				return
			}
		case verb == "DATA":
			inData = true
			if !write("354 End data with <CR><LF>.<CR><LF>") {
				return
			}
		case verb == "QUIT":
			write("221 2.0.0 Bye") //nolint:errcheck // closing anyway.

			return
		case verb == "RSET", verb == "NOOP":
			if !write("250 2.0.0 Ok") {
				return
			}
		default:
			if !write("250 2.0.0 Ok") {
				return
			}
		}
	}
}

func (s *fakeSMTP) recordRcpt(rcptLine string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rcpts = append(s.rcpts, rcptLine)
}

// recipientCount returns how many RCPT TO lines the server has accepted.
func (s *fakeSMTP) recipientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.rcpts)
}
