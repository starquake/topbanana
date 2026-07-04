package integration_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Known translated strings, pinned so a catalog change that drops or renames
// these keys surfaces here. English is the default; Dutch is the nl overlay.
const (
	homeTaglineEN = "Pick a quiz, race the clock, see who comes out on top."
	homeTaglineNL = "Kies een quiz, race tegen de klok en kijk wie er bovenaan eindigt."
	loginSubEN    = "Welcome back. Sign in to manage your quizzes."
	loginSubNL    = "Welkom terug. Log in om je quizzen te beheren."

	// The English subtitle's "we'll" is HTML-escaped by {{t}} (we&#39;ll), so
	// pin the apostrophe-free lead; the Dutch line has no apostrophe.
	forgotSubEN  = "Enter your email and"
	forgotSubNL  = "Vul je e-mailadres in en we sturen je een resetlink."
	profileSubEN = "Your account settings."
	profileSubNL = "Je accountinstellingen."
)

// TestLocale_Integration covers #1115 through the real server: the home and
// login pages localize to Dutch for an Accept-Language header or lang cookie
// and default to English, and the /lang switcher sets the cookie and redirects.
func TestLocale_Integration(t *testing.T) {
	t.Parallel()

	ctx, srv := startServer(t, nil)
	baseURL := srv.BaseURL

	t.Run("home defaults to English", func(t *testing.T) {
		t.Parallel()
		body := getBodyWithHeaderCookie(ctx, t, baseURL+"/", "", nil)
		assertContains(t, body, homeTaglineEN)
		assertContains(t, body, `<html lang="en">`)
		assertNotContains(t, body, homeTaglineNL)
	})

	t.Run("home renders Dutch for Accept-Language nl", func(t *testing.T) {
		t.Parallel()
		body := getBodyWithHeaderCookie(ctx, t, baseURL+"/", "nl-NL,nl;q=0.9,en;q=0.8", nil)
		assertContains(t, body, homeTaglineNL)
		assertContains(t, body, `<html lang="nl">`)
		assertNotContains(t, body, homeTaglineEN)
	})

	t.Run("home renders Dutch for a lang=nl cookie", func(t *testing.T) {
		t.Parallel()
		body := getBodyWithHeaderCookie(ctx, t, baseURL+"/", "", &http.Cookie{Name: "lang", Value: "nl"})
		assertContains(t, body, homeTaglineNL)
		assertContains(t, body, `<html lang="nl">`)
	})

	t.Run("login defaults to English", func(t *testing.T) {
		t.Parallel()
		body := getBodyWithHeaderCookie(ctx, t, baseURL+"/login", "", nil)
		assertContains(t, body, loginSubEN)
		assertContains(t, body, `<html lang="en">`)
	})

	t.Run("login renders Dutch for a lang=nl cookie", func(t *testing.T) {
		t.Parallel()
		body := getBodyWithHeaderCookie(ctx, t, baseURL+"/login", "", &http.Cookie{Name: "lang", Value: "nl"})
		assertContains(t, body, loginSubNL)
		assertContains(t, body, `<html lang="nl">`)
	})

	t.Run("GET /lang/nl sets the cookie and redirects to the referer", func(t *testing.T) {
		t.Parallel()

		client := &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/lang/nl", nil)
		if err != nil {
			t.Fatalf("NewRequest err = %v, want nil", err)
		}
		req.Header.Set("Referer", baseURL+"/quizzes")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("client.Do err = %v, want nil", err)
		}
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		if got, want := resp.Header.Get("Location"), "/quizzes"; got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
		var langCookie *http.Cookie
		for _, c := range resp.Cookies() {
			if c.Name == "lang" {
				langCookie = c
			}
		}
		if langCookie == nil {
			t.Fatal("no lang cookie set by /lang/nl")
		}
		if got, want := langCookie.Value, "nl"; got != want {
			t.Errorf("lang cookie = %q, want %q", got, want)
		}
	})

	t.Run("SPA shell injects the i18n global and html lang", func(t *testing.T) {
		t.Parallel()
		body := getBodyWithHeaderCookie(ctx, t, baseURL+"/join", "", &http.Cookie{Name: "lang", Value: "nl"})
		assertContains(t, body, `<html lang="nl">`)
		assertContains(t, body, `window.__I18N__ = {locale: "nl", messages:`)
		// The merged catalog is injected so the SPA has every key.
		assertContains(t, body, `"login.submit"`)
		// Server-rendered static shell text is localized through {{t}}, and the
		// footer switcher marks the active locale.
		assertContains(t, body, `Doe mee met een spel`)
		assertContains(t, body, `data-testid="lang-switcher"`)
		assertContains(t, body, `href="/lang/en"`)
	})

	t.Run("solo SPA shell localizes static text to English by default", func(t *testing.T) {
		t.Parallel()
		body := getBodyWithHeaderCookie(ctx, t, baseURL+"/client/", "", nil)
		assertContains(t, body, `<html lang="en">`)
		assertContains(t, body, `Browse all quizzes`)
		assertContains(t, body, `data-testid="lang-switcher"`)
	})

	t.Run("GET /lang with an invalid locale sets no cookie", func(t *testing.T) {
		t.Parallel()

		client := &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/lang/fr", nil)
		if err != nil {
			t.Fatalf("NewRequest err = %v, want nil", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("client.Do err = %v, want nil", err)
		}
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusSeeOther; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		for _, c := range resp.Cookies() {
			if c.Name == "lang" {
				t.Errorf("lang cookie set to %q for an invalid locale, want none", c.Value)
			}
		}
	})
}

