//go:build windows

package check

import "errors"

// syscallMkfifo has no Windows counterpart: there is no syscall.Mkfifo there,
// and an unguarded reference to it would not compile, taking the whole
// package's test binary down on that platform rather than one test.
//
// Returning an error rather than panicking is what makes the caller skip: the
// fifo test treats "fifo unavailable" as a skip, so the property is declared
// unobservable here instead of silently untested.
func syscallMkfifo(path string) error {
	return errors.New("named pipes are not available on windows")
}
