package demo_test

import (
	"testing"

	"github.com/starquake/topbanana/internal/demo"
)

func TestEnabled(t *testing.T) {
	tests := []struct {
		name string
		set  bool
		val  string
		want bool
	}{
		{name: "unset", set: false, want: false},
		{name: "true", set: true, val: "true", want: true},
		{name: "1", set: true, val: "1", want: true},
		{name: "false", set: true, val: "false", want: false},
		{name: "garbage", set: true, val: "yes-please", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("DEMO_MODE_ENABLED", tc.val)
			} else {
				t.Setenv("DEMO_MODE_ENABLED", "")
			}
			if got, want := demo.Enabled(), tc.want; got != want {
				t.Errorf("Enabled() = %v, want %v", got, want)
			}
		})
	}
}
