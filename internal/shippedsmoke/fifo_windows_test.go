//go:build shippedsmoke && windows

package shippedsmoke

import "errors"

// makeFifo has no Windows form; the assertions that need one skip there.
func makeFifo(string) error { return errors.New("no named pipes on Windows") }
