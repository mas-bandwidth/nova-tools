package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The darwin log directory (diskguard.go, guardLogDir): the fleet's launchd agents write
// ~/Library/Logs/nova-*.log and ~/nova-bench/loops is empty there, so the default --logs on
// darwin is ~/Library/Logs; it is shared with every program of the login, so there only the
// nova- logs are rotated. Elsewhere the default stays ~/nova-bench/loops, every log the fleet's.
func TestDiskGuardDefaultLogDirIsLibraryLogsOnDarwin(t *testing.T) {
	t.Parallel()
	dir, prefix := guardLogDir("darwin")
	assert.Equal(t, "~/Library/Logs", dir)
	assert.Equal(t, "nova-", prefix)
	dir, prefix = guardLogDir("linux")
	assert.Equal(t, "~/nova-bench/loops", dir)
	assert.Empty(t, prefix)

	home := "/Users/someone"
	assert.Equal(t, "nova-", guardLogPrefix(home, filepath.Join(home, "Library", "Logs")))
	assert.Equal(t, "nova-", guardLogPrefix(home, filepath.Join(home, "Library", "Logs")+"/"), "named with a trailing slash")
	assert.Empty(t, guardLogPrefix(home, filepath.Join(home, "nova-bench", "loops")))
	assert.Equal(t, []string{"nova-loop-member", "nova-loop-reader"}, guardLogSkip(home, filepath.Join(home, "Library", "Logs")))
	assert.Empty(t, guardLogSkip(home, filepath.Join(home, "nova-bench", "loops")), "on Linux the canary reads the journal, so every loop log rotates")
}

func TestDiskGuardRotatesOnlyTheNovaLogsInLibraryLogs(t *testing.T) {
	t.Parallel()
	g, out := dgGuard(t)
	dir := filepath.Join(g.home, "Library", "Logs")
	friend := filepath.Join(dir, "nova-friend-f1.log")
	mirror := filepath.Join(dir, "nova-loop-mirror-refresh.log")
	other := filepath.Join(dir, "SomeOtherApp.log")
	member := filepath.Join(dir, "nova-loop-member-m1.log")
	reader := filepath.Join(dir, "nova-loop-reader-m1.log")
	for _, p := range []string{friend, mirror, other, member, reader} {
		dgFile(t, p, 150, dgNow)
	}
	g.logDir, g.logPrefix, g.logSkip, g.logMax, g.logKeep = dir, guardLogPrefix(g.home, dir), guardLogSkip(g.home, dir), 100, 2
	g.logs()

	// the tools play's canary counts the member log's lines and reads on from them: a
	// rotation between the two would make it read nothing, so the member and reader logs stay
	for _, p := range []string{member, reader} {
		fi, err := os.Stat(p)
		require.NoError(t, err)
		assert.EqualValues(t, 150, fi.Size(), "%s is read by the canary by line count: never rotated", p)
		assert.NoFileExists(t, p+".1")
	}

	for _, p := range []string{friend, mirror} {
		fi, err := os.Stat(p)
		require.NoError(t, err)
		assert.Zero(t, fi.Size(), "%s is the fleet's: rotated", p)
		assert.FileExists(t, p+".1")
	}
	fi, err := os.Stat(other)
	require.NoError(t, err)
	assert.EqualValues(t, 150, fi.Size(), "another program's log in ~/Library/Logs is never the guard's")
	assert.NoFileExists(t, other+".1")
	assert.Equal(t, "ROTATED log "+friend+" freed=0 size=150 keep=2\nROTATED log "+mirror+" freed=0 size=150 keep=2\n", out.String())
}
