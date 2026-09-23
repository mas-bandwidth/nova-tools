//go:build windows

package sprinttable

import (
	"errors"
	"os/exec"
)

// ApplyOwnSession reports that this OS has no POSIX session to detach into.
// The table render still runs; only the own-session refresh is refused.
func ApplyOwnSession(cmd *exec.Cmd) error {
	return errors.New("own-session refresh needs POSIX setsid, which this OS does not have")
}
