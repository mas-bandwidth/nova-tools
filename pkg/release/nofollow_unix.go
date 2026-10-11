//go:build unix

package release

import "syscall"

// oNoFollow refuses a symlink as the final path component. Platforms without
// the flag leave it zero; writeNoFollow still refuses a symlink Lstat can see.
const oNoFollow = syscall.O_NOFOLLOW