// TestLocaleAuthPages_Integration covers #1197: the remaining auth pages
// localize to Dutch under a lang=nl cookie and default to English. It drives a
// public page (/forgot-password) and an authenticated one (/profile) through
// the real server.
func TestLocaleAuthPages_Integration(t *testing.T) {
	t.Parallel()

	// smtpEnabledEnv also flips REGISTRATION_ENABLED on and, per #1170, is the
	// condition under which the forgot-password route is mounted at all.
	ctx, srv := startServer(t, smtpEnabledEnv(t))
	baseURL := srv.BaseURL

	t.Run("forgot-password defaults to English", func(t *testing.T) {
		t.Parallel()
		body := getBodyWithHeaderCookie(ctx, t, baseURL+"/forgot-password", "", nil)
		assertContains(t, body, forgotSubEN)
		assertContains(t, body, `<html lang="en">`)
		assertNotContains(t, body, forgotSubNL)
	})

	t.Run("forgot-password renders Dutch for a lang=nl cookie", func(t *testing.T) {
		t.Parallel()
		body := getBodyWithHeaderCookie(ctx, t, baseURL+"/forgot-password", "", &http.Cookie{Name: "lang", Value: "nl"})
		assertContains(t, body, forgotSubNL)
		assertContains(t, body, `<html lang="nl">`)
		assertNotContains(t, body, forgotSubEN)
	})

	// /profile is behind the auth gate, so mint a signed-in client first.
	authn := authClient(t)
	registerVerifyAndMint(ctx, t, authn, baseURL, srv.DBURI, "locale-profile", "correct-battery-13")

	t.Run("profile defaults to English", func(t *testing.T) {
		t.Parallel()
		body := getBodyWithClientCookie(ctx, t, authn, baseURL+"/profile", nil)
		assertContains(t, body, profileSubEN)
		assertContains(t, body, `<html lang="en">`)
		assertNotContains(t, body, profileSubNL)
	})

	t.Run("profile renders Dutch for a lang=nl cookie", func(t *testing.T) {
		t.Parallel()
		body := getBodyWithClientCookie(ctx, t, authn, baseURL+"/profile", &http.Cookie{Name: "lang", Value: "nl"})
		assertContains(t, body, profileSubNL)
		assertContains(t, body, `<html lang="nl">`)
		assertNotContains(t, body, profileSubEN)
	})

	// A handler-supplied Heading/Message (not a {{t}} template string) must
	// also localize: an unknown verify token renders the invalid-link page.
	badToken := baseURL + "/verify-email?token=nonexistent-token"

	t.Run("verify-email invalid state defaults to English", func(t *testing.T) {
		t.Parallel()
		body := getBodyWithHeaderCookie(ctx, t, badToken, "", nil)
		assertContains(t, body, "Link is no longer valid")
		assertContains(t, body, `<html lang="en">`)
	})

	t.Run("verify-email invalid state renders Dutch handler message", func(t *testing.T) {
		t.Parallel()
		body := getBodyWithHeaderCookie(ctx, t, badToken, "", &http.Cookie{Name: "lang", Value: "nl"})
		assertContains(t, body, "Link is niet meer geldig")
		assertContains(t, body, `<html lang="nl">`)
		assertNotContains(t, body, "Link is no longer valid")
	})

	// The password-help text is a Go-side template func (not a {{t}} string);
	// under lang=nl the /profile/password page must render the Dutch copy.
	pwURL := baseURL + "/profile/password"

	t.Run("profile password page defaults to English help", func(t *testing.T) {
		t.Parallel()
		body := getBodyWithClientCookie(ctx, t, authn, pwURL, nil)
		assertContains(t, body, "Must be")
	})

	t.Run("profile password page renders Dutch help", func(t *testing.T) {
		t.Parallel()
		body := getBodyWithClientCookie(ctx, t, authn, pwURL, &http.Cookie{Name: "lang", Value: "nl"})
		assertContains(t, body, "tekens lang zijn")
		assertContains(t, body, `<html lang="nl">`)
		assertNotContains(t, body, "Must be")
	})
}

// getBodyWithClientCookie fetches target with the given (session-carrying)
// client and an optional extra cookie, returning the response body. The extra
// cookie rides alongside the client's jar cookies.
func getBodyWithClientCookie(
	ctx context.Context, t *testing.T, client *http.Client, target string, cookie *http.Cookie,
) string {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	defer closeBody(t, resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}

	return string(body)
}

// getBodyWithHeaderCookie fetches target with an optional Accept-Language
// header and an optional cookie, returning the response body. A fresh client
// per call keeps the subtests independent.
func getBodyWithHeaderCookie(
	ctx context.Context, t *testing.T, target, acceptLang string, cookie *http.Cookie,
) string {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	if acceptLang != "" {
		req.Header.Set("Accept-Language", acceptLang)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("client.Do err = %v, want nil", err)
	}
	defer closeBody(t, resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll err = %v, want nil", err)
	}

	return string(body)
}

func assertContains(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Errorf("body missing %q", want)
	}
}

func assertNotContains(t *testing.T, body, notWant string) {
	t.Helper()
	if strings.Contains(body, notWant) {
		t.Errorf("body unexpectedly contains %q", notWant)
	}
}
