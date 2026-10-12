package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTheMemberLockRefusesASecondHolderAndNamesIt pins the lock a member holds for its run
// (memberlock.go): the first taker of a name over a slots directory holds it; a second taker
// of the same name over the same slots is refused with the holder named and the way out, and
// the refusal reaps nothing (the caller never reaches the reap); another name over the same
// slots, and the same name over other slots, each take their own; once the first holder
// unlocks, the name is free again. The lock is a file under a temporary directory: no time,
// no process, no socket.
func TestTheMemberLockRefusesASecondHolderAndNamesIt(t *testing.T) {
	t.Parallel()
	slots := t.TempDir()
	first, err := takeMemberLock(slots, "m1")
	require.NoError(t, err)
	require.NotNil(t, first, "unix and windows have the lock")
	_, err = os.Stat(memberLockPath(slots, "m1"))
	require.NoError(t, err, "the lock is a file under the slots directory")

	second, err := takeMemberLock(slots, "m1")
	require.Error(t, err, "a second member of the name over the same slots is refused")
	require.Nil(t, second)
	require.Contains(t, err.Error(), "a member named m1 is running over "+slots+" already")
	require.Contains(t, err.Error(), "pid=", "the refusal names the holder")
	require.Contains(t, err.Error(), "stop the one that runs, or give this one its own --as or --root")

	other, err := takeMemberLock(slots, "m2")
	require.NoError(t, err, "another name over the same slots is its own lock")
	require.NoError(t, other.Unlock())
	elsewhere, err := takeMemberLock(t.TempDir(), "m1")
	require.NoError(t, err, "the same name over other slots is its own lock")
	require.NoError(t, elsewhere.Unlock())

	require.NoError(t, first.Unlock())
	again, err := takeMemberLock(slots, "m1")
	require.NoError(t, err, "the name is free once its holder let go")
	require.NoError(t, again.Unlock())
}

// TestMemberDirsAreAbsoluteBeforeTheyFormAMark pins memberDirs: a relative root, slots or
// results becomes absolute against the working directory, and slots and results default under
// the absolute root, so a lane's mark (its launch directory under slots) reads the same however
// the member was started.
func TestMemberDirsAreAbsoluteBeforeTheyFormAMark(t *testing.T) {
	t.Parallel()
	wd, err := os.Getwd()
	require.NoError(t, err)
	root, slots, results, err := memberDirs("r", "", "")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(wd, "r"), root)
	require.Equal(t, filepath.Join(wd, "r", "slots"), slots)
	require.Equal(t, filepath.Join(wd, "r", "results"), results)
	root, slots, results, err = memberDirs("r", "s/./x/", "res")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(wd, "r"), root)
	require.Equal(t, filepath.Join(wd, "s", "x"), slots, "a given slots is made absolute and clean")
	require.Equal(t, filepath.Join(wd, "res"), results)
	abs := t.TempDir()
	root, slots, results, err = memberDirs(abs, filepath.Join(abs, "lanes"), "")
	require.NoError(t, err)
	require.Equal(t, abs, root, "an absolute root is left as it is")
	require.Equal(t, filepath.Join(abs, "lanes"), slots)
	require.Equal(t, filepath.Join(abs, "results"), results)
}
