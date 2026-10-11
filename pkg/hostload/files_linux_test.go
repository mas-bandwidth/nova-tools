//go:build linux

package hostload

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestProcHoldersCountsEachProcessDescriptors: a /proc tree's processes, each with its
// fd entries counted, its comm and its owner; a directory that is no pid, and a process
// with no fd directory (another user's, or gone), are none. A walk past its deadline
// stops and says so.
func TestProcHoldersCountsEachProcessDescriptors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mk := func(pid, comm string, fds int) {
		dir := filepath.Join(root, pid)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "fd"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644))
		for i := range fds {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "fd", string(rune('0'+i))), nil, 0o644))
		}
	}
	mk("200", "redis-server", 3)
	mk("300", "node", 5)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sys"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "400"), 0o755))
	me, err := user.Current()
	require.NoError(t, err)
	hs, err := ProcHolders(root, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, []Holder{
		{PID: 300, Command: "node", User: me.Username, Open: 5},
		{PID: 200, Command: "redis-server", User: me.Username, Open: 3},
	}, TopHolders(hs, FilesTop))
	_, err = ProcHolders(root, time.Now().Add(-time.Second))
	require.ErrorContains(t, err, "timed out")
}

// TestLocalReadsThisMachineOpenFiles: this machine's count and limit read from /proc,
// and its own process among the holders.
func TestLocalReadsThisMachineOpenFiles(t *testing.T) {
	t.Parallel()
	src := Local()
	open, limit, err := src.OpenFiles()
	require.NoError(t, err)
	require.Positive(t, open)
	require.Positive(t, limit)
	hs, err := src.Holders()
	require.NoError(t, err)
	found := false
	for _, h := range hs {
		found = found || h.PID == os.Getpid()
	}
	require.True(t, found, "this test's own process holds descriptors")
}

// TestParseFileNr: Linux's /proc/sys/fs/file-nr is allocated, unused and the maximum.
func TestParseFileNr(t *testing.T) {
	t.Parallel()
	open, maxFiles, ok := ParseFileNr("15139\t0\t9223372036854775807\n")
	require.True(t, ok)
	require.Equal(t, 15139, open)
	require.Equal(t, 9223372036854775807, maxFiles)
	_, _, ok = ParseFileNr("x 0 1\n")
	require.False(t, ok)
	_, _, ok = ParseFileNr("")
	require.False(t, ok)
}
