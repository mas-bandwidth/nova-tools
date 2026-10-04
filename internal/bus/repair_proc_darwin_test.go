//go:build darwin

package bus

import (
	"errors"
	"github.com/stretchr/testify/require"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// lsof failing must not become an empty cwd. A git in the ps list with no -C is then
// invisible, and an old lock would be removed while that git still owns the checkout.
func TestDarwinLsofFailureLeavesOldLock(t *testing.T) {
	t.Parallel()
	hermetic(t)
	dir, lock := oldIndexLock(t)
	_, err := gitProcsFromPS("501 77 git cat-file --batch\n", "501", nil, errors.New("lsof failed"), func(string) (bool, error) {
		require.FailNow(t, "lsof failure was treated as a pid to re-check, not as an unreadable cwd")
		return false, nil
	})
	require.Error(t, err, "lsof failure with a cwd git and no -C was a complete scan")
	cleared, cerr := clearStaleIndexLock(dir, time.Now(), func() ([]gitProc, error) {
		return nil, err
	})
	assertLockKept(t, lock, cleared, cerr)
}

// A pid that is gone between ps and lsof is not an owner. A pid that is still there
// and has no cwd is an incomplete scan. The two are not the same answer.
func TestDarwinVanishedGitIsNotAnUnreadableCwd(t *testing.T) {
	t.Parallel()

	procs, err := gitProcsFromPS("501 77 git status\n", "501", map[string]string{}, nil, func(string) (bool, error) {
		return false, nil
	})
	require.NoError(t, err, "vanished git: procs=%+v err=%v, want none and no error", procs, err)
	require.Equal(t, 0, len(procs), "vanished git: procs=%+v err=%v, want none and no error", procs, err)
	_, err = gitProcsFromPS("501 77 git status\n", "501", map[string]string{}, nil, func(string) (bool, error) {
		return true, nil
	})
	require.False(t, err == nil || !strings.HasPrefix(err.Error(), ownershipUnknown), "still-present git with no cwd: err=%v", err)
	require.False(t, strings.Contains(err.Error(), "\n") || len(err.Error()) > ownershipDiagCap, "diagnostic is not bounded: %q", err)
}

// #3029 on the Studio: lsof -c git exits 1 with no output when no git is running at the
// moment it looks. A git ps listed a moment earlier and which has since exited is then
// the only git in the scan, and "lsof found nothing" was read as "lsof failed", which
// refuses every wait that meets it. No match is a complete, empty answer: each ps-listed
// git missing from it is re-checked by pid, exactly as a pid lsof did not list. A real
// lsof failure (a message, any other code) is still a failure.
func TestDarwinLsofNoMatchIsAnEmptyScan(t *testing.T) {
	t.Parallel()
	hermetic(t)
	cwds, err := lsofCwds("", "", 1, errors.New("exit status 1"))
	require.NoError(t, err, "lsof with no git to match: cwds=%v err=%v, want an empty map and no error", cwds, err)
	require.Equal(t, 0, len(cwds), "lsof with no git to match: cwds=%v err=%v, want an empty map and no error", cwds, err)
	procs, err := gitProcsFromPS("501 77 git rev-list --objects --stdin --not --all\n", "501", cwds, err, func(string) (bool, error) {
		return false, nil
	})
	require.NoError(t, err, "a git gone between ps and lsof: procs=%+v err=%v, want none and no error", procs, err)
	require.Equal(t, 0, len(procs), "a git gone between ps and lsof: procs=%+v err=%v, want none and no error", procs, err)
	dir, lock := oldIndexLock(t)
	cleared, cerr := clearStaleIndexLock(dir, time.Now(), func() ([]gitProc, error) { return procs, err })
	require.NoError(t, cerr, "stale lock with only a vanished git: cleared=%v err=%v, want removed", cleared, cerr)
	require.True(t, cleared, "stale lock with only a vanished git: cleared=%v err=%v, want removed", cleared, cerr)
	{
		_, statErr := os.Lstat(lock)
		require.True(t, os.IsNotExist(statErr), "stale index.lock still present: %v", statErr)
	}

	for _, c := range []struct {
		stdout, stderr string
		code           int
	}{
		{"", "lsof: WARNING: can't stat() nfs file system /Volumes/x\n", 1},
		{"", "", 2},
		{"p77\nn/somewhere\n", "", 1},
	} {
		{
			_, err := lsofCwds(c.stdout, c.stderr, c.code, errors.New("exit status"))
			if err == nil {
				require.Error(t, err, "lsof stdout=%q stderr=%q code=%d was read as a complete scan", c.stdout, c.stderr, c.code)
			}
		}
	}
	got, err := lsofCwds("p77\nfcwd\nn/bus\np78\nfcwd\nn/other\n", "", 0, nil)
	require.NoError(t, err, "lsof parse: %v %v", got, err)
	require.Equal(t, "/bus", got["77"], "lsof parse: %v %v", got, err)
	require.Equal(t, "/other", got["78"], "lsof parse: %v %v", got, err)
}

// #3029, the second darwin window: a git ps listed has exited by the time lsof looks, but
// its parent has not reaped it yet. lsof has no cwd for a zombie, and `ps -p` still finds
// the pid, so the scan called it a live git with an unreadable cwd and refused the wait
// (seen 1 in 50 under -race on the Studio). A zombie is dead, as procStatDead already says
// on Linux. A live state is still alive, and a vanished pid is still gone.
func TestDarwinZombieGitIsNotALiveGit(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		out   string
		alive bool
	}{
		{"Z\n", false},
		{"Z+\n", false},
		{" Zs \n", false},
		{"S\n", true},
		{"R+\n", true},
		{"U\n", true},
		{"", false},
	} {
		{
			got := darwinStatAlive(c.out)
			if got != c.alive {
				require.Equal(t, c.alive, got, "ps stat %q: alive=%v, want %v", c.out, got, c.alive)
			}
		}
	}
	procs, err := gitProcsFromPS("501 77 git index-pack --stdin --fix-thin\n", "501", map[string]string{}, nil, func(string) (bool, error) {
		return darwinStatAlive("Z+\n"), nil
	})
	require.NoError(t, err, "a zombie git with no cwd: procs=%+v err=%v, want none and no error", procs, err)
	require.Equal(t, 0, len(procs), "a zombie git with no cwd: procs=%+v err=%v, want none and no error", procs, err)
}

