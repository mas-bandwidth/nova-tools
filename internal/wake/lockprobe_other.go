//go:build !unix

package wake

import (
	"errors"
	"os"
)

// On the SENTINEL builds -- internal/bus's lock_windows.go (tag windows) and
// lock_other.go (tag !unix && !windows), which are one protocol under two tags
// -- the lock is the O_EXCL sibling <path>.held, and the probe tests that
// path's existence with lstat and NEVER OPENS <path> ITSELF.
//
// FREE AND ABSENT ARE ONE FACT HERE, and the word this tool prints is `free`:
// held is the sentinel's existence and its absence is the only other state
// there is -- nobody holds it and there is no file -- so the three-word grammar
// has two words on these builds. The tool prints the one the caller is waiting
// for and never `absent`; no attempt is made to create the sentinel.
//
// A HELD CAN OUTLIVE ITS HOLDER here, because the kernel does not drop an
// O_EXCL file when the process that made it dies. That is the lock's own limit
// and it is this source's limit too: this tool never removes the sentinel, and
// never decides that a held has gone stale.
func probeLock(path string) (string, error) {
	info, err := os.Lstat(path + ".held")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "free", nil
		}
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("symlink at the lock path")
	}
	return "held", nil
}
