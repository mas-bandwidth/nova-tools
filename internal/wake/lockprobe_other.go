//go:build !unix

package wake

import (
	"errors"
	"fmt"
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
	// WHAT IS AT <path> IS STILL A READING, even though the sentinel protocol
	// never opens it: a lock path replaced by a link into somebody else's file,
	// or by a directory, is not this lane's lock, and answering `free` about it
	// is a guess dressed as a reading. The unix probe refuses both at open time
	// -- O_NOFOLLOW and an fstat -- so the same two refusals are an lstat here
	// and the sentence a person reads is the same on every platform.
	//
	// ABSENT IS NOT ONE OF THEM. The sentinel protocol never creates <path>, so
	// nothing at all there is the ORDINARY case and the sentinel below is what
	// answers it; only something that IS there and is not a regular file is
	// unreadable. Nothing is created, replaced or removed to learn this.
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("symlink at the lock path")
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s at the lock path, not a regular file", kindOfMode(info.Mode()))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

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
