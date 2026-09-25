package handlers

import (
	"fmt"
	"net/http"
	"time"
)

// Write budget for a sized response body: a floor plus the time the body takes
// at a slow phone-on-venue-Wi-Fi rate, capped so a stalled client cannot hold
// a connection indefinitely.
const (
	writeBudgetFloor       = 10 * time.Second
	writeBudgetMinRate     = 64 << 10 // bytes per second
	writeBudgetMaxDuration = 30 * time.Minute
)

// WriteBudget returns how long a response carrying size bytes may take to
// write.
func WriteBudget(size int64) time.Duration {
	budget := writeBudgetFloor + time.Duration(max(size, 0)/writeBudgetMinRate)*time.Second

	return min(budget, writeBudgetMaxDuration)
}

// ExtendWriteDeadline moves w's write deadline to fit a size-byte body, since
// the server-wide WriteTimeout counts from the request read and would cut a
// large download off part way.
func ExtendWriteDeadline(w http.ResponseWriter, size int64) error {
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(WriteBudget(size))); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}

	return nil
}
