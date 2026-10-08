package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// wakeIdentity uses the same Windows file index as os.SameFile (SPEC-BUS,
// wait; tla/WakeCursor.tla). A failed query is not a reusable identity.
func wakeIdentity(f *os.File, _ os.FileInfo) (string, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}

// openSafeWake opens the reparse point itself rather than following a raced
// link to a named pipe. The handle's type and identity are checked next.
func openSafeWake(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}
