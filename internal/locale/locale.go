// Package locale is a small hand-rolled i18n catalog for the player-facing
// surfaces, with two locales (English default, Dutch) loaded from embedded JSON.
// Keys are dot-namespaced; a missing key falls back to the English value, then
// to the key itself, so an untranslated string is always visible.
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

// Locale identifiers. LocaleEN is the default and the fallback for a missing
// locale or key.
const (
	LocaleEN = "en"
	LocaleNL = "nl"
)

// CookieName is the cookie that pins a manually chosen UI language. It takes
// precedence over the request's Accept-Language header in [Resolve].
const CookieName = "lang"

// cookieMaxAgeSeconds keeps a chosen language for a year so a manual switch
// survives across sessions.
const cookieMaxAgeSeconds = 365 * 24 * 60 * 60

// defaultQWeight is the Accept-Language q-value assumed when ";q=" is omitted;
// qBitSize is the bit size passed to [strconv.ParseFloat] for a q-value.
const (
	defaultQWeight = 1.0
	qBitSize       = 64
)

//go:embed en.json nl.json
var catalogFS embed.FS

//nolint:gochecknoglobals // immutable translation catalog, parsed once at load.
var catalog = loadCatalog()

// messagesJSON holds each locale's merged catalog marshaled to JSON once at
// load, so the SPA shell can inject the same bytes without re-merging per request.
//
//nolint:gochecknoglobals // immutable, derived from catalog once at load.
var messagesJSON = loadMessagesJSON()

// loadCatalog reads and parses the embedded per-locale JSON once. A failure is a
// build-time programming error (files are embedded and tested), so it panics.
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

// Locales returns the supported locales in display order, English first, as a
// fresh slice callers can range over without sharing a backing array.
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

// fromAcceptLanguage returns the supported locale with the highest q-weight,
// ties broken by list order. A q of 0 or a malformed q drops the tag; anything
// unrecognised (including an empty header) falls back to English.
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

// parseLanguageRange splits one Accept-Language element into its tag and
// q-weight (default 1.0). An empty tag or an unparseable weight returns ok=false.
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

// MessageID is a catalog message key. It is distinct from a display string so
// the compiler stops a translated value being passed where a key is wanted, and
// vice versa. The template FuncMap boundary is the one place a raw string is
// converted to a MessageID, because templates are inherently stringly-typed.
type MessageID string

// Translate returns the message for key in loc, falling back to the English
// value and then to the key itself so a missing translation is visible.
func Translate(loc string, key MessageID) string {
	k := string(key)
	if messages, ok := catalog[loc]; ok {
		if v, ok := messages[k]; ok {
			return v
		}
	}
	if v, ok := catalog[LocaleEN][k]; ok {
		return v
	}

	return k
}

// TranslateWith translates key for loc and replaces each {token} placeholder in
// the message with its value from tokens, so a message can carry dynamic values
// without a non-constant format string that vet would flag.
func TranslateWith(loc string, key MessageID, tokens map[string]string) string {
	msg := Translate(loc, key)
	for token, value := range tokens {
		msg = strings.ReplaceAll(msg, "{"+token+"}", value)
	}

	return msg
}

// TranslateCount translates key for loc and fills its {n} placeholder with n.
func TranslateCount(loc string, key MessageID, n int) string {
	return TranslateWith(loc, key, map[string]string{"n": strconv.Itoa(n)})
}

// Messages returns a fresh full message map for loc (English overlaid with the
// locale's own entries) so every key resolves. The SPA shell path uses the
// precomputed [MessagesJSON]; this is for callers that need the map itself.
func Messages(loc string) map[string]string {
	merged := make(map[string]string, len(catalog[LocaleEN]))
	maps.Copy(merged, catalog[LocaleEN])
	if loc != LocaleEN {
		maps.Copy(merged, catalog[loc])
	}

	return merged
}

// loadMessagesJSON marshals each locale's merged catalog to JSON once at load.
// A failure is a build-time programming error (static ASCII, tested), so it panics.
func loadMessagesJSON() map[string]template.JS {
	out := make(map[string]template.JS, len(catalog))
	for _, loc := range Locales() {
		data, err := json.Marshal(Messages(loc))
		if err != nil {
			panic(fmt.Sprintf("locale: marshal %s messages: %v", loc, err))
		}
		//nolint:gosec // G203: trusted, server-owned JSON; not attacker-controlled.
		out[loc] = template.JS(data)
	}

	return out
}

// MessagesJSON returns the precomputed merged catalog for loc as JSON ready to
// embed as window.__I18N__.messages. Content is server-owned static ASCII, so
// [template.JS] carries no XSS risk. An unknown locale falls back to English.
func MessagesJSON(loc string) template.JS {
	if v, ok := messagesJSON[loc]; ok {
		return v
	}

	return messagesJSON[LocaleEN]
}

// SetCookie writes the lang cookie so a manual language choice persists. It is
// HttpOnly (no JS reads it) and non-Secure so it works over plain HTTP in dev.
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
