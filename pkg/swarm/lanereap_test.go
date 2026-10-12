package swarm

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestReadWithinSkipsAReadThatDoesNotReturn pins the bound on a process record read
// (readWithin, lanereap_linux.go readEnviron): a read that returns is what it read; a read that
// has not returned when the bound fires is skipped, so a member's start never waits on a
// process stuck in the kernel; a read that fails is nil and not a skip. The bound is a channel
// the test fires itself: no real time.
func TestReadWithinSkipsAReadThatDoesNotReturn(t *testing.T) {
	t.Parallel()
	// a read that answers, with a bound that never fires
	got, ok := readWithin(func() ([]byte, error) { return []byte("PATH=/bin"), nil }, nil)
	require.True(t, ok)
	require.Equal(t, []byte("PATH=/bin"), got)
	// a read that fails: nothing read, and not a skip
	got, ok = readWithin(func() ([]byte, error) { return nil, errors.New("permission denied") }, nil)
	require.True(t, ok)
	require.Nil(t, got)
	// a read that does not return until the test lets it: the bound fires first, the read is skipped
	release := make(chan struct{})
	expired := make(chan time.Time, 1)
	expired <- time.Time{}
	got, ok = readWithin(func() ([]byte, error) { <-release; return []byte("late"), nil }, expired)
	require.False(t, ok, "a read that has not returned when the bound fires is skipped")
	require.Nil(t, got)
	close(release)
}
