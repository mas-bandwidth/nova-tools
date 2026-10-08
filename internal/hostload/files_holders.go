//go:build darwin || linux

package hostload

import "fmt"

// HoldersTimedOut is the reason a holders' read that ran past HoldersTimeout gives.
func HoldersTimedOut(tool string) error {
	return fmt.Errorf("%s timed out after %s", tool, HoldersTimeout)
}
