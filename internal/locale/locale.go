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
	"html/template"
	"maps"
	"net/http"
	"strconv"
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

// defaultQWeight is the Accept-Language q-value assumed for a tag that omits
// ";q="; qBitSize is the bit size passed to [strconv.ParseFloat] for a q-value.
const (
	defaultQWeight = 1.0
	qBitSize       = 64
)

//go:embed en.json nl.json
var catalogFS embed.FS

//nolint:gochecknoglobals // immutable translation catalog, parsed once at load and never mutated.
var catalog = loadCatalog()

// messagesJSON holds each locale's merged catalog marshaled to JSON once at
// load. The catalog is immutable, so the SPA shell can inject the same bytes on
// every render instead of re-merging and re-marshaling ~170 entries per request.
//
//nolint:gochecknoglobals // immutable, derived from catalog once at load.
var messagesJSON = loadMessagesJSON()

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

// fromAcceptLanguage does a deliberately small Accept-Language parse honouring
// q-weights: it reads each comma-separated tag with its ";q=" weight (default
// 1.0 when omitted) and returns the supported locale with the highest weight,
// ties broken by list order. A q of 0 excludes a tag; a malformed q skips it.
// Anything unrecognised (including an empty header) falls back to English.
func fromAcceptLanguage(header string) string {
	best := LocaleEN
	var bestQ float64
	for part := range strings.SplitSeq(header, ",") {
		tag, q, ok := parseLanguageRange(part)
		if !ok || q <= 0 {
			continue
		}
		primary, _, _ := strings.Cut(tag, "-")
		var loc string
		switch {
		case strings.EqualFold(primary, LocaleNL):
			loc = LocaleNL
		case strings.EqualFold(primary, LocaleEN):
			loc = LocaleEN
		default:
			continue
		}
		if q > bestQ {
			best = loc
			bestQ = q
		}
	}

	return best
}

// parseLanguageRange splits one Accept-Language element into its language tag
// and q-weight. The weight defaults to 1.0 when no ";q=" is present; a present
// but unparseable weight returns ok=false so the caller drops the tag. An empty
// tag also returns ok=false.
func parseLanguageRange(part string) (tag string, q float64, ok bool) {
	fields := strings.Split(part, ";")
	tag = strings.TrimSpace(fields[0])
	if tag == "" {
		return "", 0, false
	}
	q = defaultQWeight
	for _, f := range fields[1:] {
		val, isQ := strings.CutPrefix(strings.TrimSpace(f), "q=")
		if !isQ {
			continue
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(val), qBitSize)
		if err != nil {
			return "", 0, false
		}
		q = parsed
	}

	return tag, q, true
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
// locale's own entries, so every key resolves to a value. A fresh map is
// returned on every call. The SPA shell path uses the precomputed
// [MessagesJSON] instead; this stays for callers that need the map itself.
func Messages(loc string) map[string]string {
	merged := make(map[string]string, len(catalog[LocaleEN]))
	maps.Copy(merged, catalog[LocaleEN])
	if loc != LocaleEN {
		maps.Copy(merged, catalog[loc])
	}

	return merged
}

// loadMessagesJSON marshals each locale's merged catalog to JSON once at load.
// A marshal failure is a build-time programming error (the catalog is static
// ASCII covered by tests), so it panics rather than returning an error.
func loadMessagesJSON() map[string]template.JS {
	out := make(map[string]template.JS, len(catalog))
	for _, loc := range Locales() {
		data, err := json.Marshal(Messages(loc))
		if err != nil {
			panic(fmt.Sprintf("locale: marshal %s messages: %v", loc, err))
		}
		// Content is our own static ASCII catalog, never attacker input.
		//nolint:gosec // G203: trusted, server-owned JSON; not attacker-controlled.
		out[loc] = template.JS(data)
	}

	return out
}

// MessagesJSON returns the precomputed merged catalog for loc as JSON ready to
// embed in a <script> as window.__I18N__.messages. The bytes are byte-identical
// across calls. Content is server-owned static ASCII, so [template.JS] carries
// no XSS risk. An unknown locale falls back to English.
func MessagesJSON(loc string) template.JS {
	if v, ok := messagesJSON[loc]; ok {
		return v
	}

	return messagesJSON[LocaleEN]
}

// SetCookie writes the lang cookie so a manual language choice persists. No
// client JS needs to read this cookie, so HttpOnly is set. SameSite=Lax and a
// non-Secure attribute suit a preference cookie that carries no auth and must
// work over plain HTTP in development.
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
