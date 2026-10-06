//go:build unix

package friend

import "golang.org/x/sys/unix"

// volumeCapacity reads capacity available to this user (SPEC-FRIEND, jobs capacity).
func volumeCapacity(dir string) (int64, *int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, nil, err
	}
	var inodes *int64
	if st.Files > 0 {
		n := int64(st.Ffree)
		inodes = &n
	}
	return int64(st.Bavail) * int64(st.Bsize), inodes, nil
}
