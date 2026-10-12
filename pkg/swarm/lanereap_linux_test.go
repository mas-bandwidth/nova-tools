//go:build linux

package swarm

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReadEnvironAsksAStuckPidOnce pins the stuck set of one reap (readEnviron): a pid whose
// environ read ran past its bound is in the set and is never asked again in that reap, so a
// process stuck in the kernel costs a reap one bound and not one per round; a pid not in the
// set is read (this process's own record, the one read that spawns nothing). A nil set asks
// every time.
func TestReadEnvironAsksAStuckPidOnce(t *testing.T) {
	t.Parallel()
	self := os.Getpid()
	ents := readEnviron(self, map[int]bool{})
	require.NotEmpty(t, ents, "this process's own /proc/self/environ reads")
	require.Nil(t, readEnviron(self, map[int]bool{self: true}), "a pid the reap found stuck is not asked again")
	require.NotEmpty(t, readEnviron(self, nil), "a nil set records nothing and asks every time")
	// a process that is gone, or whose record this user may not read, is nil and not stuck
	stuck := map[int]bool{}
	require.Nil(t, readEnviron(1<<22+self, stuck))
	require.Empty(t, stuck, "a read that failed is not a read that ran out")
}
