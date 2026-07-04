// Package locale is a small hand-rolled i18n catalog for the player-facing
// surfaces (home, browse, login, register, and the SPA shell). It supports two
// locales, English (the default) and Dutch, loaded once from embedded JSON.
// There is deliberately no i18n library: two locales and a flat key->string
// map do not justify a dependency, and it matches the frontend library policy.
//
// Keys are dot-namespaced (e.g. "home.title", "login.submit"). A missing key
// falls back to the English value, then to the key itself, so an untranslated
// string is always visible and never fatal.
package locale

import (
	"embed"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"
)

// Locale identifiers. LocaleEN is the default and the fallback every lookup
// resolves to when a locale or key is missing.
const (
	LocaleEN = "en"
	LocaleNL = "nl"
)

// CookieName is the cookie that pins a manually chosen UI language. It takes
// precedence over the request's Accept-Language header in [Resolve].
const CookieName = "lang"

// cookieMaxAgeSeconds keeps a chosen language for a year, so a manual switch
// survives across sessions without being permanent.
const cookieMaxAgeSeconds = 365 * 24 * 60 * 60

//go:embed en.json nl.json
var catalogFS embed.FS

//nolint:gochecknoglobals // immutable translation catalog, parsed once at load and never mutated.
var catalog = loadCatalog()

// loadCatalog reads and parses the embedded per-locale JSON once. A read or
// parse failure is a build-time programming error (the files are embedded and
// covered by tests), so it panics rather than returning an error.
func loadCatalog() map[string]map[string]string {
	locales := Locales()
	out := make(map[string]map[string]string, len(locales))
	for _, loc := range locales {
		data, err := catalogFS.ReadFile(loc + ".json")
		if err != nil {
			panic(fmt.Sprintf("locale: read %s.json: %v", loc, err))
		}
		var messages map[string]string
		if err := json.Unmarshal(data, &messages); err != nil {
			panic(fmt.Sprintf("locale: parse %s.json: %v", loc, err))
		}
		out[loc] = messages
	}

	return out
}

// Locales returns the supported locales in display order, English first.
// Returned as a fresh slice so callers can range over it without sharing a
// backing array.
func Locales() []string {
	return []string{LocaleEN, LocaleNL}
}

// IsValid reports whether loc is one of the supported locales.
func IsValid(loc string) bool {
	return loc == LocaleEN || loc == LocaleNL
}

// Resolve picks the UI locale for a request: a valid lang cookie wins, then
// the Accept-Language header, then English.
func Resolve(r *http.Request) string {
	if c, err := r.Cookie(CookieName); err == nil && IsValid(c.Value) {
		return c.Value
	}

	return fromAcceptLanguage(r.Header.Get("Accept-Language"))
}

// fromAcceptLanguage does a deliberately small Accept-Language parse: split on
// commas, drop any ";q=..." weight, and return LocaleNL for the first tag whose
// primary subtag is "nl". Anything else (including an empty header) falls back
// to English, the only other locale.
func fromAcceptLanguage(header string) string {
	for part := range strings.SplitSeq(header, ",") {
		tag := part
		if i := strings.IndexByte(tag, ';'); i >= 0 {
			tag = tag[:i]
		}
		primary, _, _ := strings.Cut(strings.TrimSpace(tag), "-")
		if strings.EqualFold(primary, LocaleNL) {
			return LocaleNL
		}
	}

	return LocaleEN
}

// Translate returns the message for key in loc, falling back to the English
// value and then to key itself so a missing translation is visible but never
// fatal.
func Translate(loc, key string) string {
	if messages, ok := catalog[loc]; ok {
		if v, ok := messages[key]; ok {
			return v
		}
	}
	if v, ok := catalog[LocaleEN][key]; ok {
		return v
	}

	return key
}

// Messages returns the full message map for loc, English overlaid with the
// locale's own entries, so every key resolves to a value. Used to inject the
// catalog into the SPA. A fresh map is returned on every call.
func Messages(loc string) map[string]string {
	merged := make(map[string]string, len(catalog[LocaleEN]))
	maps.Copy(merged, catalog[LocaleEN])
	if loc != LocaleEN {
		maps.Copy(merged, catalog[loc])
	}

	return merged
}

// SetCookie writes the lang cookie so a manual language choice persists. It is
// deliberately not marked Secure so it works over plain HTTP in development; the
// SPA reads the locale from the injected window.__I18N__ global rather than the
// cookie, so HttpOnly is on. SameSite=Lax is enough for a preference cookie that
// carries no auth.
//
//nolint:gosec // G124: a language preference cookie carries no auth; not Secure so it works over plain HTTP in dev.
func SetCookie(w http.ResponseWriter, loc string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    loc,
		Path:     "/",
		MaxAge:   cookieMaxAgeSeconds,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
