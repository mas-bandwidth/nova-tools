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
