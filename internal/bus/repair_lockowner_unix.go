//go:build unix

package bus

import (
	"os"
	"syscall"
)

// lockFileOwner is the owner uid of the index.lock that Lstat described. ok is false when
// the stat carries no uid.
func lockFileOwner(fi os.FileInfo) (uint32, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
}
