package friend

import "golang.org/x/sys/windows"

// volumeCapacity reports bytes; Windows reports no inode headroom (SPEC-FRIEND).
func volumeCapacity(dir string) (int64, *int64, error) {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, nil, err
	}
	var available, total, free uint64
	err = windows.GetDiskFreeSpaceEx(path, &available, &total, &free)
	return int64(available), nil, err
}
