package quiz_test

import (
	"slices"
	"testing"

	. "github.com/starquake/topbanana/internal/quiz"
)

func TestIsValidVisibility(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "public", input: VisibilityPublic, want: true},
		{name: "unlisted", input: VisibilityUnlisted, want: true},
		{name: "private", input: VisibilityPrivate, want: true},
		{name: "empty", input: "", want: false},
		{name: "unknown", input: "internal", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got, want := IsValidVisibility(tc.input), tc.want; got != want {
				t.Errorf("IsValidVisibility(%q) = %t, want %t", tc.input, got, want)
			}
		})
	}
}

func TestIsValidMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "solo", input: ModeSolo, want: true},
		{name: "live", input: ModeLive, want: true},
		{name: "empty", input: "", want: false},
		{name: "unknown", input: "team", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got, want := IsValidMode(tc.input), tc.want; got != want {
				t.Errorf("IsValidMode(%q) = %t, want %t", tc.input, got, want)
			}
		})
	}
}

func TestVisibilityValues(t *testing.T) {
	t.Parallel()

	got := VisibilityValues()
	want := []string{"public", "unlisted", "private"}
	if !slices.Equal(got, want) {
		t.Errorf("VisibilityValues() = %v, want %v", got, want)
	}
}

func TestModeValues(t *testing.T) {
	t.Parallel()

	got := ModeValues()
	want := []string{"solo", "live"}
	if !slices.Equal(got, want) {
		t.Errorf("ModeValues() = %v, want %v", got, want)
	}
}
