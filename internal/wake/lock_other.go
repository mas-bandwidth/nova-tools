//go:build !unix

package wake

import (
	"errors"
	"os"
	"path/filepath"
)

// The lock, where there is no flock: Windows, which this repo publishes a
// binary for. The advisory lock the unix build uses has no standard-library
// equivalent here, so the lock is an EXCLUSIVE CREATE of a sibling file. Same
// mutual exclusion, and it gives up the one property that makes flock better:
// the kernel does not drop it when the process dies, so a watch killed at its
// harness ceiling leaves the file behind and the next run refuses, naming it.
// That is a refusal a person can act on -- delete it -- rather than a
// corruption, and it is written down here rather than discovered.
func tryLockFile(f *os.File) (bool, error) {
	held, err := os.OpenFile(sentinel(f), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, err
	}
	return true, held.Close()
}

func unlockFile(f *os.File) { _ = os.Remove(sentinel(f)) }

func sentinel(f *os.File) string { return filepath.Clean(f.Name()) + ".held" }
