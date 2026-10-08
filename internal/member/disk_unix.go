//go:build !windows

package member

import (
	"fmt"
	"path/filepath"
	"syscall"
	"time"
)

// measureDisk reads the volume holding path: the bytes available to an
// unprivileged writer, the volume's size as df counts it, the free and total
// inodes, and the volume's name (its mount point).
func measureDisk(path string, now time.Time) (diskReading, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return diskReading{}, err
	}
	bsize := uint64(st.Bsize)
	free := uint64(st.Bavail) * bsize
	used := (uint64(st.Blocks) - uint64(st.Bfree)) * bsize
	return diskReading{At: now.UTC(), Path: path, Volume: diskMount(path),
		Free: free, Total: used + free,
		InodesFree: uint64(st.Ffree), InodesTotal: uint64(st.Files)}, nil
}

// diskMount is the mount point path lives on: the highest ancestor whose
// filesystem id is path's own.
func diskMount(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(abs, &st); err != nil {
		return abs
	}
	key := fmt.Sprint(st.Fsid)
	mount := abs
	for {
		parent := filepath.Dir(mount)
		if parent == mount {
			return mount
		}
		var p syscall.Statfs_t
		if err := syscall.Statfs(parent, &p); err != nil || fmt.Sprint(p.Fsid) != key {
			return mount
		}
		mount = parent
	}
}