// CI run 36268521205, darwin shard on studio-nova-2: the runner account `nova` listed the
// coordinator's gits with ps, lsof as `nova` could not read their cwds, and every wait
// refused with "cwd unreadable pid=N". Another account's git is neither an owner nor an
// unknown: its row is skipped before its cwd is looked up, and the caller's own git is
// still placed by its cwd.
func TestGitScanSkipsAnotherAccountsGit(t *testing.T) {
	t.Parallel()
	var asked []string
	alive := func(pid string) (bool, error) {
		asked = append(asked, pid)
		return true, nil
	}
	ps := "502 18772 git status\n501 77 git fetch -q origin\n"
	procs, err := gitProcsFromPS(ps, "501", map[string]string{"77": "/Users/nova/bus"}, nil, alive)
	require.NoError(t, err, "another account's git with no cwd beside our own: procs=%+v err=%v, want theirs flagged foreign, ours placed, and no error", procs, err)
	require.Equal(t, 2, len(procs), "another account's git with no cwd beside our own: procs=%+v err=%v, want theirs flagged foreign, ours placed, and no error", procs, err)
	require.True(t, procs[0].foreign, "another account's git with no cwd beside our own: procs=%+v err=%v, want theirs flagged foreign, ours placed, and no error", procs, err)
	require.Equal(t, "git status", procs[0].command, "another account's git with no cwd beside our own: procs=%+v err=%v, want theirs flagged foreign, ours placed, and no error", procs, err)
	{
		own := placed(procs)
		require.Equal(t, 1, len(own), "our own git was not placed by its cwd: %+v", procs)
		require.True(t, own[0].cwdKnown, "our own git was not placed by its cwd: %+v", procs)
		require.Equal(t, "/Users/nova/bus", own[0].cwd, "our own git was not placed by its cwd: %+v", procs)
		require.Equal(t, "git fetch -q origin", own[0].command, "our own git was not placed by its cwd: %+v", procs)
	}
	require.Equal(t, 0, len(asked), "another account's pid reached the liveness check: %v", asked)
}

