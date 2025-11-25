// Package must contains functions that panic if an error is not nil
package must

// OK checks if an error is nil, if not it panics.
// This is useful if you don't want to handle errors because it is not expected or doesn't need to be handled.
func OK(err error) {
	if err != nil {
		panic(err)
	}
}

// Any returns the value if the error is nil, otherwise it panics
//
//nolint:ireturn // Returning interface{} is fine here because it is a generic function
func Any[T any](ret T, err error) T {
	if err != nil {
		panic(err)
	}

	return ret
}
