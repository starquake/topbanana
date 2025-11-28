package must_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/starquake/topbanana/internal/must"
)

func TestOK(t *testing.T) {
	t.Parallel()
	t.Run("nil error does not panic", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("OK(nil) panicked unexpectedly: %v", r)
			}
		}()
		must.OK(nil)
	})

	t.Run("non-nil error panics", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if r := recover(); r == nil {
				t.Error("OK(err) did not panic")
			}
		}()
		must.OK(errors.New("test error"))
	})
}

func TestAny(t *testing.T) {
	t.Parallel()
	t.Run("returns value when error is nil", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			val  any
			err  error
		}{
			{"int", 42, nil},
			{"string", "hello", nil},
			{"bool", true, nil},
			{"slice", []int{1, 2, 3}, nil},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got := must.Any(tt.val, tt.err)
				if !reflect.DeepEqual(got, tt.val) {
					t.Errorf("Any(%v, nil) = %v; want %v", tt.val, got, tt.val)
				}
			})
		}
	})

	t.Run("panics when error is non-nil", func(t *testing.T) {
		t.Parallel()
		testErr := errors.New("test error")
		defer func() {
			r := recover()
			if r == nil {
				t.Error("Any(value, err) did not panic")
			}
		}()

		must.Any(42, testErr)
	})
}
