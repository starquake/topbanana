package handlers_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/handlers"
)

func TestWriteBudget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		size int64
		want time.Duration
	}{
		{name: "empty body gets the floor", size: 0, want: 10 * time.Second},
		{name: "negative size gets the floor", size: -1, want: 10 * time.Second},
		{name: "20 MB clip", size: 20 << 20, want: 10*time.Second + 320*time.Second},
		{name: "huge body is capped", size: 1 << 40, want: 30 * time.Minute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, want := handlers.WriteBudget(tc.size), tc.want; got != want {
				t.Errorf("WriteBudget(%d) = %v, want %v", tc.size, got, want)
			}
		})
	}
}

// deadlineRecorder records the write deadline a handler sets through
// [http.ResponseController].
type deadlineRecorder struct {
	*httptest.ResponseRecorder

	deadline time.Time
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.deadline = t

	return nil
}

func TestExtendWriteDeadline(t *testing.T) {
	t.Parallel()

	t.Run("sets the deadline to the size budget", func(t *testing.T) {
		t.Parallel()
		w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		before := time.Now()

		if err := handlers.ExtendWriteDeadline(w, 20<<20); err != nil {
			t.Fatalf("ExtendWriteDeadline err = %v, want nil", err)
		}

		if got, want := w.deadline.Sub(before), handlers.WriteBudget(20<<20); got < want {
			t.Errorf("deadline - now = %v, want at least %v", got, want)
		}
	})

	t.Run("unsupported writer returns an error", func(t *testing.T) {
		t.Parallel()
		err := handlers.ExtendWriteDeadline(httptest.NewRecorder(), 1)
		if got, want := err, http.ErrNotSupported; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}
