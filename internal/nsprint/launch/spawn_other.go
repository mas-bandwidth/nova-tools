//go:build !unix

package launch

import "errors"

// startDetached refuses: a detached card wrapper needs POSIX setsid.
func startDetached(string, Line) (int, error) {
	return 0, errors.New("card launch needs POSIX setsid, which this OS does not have")
}
