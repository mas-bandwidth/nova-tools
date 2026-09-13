//go:build !windows

package bus

import "syscall"

// oNoFollow is the open flag that refuses a symlink at the final path component. Windows
// has no such flag, so there it is zero and the Lstat and the fstat carry the rule alone.
const oNoFollow = syscall.O_NOFOLLOW
