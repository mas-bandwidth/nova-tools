//go:build windows

package yield

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// Supported: Windows has a priority class, and a native Windows member (a gates_only
// machine row) runs its cards behind CI by it.
const Supported = true

// setNice puts this process at BELOW_NORMAL_PRIORITY_CLASS for any n above 0 (Windows has
// classes, not nice values; BELOW_NORMAL is the class below the CI runner's NORMAL), and at
// NORMAL for 0. A priority class belongs to the process, every thread at once, and a child
// started afterwards inherits BELOW_NORMAL (CreateProcess, the priority class flags), so
// the job's whole tree runs behind CI as the unix nice does.
func setNice(n int) error {
	class := uint32(windows.NORMAL_PRIORITY_CLASS)
	if n > 0 {
		class = windows.BELOW_NORMAL_PRIORITY_CLASS
	}
	if err := windows.SetPriorityClass(windows.CurrentProcess(), class); err != nil {
		return fmt.Errorf("SetPriorityClass: %w", err)
	}
	return nil
}
