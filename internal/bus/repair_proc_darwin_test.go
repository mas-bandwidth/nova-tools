//go:build darwin

package bus

import (
	"errors"
	"os"
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
	_, err := gitProcsFromPS("77 git cat-file --batch\n", nil, errors.New("lsof failed"), func(string) (bool, error) {
		t.Fatal("lsof failure was treated as a pid to re-check, not as an unreadable cwd")
		return false, nil
	})
	if err == nil {
		t.Fatal("lsof failure with a cwd git and no -C was a complete scan")
	}
	cleared, cerr := clearStaleIndexLock(dir, time.Now(), func() ([]gitProc, error) {
		return nil, err
	})
	assertLockKept(t, lock, cleared, cerr)
}

// A pid that is gone between ps and lsof is not an owner. A pid that is still there
// and has no cwd is an incomplete scan. The two are not the same answer.
func TestDarwinVanishedGitIsNotAnUnreadableCwd(t *testing.T) {
	procs, err := gitProcsFromPS("77 git status\n", map[string]string{}, nil, func(string) (bool, error) {
		return false, nil
	})
	if err != nil || len(procs) != 0 {
		t.Fatalf("vanished git: procs=%+v err=%v, want none and no error", procs, err)
	}
	_, err = gitProcsFromPS("77 git status\n", map[string]string{}, nil, func(string) (bool, error) {
		return true, nil
	})
	if err == nil || !strings.HasPrefix(err.Error(), ownershipUnknown) {
		t.Fatalf("still-present git with no cwd: err=%v", err)
	}
	if strings.Contains(err.Error(), "\n") || len(err.Error()) > ownershipDiagCap {
		t.Fatalf("diagnostic is not bounded: %q", err)
	}
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
	if err != nil || len(cwds) != 0 {
		t.Fatalf("lsof with no git to match: cwds=%v err=%v, want an empty map and no error", cwds, err)
	}
	procs, err := gitProcsFromPS("77 git rev-list --objects --stdin --not --all\n", cwds, err, func(string) (bool, error) {
		return false, nil
	})
	if err != nil || len(procs) != 0 {
		t.Fatalf("a git gone between ps and lsof: procs=%+v err=%v, want none and no error", procs, err)
	}
	dir, lock := oldIndexLock(t)
	cleared, cerr := clearStaleIndexLock(dir, time.Now(), func() ([]gitProc, error) { return procs, err })
	if cerr != nil || !cleared {
		t.Fatalf("stale lock with only a vanished git: cleared=%v err=%v, want removed", cleared, cerr)
	}
	if _, statErr := os.Lstat(lock); !os.IsNotExist(statErr) {
		t.Fatalf("stale index.lock still present: %v", statErr)
	}

	for _, c := range []struct {
		stdout, stderr string
		code           int
	}{
		{"", "lsof: WARNING: can't stat() nfs file system /Volumes/x\n", 1},
		{"", "", 2},
		{"p77\nn/somewhere\n", "", 1},
	} {
		if _, err := lsofCwds(c.stdout, c.stderr, c.code, errors.New("exit status")); err == nil {
			t.Fatalf("lsof stdout=%q stderr=%q code=%d was read as a complete scan", c.stdout, c.stderr, c.code)
		}
	}
	got, err := lsofCwds("p77\nfcwd\nn/bus\np78\nfcwd\nn/other\n", "", 0, nil)
	if err != nil || got["77"] != "/bus" || got["78"] != "/other" {
		t.Fatalf("lsof parse: %v %v", got, err)
	}
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
		if got := darwinStatAlive(c.out); got != c.alive {
			t.Fatalf("ps stat %q: alive=%v, want %v", c.out, got, c.alive)
		}
	}
	procs, err := gitProcsFromPS("77 git index-pack --stdin --fix-thin\n", map[string]string{}, nil, func(string) (bool, error) {
		return darwinStatAlive("Z+\n"), nil
	})
	if err != nil || len(procs) != 0 {
		t.Fatalf("a zombie git with no cwd: procs=%+v err=%v, want none and no error", procs, err)
	}
}
