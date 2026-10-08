//go:build unix

package friend

import "syscall"

// Test fixtures keep their group leader unreaped until cleanup, pinning the
// numeric PGID. Production STOP never signals a group by numeric PGID.
func killGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) }
