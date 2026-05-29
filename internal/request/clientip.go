// Package request provides shared helpers for inspecting an
// [*http.Request] in ways the stdlib does not. The first inhabitant is
// [ClientIP], the source-IP extractor every per-IP rate limiter uses;
// the package exists so admin/, auth/, and any future per-IP guard
// share one implementation rather than copy-pasting RemoteAddr parsing.
package request

import (
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
)

// ParseTrustedProxyCIDRs parses a comma-separated CIDR list (e.g.
// "10.0.0.0/8,127.0.0.1/32") into a slice of [*net.IPNet]. An empty
// string returns nil so the caller can use the result as a "trust
// nothing" sentinel for [ClientIP]. Whitespace around individual entries
// is trimmed; empty entries (back-to-back commas, trailing comma) are
// silently dropped so an operator-friendly list stays valid.
func ParseTrustedProxyCIDRs(raw string) ([]*net.IPNet, error) {
	if raw == "" {
		return nil, nil
	}

	parts := strings.Split(raw, ",")
	out := make([]*net.IPNet, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		_, cidr, err := net.ParseCIDR(trimmed)
		if err != nil {
			return nil, fmt.Errorf("parse CIDR %q: %w", trimmed, err)
		}
		out = append(out, cidr)
	}
	if len(out) == 0 {
		return nil, nil
	}

	return out, nil
}

// ClientIP returns the source IP the caller should attribute r to.
//
// When trustedCIDRs is empty the function always returns the host half
// of r.RemoteAddr - the deployment is not behind a proxy, so X-Forwarded-For
// is attacker-controlled and ignoring it eliminates the spoof surface.
//
// When trustedCIDRs is non-empty and r.RemoteAddr matches one of the
// CIDRs, X-Forwarded-For is walked right-to-left and the first entry that
// is NOT in trustedCIDRs is returned - that is the original client IP
// the trusted hop chain forwarded for. If every XFF entry is trusted
// (a chain of internal hops with no public IP at the head) the
// RemoteAddr host - the directly-connected trusted hop - is returned,
// since any XFF entry would be spoofable. If XFF is absent or empty the
// RemoteAddr host is returned.
//
// When trustedCIDRs is non-empty but r.RemoteAddr does not match any
// CIDR, XFF is ignored entirely - the request came from an untrusted
// peer, so trusting their XFF would let them pick any bucket they like.
//
// The return value never includes a port. r.RemoteAddr is returned
// verbatim when it does not parse as host:port (this matches the
// pre-existing behaviour the two limiters relied on).
func ClientIP(r *http.Request, trustedCIDRs []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if len(trustedCIDRs) == 0 {
		return host
	}
	if !ipInCIDRs(host, trustedCIDRs) {
		return host
	}

	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return host
	}
	entries := splitXFF(xff)
	if len(entries) == 0 {
		return host
	}
	for _, v := range slices.Backward(entries) {
		if !ipInCIDRs(v, trustedCIDRs) {
			return v
		}
	}

	// Every XFF entry is trusted (a chain of internal hops with no
	// public IP at the head). Falling back to the leftmost XFF entry
	// would trust a value the immediate peer could spoof, so return the
	// directly-connected trusted hop instead.
	return host
}

// ipInCIDRs reports whether ip parses as an IP literal and is contained
// in any of cidrs. Non-IP strings (e.g. "unknown", a hostname) read as
// "not in any CIDR" so a malformed XFF segment is treated as an external
// client - the safer default for the right-to-left walk.
func ipInCIDRs(ip string, cidrs []*net.IPNet) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, c := range cidrs {
		if c.Contains(parsed) {
			return true
		}
	}

	return false
}

// splitXFF splits a raw X-Forwarded-For header value into its
// comma-separated entries, trims whitespace, and drops empty segments.
// Each upstream hop appends its view of the client IP, separated by
// commas (often with a trailing space).
func splitXFF(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}
