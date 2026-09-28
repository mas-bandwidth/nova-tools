//go:build darwin || linux

package life

import "syscall"

// freeBytes is the space the bench user may still write on path's filesystem.
func freeBytes(path string) (uint64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, false
	}
	return uint64(st.Bavail) * uint64(st.Bsize), true
}
