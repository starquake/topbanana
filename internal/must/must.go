// Package must contains functions that panic if an error is not nil
package must

// Must checks if an error is nil, if not it panics
// This is useful if you don't want to handle errors because it is not expected or doesn't need to be handled.
func Must(err error) {
	if err != nil {
		panic(err)
	}
}
