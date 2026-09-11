//go:build !unix

package tokens

import (
	"errors"
	"os"
	"path/filepath"
)

// The lock where there is no flock: Windows, which this repo publishes a binary for.
// LockFileEx lives outside the standard library, so the lock is an EXCLUSIVE CREATE of a
// sibling file. Same mutual exclusion; it gives up the property that makes flock better —
// the kernel does not drop it when the process dies — so a fold killed while holding it
// leaves the file behind and the next fold waits its budget and refuses, naming the file.
// That is a refusal a person can act on rather than a corruption.
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
