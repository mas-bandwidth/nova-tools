//go:build windows

package main

import "golang.org/x/sys/windows"

// diskFree is the bytes the calling user may still write on the volume holding path:
// GetDiskFreeSpaceExW's FreeBytesAvailableToCaller, which honours per-user quotas, the
// Windows twin of statfs's available blocks (diskfree_unix.go). A native Windows member
// (a gates_only machine row) holds the disk floor by it as a bench does.
func diskFree(path string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &free); err != nil {
		return 0, err
	}
	return avail, nil
}
