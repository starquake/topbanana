package htmx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/starquake/topbanana/internal/htmx"
)

func TestIsRequest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		header string
		value  string
		want   bool
	}{
		{name: "no header", want: false},
		{name: "wire-case true", header: "HX-Request", value: "true", want: true},
		{name: "canonical true", header: "Hx-Request", value: "true", want: true},
		{name: "false value", header: "Hx-Request", value: "false", want: false},
		{name: "title-case true is rejected", header: "Hx-Request", value: "True", want: false},
		{name: "empty value", header: "Hx-Request", value: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set(tc.header, tc.value)
			}
			if got, want := htmx.IsRequest(req), tc.want; got != want {
				t.Errorf("IsRequest(%q=%q) = %v, want %v", tc.header, tc.value, got, want)
			}
		})
	}
}
