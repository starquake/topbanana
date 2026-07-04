package locale_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/starquake/topbanana/internal/locale"
)

func TestIsValid(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		LocaleEN: true,
		LocaleNL: true,
		"fr":     false,
		"":       false,
		"EN":     false,
	}
	for loc, want := range cases {
		if got := IsValid(loc); got != want {
			t.Errorf("IsValid(%q) = %v, want %v", loc, got, want)
		}
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		cookie     string
		acceptLang string
		want       string
	}{
		{name: "default when nothing set", want: LocaleEN},
		{name: "valid cookie wins", cookie: LocaleNL, acceptLang: "en-US", want: LocaleNL},
		{name: "invalid cookie ignored", cookie: "fr", acceptLang: "nl", want: LocaleNL},
		{name: "accept-language nl", acceptLang: "nl-NL,en;q=0.8", want: LocaleNL},
		{name: "accept-language en", acceptLang: "en-US,en;q=0.9", want: LocaleEN},
		{name: "accept-language unknown falls back", acceptLang: "fr-FR,de;q=0.8", want: LocaleEN},
		{name: "junk header falls back", acceptLang: ";;;q=", want: LocaleEN},
		{name: "nl behind a weight still wins", acceptLang: "de;q=0.9, nl;q=0.8", want: LocaleNL},
		{name: "cookie wins over accept-language", cookie: LocaleEN, acceptLang: "nl", want: LocaleEN},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			if tc.acceptLang != "" {
				r.Header.Set("Accept-Language", tc.acceptLang)
			}
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: CookieName, Value: tc.cookie})
			}
			if got := Resolve(r); got != tc.want {
				t.Errorf("Resolve() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTranslate(t *testing.T) {
	t.Parallel()

	if got, want := Translate(LocaleNL, "login.submit"), "Inloggen"; got != want {
		t.Errorf("Translate(nl, login.submit) = %q, want %q", got, want)
	}
	if got, want := Translate(LocaleEN, "login.submit"), "Log in"; got != want {
		t.Errorf("Translate(en, login.submit) = %q, want %q", got, want)
	}
	// Unknown locale falls back to the English value.
	if got, want := Translate("fr", "login.submit"), "Log in"; got != want {
		t.Errorf("Translate(fr, login.submit) = %q, want %q", got, want)
	}
	// A missing key returns the key itself so it is visible, never fatal.
	if got, want := Translate(LocaleNL, "does.not.exist"), "does.not.exist"; got != want {
		t.Errorf("Translate(nl, missing) = %q, want %q", got, want)
	}
}

func TestMessages(t *testing.T) {
	t.Parallel()

	nl := Messages(LocaleNL)
	if got, want := nl["login.submit"], "Inloggen"; got != want {
		t.Errorf("Messages(nl)[login.submit] = %q, want %q", got, want)
	}
	en := Messages(LocaleEN)
	if got, want := en["login.submit"], "Log in"; got != want {
		t.Errorf("Messages(en)[login.submit] = %q, want %q", got, want)
	}
	// Every English key must be present in the merged nl map (English base
	// overlaid with the locale), so the SPA always has a value.
	for key := range en {
		if _, ok := nl[key]; !ok {
			t.Errorf("Messages(nl) missing key %q present in English base", key)
		}
	}
}

func TestSetCookie(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	SetCookie(rec, LocaleNL)

	res := rec.Result()
	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Errorf("close body err = %v", err)
		}
	}()
	var found *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == CookieName {
			found = c

			break
		}
	}
	if found == nil {
		t.Fatalf("SetCookie did not set a %q cookie", CookieName)
	}
	if got, want := found.Value, LocaleNL; got != want {
		t.Errorf("cookie value = %q, want %q", got, want)
	}
	if got, want := found.Path, "/"; got != want {
		t.Errorf("cookie path = %q, want %q", got, want)
	}
	if found.MaxAge <= 0 {
		t.Errorf("cookie MaxAge = %d, want a positive lifetime", found.MaxAge)
	}
}
