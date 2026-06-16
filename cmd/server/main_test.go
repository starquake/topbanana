package main

import "testing"

func TestTooManyModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		modes []bool
		want  bool
	}{
		{name: "none set", modes: []bool{false, false, false, false}, want: false},
		{name: "one set", modes: []bool{true, false, false, false}, want: false},
		{name: "two set", modes: []bool{true, true, false, false}, want: true},
		{name: "all set", modes: []bool{true, true, true, true}, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, want := tooManyModes(tc.modes...), tc.want; got != want {
				t.Errorf("tooManyModes(%v) = %v, want %v", tc.modes, got, want)
			}
		})
	}
}
