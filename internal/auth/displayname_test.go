package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/auth"
)

func TestCleanDisplayName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{name: "plain", input: "Alice", want: "Alice"},
		{name: "trims", input: "  Alice \t", want: "Alice"},
		{
			name:  "accents and emoji",
			input: "Ren\u00e9e \U0001F34C",
			want:  "Ren\u00e9e \U0001F34C",
		},
		{
			name:  "at the cap",
			input: strings.Repeat("\u00e9", auth.MaxDisplayNameLength),
			want:  strings.Repeat("\u00e9", auth.MaxDisplayNameLength),
		},
		{name: "empty", input: "", wantErr: auth.ErrDisplayNameEmpty},
		{name: "whitespace only", input: " \t\n", wantErr: auth.ErrDisplayNameEmpty},
		{
			name:    "one over the cap",
			input:   strings.Repeat("a", auth.MaxDisplayNameLength+1),
			wantErr: auth.ErrDisplayNameTooLong,
		},
		{
			name:    "zero-width space",
			input:   "Ali\u200bce",
			wantErr: auth.ErrDisplayNameInvalid,
		},
		{
			name:    "bidi override",
			input:   "\u202eAlice",
			wantErr: auth.ErrDisplayNameInvalid,
		},
		{name: "control character", input: "Ali\x07ce", wantErr: auth.ErrDisplayNameInvalid},
		{name: "embedded newline", input: "Ali\nce", wantErr: auth.ErrDisplayNameInvalid},
		{name: "invalid UTF-8", input: "Ali\xffce", wantErr: auth.ErrDisplayNameInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cleaned, err := auth.CleanDisplayName(tt.input)
			if tt.wantErr != nil {
				if got, want := err, tt.wantErr; !errors.Is(got, want) {
					t.Errorf("CleanDisplayName(%q) err = %v, want %v", tt.input, got, want)
				}

				return
			}
			if err != nil {
				t.Fatalf("CleanDisplayName(%q) err = %v, want nil", tt.input, err)
			}
			if got, want := cleaned, tt.want; got != want {
				t.Errorf("CleanDisplayName(%q) = %q, want %q", tt.input, got, want)
			}
		})
	}
}
