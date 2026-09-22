//go:build darwin

package bus

import (
	"errors"
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
