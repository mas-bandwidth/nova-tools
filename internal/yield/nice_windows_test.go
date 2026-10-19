//go:build windows

package yield

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// TestToCIPutsThisProcessBelowNormalOnWindows: ToCI is the member's and native's step
// behind CI; on Windows it is the BELOW_NORMAL priority class, read back from the kernel,
// and the member no longer refuses to start (Supported). The class is put back after.
func TestToCIPutsThisProcessBelowNormalOnWindows(t *testing.T) {
	t.Parallel()
	self := windows.CurrentProcess()
	was, err := windows.GetPriorityClass(self)
	require.NoError(t, err)
	t.Cleanup(func() { _ = windows.SetPriorityClass(self, was) })

	assert.True(t, Supported, "a Windows member runs its cards behind CI")
	require.NoError(t, ToCI())
	got, err := windows.GetPriorityClass(self)
	require.NoError(t, err)
	assert.Equal(t, uint32(windows.BELOW_NORMAL_PRIORITY_CLASS), got)

	require.NoError(t, setNice(0))
	got, err = windows.GetPriorityClass(self)
	require.NoError(t, err)
	assert.Equal(t, uint32(windows.NORMAL_PRIORITY_CLASS), got, "nice 0 is the normal class")
}
