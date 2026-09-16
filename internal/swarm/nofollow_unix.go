//go:build !windows

package swarm

import "syscall"

// oNoFollow refuses a symlink at the final path component; oNonBlock makes the open of a
// FIFO return at once instead of waiting for a writer that is never coming. Windows has
// neither, so there both are zero and the Lstat and the fstat carry the rule.
const (
	oNoFollow = syscall.O_NOFOLLOW
	oNonBlock = syscall.O_NONBLOCK
)

// ONoFollow is oNoFollow for callers outside this package: `native` opens a log inside the
// JOB, which is the worker's own writable directory, and a symlink planted there by a card
// would carry that write out of the wall (security#30's class, issue #608).
const ONoFollow = oNoFollow
