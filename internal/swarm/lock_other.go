//go:build !unix

package swarm

import (
	"errors"
	"os"
	"path/filepath"
)

// The lock where there is no flock: Windows, which this repo publishes a binary for. An
// exclusive create of a sibling file gives the same mutual exclusion and gives up the one
// property that makes flock better -- the kernel does not drop it when the holder dies, so
// a dispatcher killed holding it leaves the file behind and the next run refuses, naming
// it. That is a refusal a person can act on rather than a corruption, and it is written
// down here rather than discovered.
func tryLockFile(f *os.File) (bool, error) {
	held, err := os.OpenFile(sentinel(f), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) || errors.Is(err, os.ErrPermission) {
			return false, nil
		}
		return false, err
	}
	return true, held.Close()
}

func unlockFile(f *os.File) { _ = os.Remove(sentinel(f)) }

func sentinel(f *os.File) string { return filepath.Clean(f.Name()) + ".held" }
