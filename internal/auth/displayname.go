package auth

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/starquake/topbanana/internal/locale"
)

// ErrDisplayNameTooLong is returned when a display name exceeds
// [MaxDisplayNameLength] runes.
var ErrDisplayNameTooLong = errors.New("displayName too long")

// ErrDisplayNameInvalid is returned when a display name contains a control,
// format or line/paragraph separator character (zero-width spaces, bidi
// overrides and the like).
var ErrDisplayNameInvalid = errors.New("displayName contains invalid characters")

// CleanDisplayName trims name and checks it against the rules every
// display-name entry point shares. It returns the trimmed name, or
// [ErrDisplayNameEmpty], [ErrDisplayNameTooLong] or [ErrDisplayNameInvalid].
func CleanDisplayName(name string) (string, error) {
	cleaned := strings.TrimSpace(name)
	if cleaned == "" {
		return "", ErrDisplayNameEmpty
	}
	if utf8.RuneCountInString(cleaned) > MaxDisplayNameLength {
		return cleaned, ErrDisplayNameTooLong
	}
	// Cf covers zero-width and bidi characters, which make look-alike names.
	if strings.ContainsFunc(cleaned, func(r rune) bool {
		return r == utf8.RuneError || unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp)
	}) {
		return cleaned, ErrDisplayNameInvalid
	}

	return cleaned, nil
}

// DisplayNameErrorMessage returns the localized form message for an error
// from [CleanDisplayName].
func DisplayNameErrorMessage(loc string, err error) string {
	switch {
	case errors.Is(err, ErrDisplayNameTooLong):
		return locale.TranslateCount(loc, "validation.displayNameTooLong", MaxDisplayNameLength)
	case errors.Is(err, ErrDisplayNameInvalid):
		return locale.Translate(loc, "validation.displayNameInvalid")
	default:
		return locale.Translate(loc, "validation.displayNameRequired")
	}
}
