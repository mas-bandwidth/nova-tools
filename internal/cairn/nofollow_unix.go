//go:build linux || darwin || freebsd || netbsd || openbsd

package cairn

import "syscall"

// noFollow makes an open refuse a symbolic link at the last name, so that a
// name swapped for a link between the check and the open creates and reads
// nothing through it.
const noFollow = syscall.O_NOFOLLOW
