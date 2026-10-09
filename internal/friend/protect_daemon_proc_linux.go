//go:build linux

package friend

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// ProtectDaemonProcEnvironment makes the daemon's proc files inaccessible to
// same-UID lane children before the first native turn. Landlock must still
// expose /proc for the child and its toolchain (docs/SPEC-SANDBOX.md).
func ProtectDaemonProcEnvironment() error {
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("set Linux daemon nondumpable: %w", err)
	}
	return nil
}
