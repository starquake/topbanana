package admin_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/starquake/topbanana/internal/admin"
)

func TestParseUploadCounts(t *testing.T) {
	t.Parallel()

	overCeiling := strconv.Itoa(admin.UploadCountCeiling + 5)

	cases := []struct {
		name         string
		query        string
		wantUploaded int
		wantFailed   int
	}{
		{name: "empty query", query: "", wantUploaded: 0, wantFailed: 0},
		{name: "uploaded only", query: "uploaded=3", wantUploaded: 3, wantFailed: 0},
		{name: "failed only", query: "failed=2", wantUploaded: 0, wantFailed: 2},
		{name: "both present", query: "uploaded=4&failed=1", wantUploaded: 4, wantFailed: 1},
		{name: "negative uploaded clamps to zero", query: "uploaded=-1&failed=2", wantUploaded: 0, wantFailed: 2},
		{
			name:         "non-numeric uploaded falls back to zero",
			query:        "uploaded=abc&failed=2",
			wantUploaded: 0,
			wantFailed:   2,
		},
		{
			name:         "non-numeric failed falls back to zero",
			query:        "uploaded=2&failed=oops",
			wantUploaded: 2,
			wantFailed:   0,
		},
		{
			name:         "uploaded over ceiling clamps",
			query:        "uploaded=" + overCeiling + "&failed=0",
			wantUploaded: admin.UploadCountCeiling,
			wantFailed:   0,
		},
		{
			name:         "failed over ceiling clamps",
			query:        "uploaded=0&failed=" + overCeiling,
			wantUploaded: 0,
			wantFailed:   admin.UploadCountCeiling,
		},
		{
			name: "exactly at ceiling passes through",
			query: "uploaded=" + strconv.Itoa(admin.UploadCountCeiling) +
				"&failed=" + strconv.Itoa(admin.UploadCountCeiling),
			wantUploaded: admin.UploadCountCeiling,
			wantFailed:   admin.UploadCountCeiling,
		},
		{
			name:         "blank values default to zero",
			query:        "uploaded=&failed=",
			wantUploaded: 0,
			wantFailed:   0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			target := "/admin/quizzes/1"
			if tc.query != "" {
				target += "?" + tc.query
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)

			gotUploaded, gotFailed := admin.ParseUploadCounts(req)
			if got, want := gotUploaded, tc.wantUploaded; got != want {
				t.Errorf("uploaded = %d, want %d", got, want)
			}
			if got, want := gotFailed, tc.wantFailed; got != want {
				t.Errorf("failed = %d, want %d", got, want)
			}
		})
	}
}
