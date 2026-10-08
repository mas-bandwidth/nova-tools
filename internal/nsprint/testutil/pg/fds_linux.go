//go:build linux

package pg

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// A coordinator may have passed its flock descriptor to the test binary.
// Its own lock stays open, but none of this fixture's children inherit it.
func closeInheritedFDsOnExec() error {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return fmt.Errorf("inspect test descriptors: %w", err)
	}
	for _, e := range entries {
		fd, err := strconv.Atoi(e.Name())
		if err == nil && fd > 2 {
			syscall.CloseOnExec(fd)
		}
	}
	return nil
}
