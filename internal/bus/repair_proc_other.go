//go:build !linux && !darwin

package bus

import (
	"fmt"
	"runtime"
)

// gitProcesses cannot see live git processes on this OS. The caller leaves the lock
// alone: removing it without knowing whether a git still owns the checkout is the
// dangerous direction.
func gitProcesses() ([]gitProc, error) {
	return nil, fmt.Errorf("cannot tell whether a git process owns this checkout on %s", runtime.GOOS)
}
