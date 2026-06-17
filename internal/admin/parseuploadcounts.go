package admin

import (
	"net/http"
	"strconv"
)

// uploadCountCeiling clamps the banner counts so a tampered URL can't paint
// an outrageous number (#951).
const uploadCountCeiling = 100

// parseUploadCounts pulls the post-upload banner counts out of the URL query.
// All three default to 0 and are clamped to uploadCountCeiling; a non-numeric
// or negative value is treated as 0 so a tampered query cannot paint a banner
// with a misleading number.
func parseUploadCounts(r *http.Request) (uploaded, failed, cancelled int) {
	return parseUploadCount(r, "uploaded"),
		parseUploadCount(r, "failed"),
		parseUploadCount(r, "cancelled")
}

func parseUploadCount(r *http.Request, name string) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	if n > uploadCountCeiling {
		return uploadCountCeiling
	}

	return n
}
