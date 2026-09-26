//go:build !unix

package bus

import "os"

// lockFileOwner has no uid to read on this OS, so every index.lock's owner is unknown
// and ClearStaleIndexLock refuses rather than judging it.
func lockFileOwner(os.FileInfo) (uint32, bool) {
	return 0, false
}
