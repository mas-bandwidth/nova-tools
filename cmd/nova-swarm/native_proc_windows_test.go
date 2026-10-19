//go:build windows

package main

import (
	"os"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// The native Windows member's three process facts (the native Windows members that take
// only GATE-NEEDS windows cards). These run on a Windows
// machine only (a Windows bench's native Go, or a Windows runner); Linux CI type-checks them by
// `make vet-windows`. No time, no socket, no child process.

// TestDiskFreeReadsTheVolumeOnWindows: the disk floor has a number to hold on Windows.
func TestDiskFreeReadsTheVolumeOnWindows(t *testing.T) {
	t.Parallel()

	free, err := diskFree(t.TempDir())
	require.NoError(t, err, "GetDiskFreeSpaceExW answers for a directory that exists")
	assert.Positive(t, free, "a volume a test can write a temp dir on has bytes free")
	_, err = diskFree(`Z:\no\such\volume\here`)
	assert.Error(t, err, "a path on no volume is an error, never a zero that reads as a full disk")
}

// TestLowerChildPriorityIsBelowNormal: the class is BELOW_NORMAL after the call. It is asked
// of this test process and put back, since a test starts no child.
func TestLowerChildPriorityIsBelowNormal(t *testing.T) {
	t.Parallel()

	self := windows.CurrentProcess()
	was, err := windows.GetPriorityClass(self)
	require.NoError(t, err)
	t.Cleanup(func() { _ = windows.SetPriorityClass(self, was) })

	require.NoError(t, lowerChildPriority(os.Getpid()))
	got, err := windows.GetPriorityClass(self)
	require.NoError(t, err)
	assert.Equal(t, uint32(windows.BELOW_NORMAL_PRIORITY_CLASS), got)
	assert.True(t, lowersEveryChild, "on Windows native lowers every child, walled or not")
}

// TestTheChildJobKillsOnClose: the job's base limit is JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE and
// no breakaway flag, read back from the kernel.
func TestTheChildJobKillsOnClose(t *testing.T) {
	t.Parallel()

	h, err := newKillOnCloseJob()
	require.NoError(t, err)
	defer windows.CloseHandle(h)

	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	require.NoError(t, windows.QueryInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil))
	flags := info.BasicLimitInformation.LimitFlags
	assert.NotZero(t, flags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, "kill on close is set")
	assert.Zero(t, flags&(windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK|windows.JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK), "no process may leave the job")
}
