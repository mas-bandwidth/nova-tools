//go:build !unix

package merge

import (
	"errors"
	"os"
	"path/filepath"
)

// The lock where there is no flock: Windows, which this repo publishes a binary for.
// LockFileEx lives in golang.org/x/sys and this repo is standard library only, so the
// lock is an EXCLUSIVE CREATE of a sibling file.
//
// It gives the same mutual exclusion and gives up the property rule 2 is built on: the
// kernel does not drop it when the process dies, so a run killed holding it leaves the
// file behind and the next run waits its whole budget and then refuses, naming the file.
// That is a refusal a person can act on -- delete it -- rather than a corruption, and it
// is written down here rather than discovered. nova-bus's lock_other.go is the same
// trade for the same reason.
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
