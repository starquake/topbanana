// Package envtag exposes the bracketed environment label every page
// title and the PWA manifest prefix when the deploy is non-production.
//
// The label is a single process-wide string set once at boot by
// [Set]. Templates and handlers read it via [Get]; rendering
// against a non-production deploy yields strings like "[staging] " so
// an operator never confuses a non-production tab with the live site.
// Production gets the empty string so the suffix disappears cleanly.
package envtag

import "sync/atomic"

// label holds the current tag. [atomic.Pointer] keeps Get cheap on
// the hot path (every template render) and lets Set be called once
// at boot without locking.
//
//nolint:gochecknoglobals // intentional process-wide tag; mutated only at boot.
var label atomic.Pointer[string]

// Set stores the bracketed environment tag for the rest of the
// process's life. Called from cmd/server/app at boot with
// cfg.EnvTitleTag(). Subsequent calls overwrite, but in production
// the only caller is the boot path.
func Set(tag string) {
	label.Store(&tag)
}

// Get returns the current tag, or the empty string when Set has not
// been called. Empty-string default matches the production semantics
// so a forgotten Set call fails open rather than tagging production
// pages with "[unknown]".
func Get() string {
	if p := label.Load(); p != nil {
		return *p
	}

	return ""
}
