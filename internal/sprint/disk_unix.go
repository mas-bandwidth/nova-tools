//go:build !windows

package sprint

import (
	"fmt"
	"path/filepath"
	"syscall"
)

// statDisk reads the volume holding path: the bytes available to an
// unprivileged writer, the volume's size as df counts it (the blocks in use
// plus those available), the free and total inodes, and the volume's name (its
// mount point). A field the platform does not report is zero.
func statDisk(path string) (free, total, ifree, itotal uint64, volume string, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, 0, 0, "", err
	}
	bsize := uint64(st.Bsize)
	free = uint64(st.Bavail) * bsize
	used := (uint64(st.Blocks) - uint64(st.Bfree)) * bsize
	total = used + free
	ifree, itotal = uint64(st.Ffree), uint64(st.Files)
	return free, total, ifree, itotal, diskMount(path), nil
}

// diskMount is the mount point path lives on: the highest ancestor whose
// filesystem id is path's own. One statfs per path component, no mount table,
// so a volume is named the same way on every platform.
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
