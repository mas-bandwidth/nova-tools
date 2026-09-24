//go:build !unix

package launch

import (
	"errors"
	"time"
)

// startDetached refuses: a detached card wrapper needs POSIX setsid.
func startDetached(string, Line, time.Duration) (int, string, error) {
	return 0, "", errors.New("card launch needs POSIX setsid, which this OS does not have")
}
