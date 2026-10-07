//go:build !windows

package sprint

import "syscall"

// GCVolumeUse is the use, in percent, of the volume holding path, as df counts it: the
// blocks in use over those in use and those an unprivileged writer may still use, rounded
// up.
func GCVolumeUse(path string) (int, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	used := uint64(st.Blocks) - uint64(st.Bfree)
	all := used + uint64(st.Bavail)
	if all == 0 {
		return 0, nil
	}
	return int((used*100 + all - 1) / all), nil
}