// The narrowing is by account, not a licence: our own git that is alive and whose cwd
// lsof did not give is still an incomplete scan, with the same sentence as before.
func TestGitScanStillRefusesOwnUnreadableCwd(t *testing.T) {
	t.Parallel()
	_, err := gitProcsFromPS("502 18772 git status\n501 19050 git status\n", "501", map[string]string{}, nil, func(string) (bool, error) {
		return true, nil
	})
	const want = "cannot tell whether a git process owns this checkout: cwd unreadable pid=19050"
	require.Error(t, err, "our own live git with no cwd: err=%v, want %q", err, want)
	require.Equal(t, want, err.Error(), "our own live git with no cwd: err=%v, want %q", err, want)
}

// The wait's own repair path with the scan the CI runner saw: a stale lock in a checkout
// this account owns, and a live git of another account with no readable cwd. The lock
// is removed; the same row as this account's keeps it.
func TestStaleLockClearsBesideAnotherAccountsGit(t *testing.T) {
	t.Parallel()
	hermetic(t)
	self := strconv.Itoa(os.Geteuid())
	other := strconv.Itoa(os.Geteuid() + 1)
	alive := func(string) (bool, error) { return true, nil }

	dir, lock := oldIndexLock(t)
	rep, err := clearStaleIndexLockReport(dir, time.Now(), func() ([]gitProc, error) {
		return gitProcsFromPS(other+" 18772 git status\n", self, map[string]string{}, nil, alive)
	}, indexLockOwner, effectiveUID())
	require.NoError(t, err, "stale lock beside another account's git: report=%+v err=%v, want removed with foreign=1", rep, err)
	require.True(t, rep.Cleared, "stale lock beside another account's git: report=%+v err=%v, want removed with foreign=1", rep, err)
	require.True(t, rep.Scanned, "stale lock beside another account's git: report=%+v err=%v, want removed with foreign=1", rep, err)
	require.Equal(t, LockScan{Foreign: 1}, rep.Scan, "stale lock beside another account's git: report=%+v err=%v, want removed with foreign=1", rep, err)
	{
		_, statErr := os.Lstat(lock)
		require.True(t, os.IsNotExist(statErr), "stale index.lock still present: %v", statErr)
	}

	ours, oursLock := oldIndexLock(t)
	cleared, err := clearStaleIndexLock(ours, time.Now(), func() ([]gitProc, error) {
		return gitProcsFromPS(self+" 18772 git status\n", self, map[string]string{}, nil, alive)
	})
	assertLockKept(t, oursLock, cleared, err)
}

// The ps path of TestForeignGitDirKeepsItsLock: a git of another account whose command
// names this checkout's git dir absolutely, and whose cwd lsof did not give, is an owner.
func TestDarwinForeignGitDirKeepsItsLock(t *testing.T) {
	t.Parallel()
	hermetic(t)
	dir, lock := oldIndexLock(t)
	gd, err := GitDir(dir)
	require.NoError(t, err)
	cleared, cerr := clearStaleIndexLock(dir, time.Now(), func() ([]gitProc, error) {
		return gitProcsFromPS("502 77 git --git-dir="+gd+" fetch\n", "501", map[string]string{}, nil, func(string) (bool, error) { return true, nil })
	})
	require.False(t, cleared, "--git-dir of another account: cleared=%v err=%v, want the lock kept as owned", cleared, cerr)
	require.NoError(t, cerr, "--git-dir of another account: cleared=%v err=%v, want the lock kept as owned", cleared, cerr)
	{
		_, statErr := os.Lstat(lock)
		require.Equal(t, nil, statErr, "lock lost: %v", statErr)
	}
}
