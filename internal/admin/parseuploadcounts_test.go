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
		name          string
		query         string
		wantUploaded  int
		wantFailed    int
		wantCancelled int
	}{
		{name: "empty query", query: ""},
		{name: "uploaded only", query: "uploaded=3", wantUploaded: 3},
		{name: "failed only", query: "failed=2", wantFailed: 2},
		{name: "cancelled only", query: "cancelled=4", wantCancelled: 4},
		{
			name:          "all three present",
			query:         "uploaded=6&failed=1&cancelled=3",
			wantUploaded:  6,
			wantFailed:    1,
			wantCancelled: 3,
		},
		{name: "negative uploaded clamps to zero", query: "uploaded=-1&failed=2", wantFailed: 2},
		{
			name:       "non-numeric uploaded falls back to zero",
			query:      "uploaded=abc&failed=2",
			wantFailed: 2,
		},
		{
			name:         "non-numeric failed falls back to zero",
			query:        "uploaded=2&failed=oops",
			wantUploaded: 2,
		},
		{
			name:          "non-numeric cancelled falls back to zero",
			query:         "uploaded=1&failed=1&cancelled=oops",
			wantUploaded:  1,
			wantFailed:    1,
			wantCancelled: 0,
		},
		{
			name:         "uploaded over ceiling clamps",
			query:        "uploaded=" + overCeiling + "&failed=0",
			wantUploaded: admin.UploadCountCeiling,
		},
		{
			name:       "failed over ceiling clamps",
			query:      "uploaded=0&failed=" + overCeiling,
			wantFailed: admin.UploadCountCeiling,
		},
		{
			name:          "cancelled over ceiling clamps",
			query:         "cancelled=" + overCeiling,
			wantCancelled: admin.UploadCountCeiling,
		},
		{
			name: "exactly at ceiling passes through",
			query: "uploaded=" + strconv.Itoa(admin.UploadCountCeiling) +
				"&failed=" + strconv.Itoa(admin.UploadCountCeiling) +
				"&cancelled=" + strconv.Itoa(admin.UploadCountCeiling),
			wantUploaded:  admin.UploadCountCeiling,
			wantFailed:    admin.UploadCountCeiling,
			wantCancelled: admin.UploadCountCeiling,
		},
		{name: "blank values default to zero", query: "uploaded=&failed=&cancelled="},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			target := "/admin/quizzes/1"
			if tc.query != "" {
				target += "?" + tc.query
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)

			gotUploaded, gotFailed, gotCancelled := admin.ParseUploadCounts(req)
			if got, want := gotUploaded, tc.wantUploaded; got != want {
				t.Errorf("parseUploadCounts(%q) uploaded = %d, want %d", tc.query, got, want)
			}
			if got, want := gotFailed, tc.wantFailed; got != want {
				t.Errorf("parseUploadCounts(%q) failed = %d, want %d", tc.query, got, want)
			}
			if got, want := gotCancelled, tc.wantCancelled; got != want {
				t.Errorf("parseUploadCounts(%q) cancelled = %d, want %d", tc.query, got, want)
			}
		})
	}
}
