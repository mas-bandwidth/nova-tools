//go:build !windows

package main

import "syscall"

// diskFree is the bytes an unprivileged writer may still use on the volume holding path
// (statfs: the available blocks times the block size).
func diskFree(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// diskSize is the bytes of the volume holding path (statfs: all its blocks times the block
// size), which the slots' default cap is a tenth of (slotsCap).
func diskSize(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Blocks) * uint64(st.Bsize), nil
}
