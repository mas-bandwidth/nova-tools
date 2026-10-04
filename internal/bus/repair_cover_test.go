package bus

// The unit coverage for repair.go's seams and small helpers. Every test is named
// TestRepairCover... so `go test -run TestRepairCover` selects the set, and each covers
// one function's main path and one refusal. The git-backed pair runs a real repository in
// t.TempDir through the package's own git helper; nothing here sleeps, opens a socket or
// reaches a store.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRepairCoverHookIndexLockScanForTest pins the one-shot scan hook: it remembers the
// inspected lock, fires its callback once when that lock reaches runIndexLockScanHooks,
// and refuses a path that is not there.
func TestRepairCoverHookIndexLockScanForTest(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.lock")
	require.NoError(t, os.WriteFile(path, nil, 0o644))

	fired := 0
	restore, err := HookIndexLockScanForTest(path, func() { fired++ })
	require.NoError(t, err, "a lock that exists is hookable")
	require.NotNil(t, restore)

	fi, err := os.Lstat(path)
	require.NoError(t, err)
	runIndexLockScanHooks(fi)
	require.Equal(t, 1, fired, "the hook fires on the inspected lock")
	runIndexLockScanHooks(fi)
	require.Equal(t, 1, fired, "the hook fires once only")
	restore()

	restore, err = HookIndexLockScanForTest(filepath.Join(t.TempDir(), "absent.lock"), func() {})
	require.Error(t, err, "a lock that does not exist cannot be hooked")
	require.Nil(t, restore)
}

// TestRepairCoverSetIndexLockOwnerForTest pins the owner override: while it is set the
// owner reader returns the given uid for that lock, and after restore the real reader is
// back. A path that is not there is refused.
func TestRepairCoverSetIndexLockOwnerForTest(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.lock")
	require.NoError(t, os.WriteFile(path, nil, 0o644))
	fi, err := os.Lstat(path)
	require.NoError(t, err)

	restore, err := SetIndexLockOwnerForTest(path, 4321)
	require.NoError(t, err, "a lock that exists can be overridden")
	require.NotNil(t, restore)
	uid, ok := indexLockOwner(fi)
	require.True(t, ok)
	require.Equal(t, uint32(4321), uid, "the override is what the owner reader returns")

	restore()
	gotUID, gotOK := indexLockOwner(fi)
	wantUID, wantOK := lockFileOwner(fi)
	require.Equal(t, wantOK, gotOK, "the real owner reader answers after restore")
	require.Equal(t, wantUID, gotUID, "the real owner reader answers after restore")

	restore, err = SetIndexLockOwnerForTest(filepath.Join(t.TempDir(), "absent.lock"), 4321)
	require.Error(t, err, "a lock that does not exist cannot be overridden")
	require.Nil(t, restore)
}

// TestRepairCoverProcDead pins the stat-state read: Z and X are dead, S is not, a line
// that is not a stat line is refused, and a read that fails is returned rather than read
// as a live process.
func TestRepairCoverProcDead(t *testing.T) {
	t.Parallel()
	readErr := errors.New("stat unreadable")
	reader := func(stat string, err error) procReader {
		return procReader{readFile: func(string) ([]byte, error) {
			if err != nil {
				return nil, err
			}
			return []byte(stat), nil
		}}
	}
	cases := []struct {
		name     string
		reader   procReader
		wantDead bool
		wantErr  bool
	}{
		{"zombie", reader("42 (git) Z 1 1", nil), true, false},
		{"exited", reader("42 (git defunct) X 1", nil), true, false},
		{"sleeping", reader("42 (git) S 1 1", nil), false, false},
		{"not a stat line", reader("nonsense", nil), false, true},
		{"read fails", reader("", readErr), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dead, err := procDead("42", tc.reader)
			require.Equal(t, tc.wantErr, err != nil, "err = %v", err)
			require.Equal(t, tc.wantDead, dead)
		})
	}
}

// TestRepairCoverExistsInTree pins the tree membership question: a path in the revision is
// true, one that is not is false and not an error, and a directory that is not a
// repository is the refusal.
func TestRepairCoverExistsInTree(t *testing.T) {
	t.Parallel()
	hermetic(t)
	dir := t.TempDir()
	_, err := git(dir, "init", "-q", "-b", "main")
	require.NoError(t, err)
	write(t, dir, "tracked.txt", "present\n")
	_, err = git(dir, "add", "--", "tracked.txt")
	require.NoError(t, err)
	_, err = git(dir, "commit", "-q", "-m", "add tracked")
	require.NoError(t, err)

	exists, err := existsInTree(dir, "HEAD", "tracked.txt")
	require.NoError(t, err)
	require.True(t, exists, "a path in the tree is found")
	exists, err = existsInTree(dir, "HEAD", "missing.txt")
	require.NoError(t, err)
	require.False(t, exists, "a path not in the tree is reported absent, not as an error")

	_, err = existsInTree(t.TempDir(), "HEAD", "tracked.txt")
	require.Error(t, err, "a directory that is not a repository is a refusal")
}

// TestRepairCoverBlockingPaths pins the fast-forward blocking question: no dirty paths
// block nothing; a dirty path the incoming tree changes is blocked; a dirty path the
// incoming tree leaves alone is not; and a directory that is not a repository is the
// refusal.
func TestRepairCoverBlockingPaths(t *testing.T) {
	t.Parallel()
	hermetic(t)
	dir := t.TempDir()
	_, err := git(dir, "init", "-q", "-b", "main")
	require.NoError(t, err)
	write(t, dir, "same.txt", "a\n")
	_, err = git(dir, "add", "--", "same.txt")
	require.NoError(t, err)
	_, err = git(dir, "commit", "-q", "-m", "base")
	require.NoError(t, err)

	_, err = git(dir, "checkout", "-q", "-b", "incoming")
	require.NoError(t, err)
	write(t, dir, "same.txt", "b\n")
	write(t, dir, "added.txt", "new\n")
	_, err = git(dir, "add", "--", "same.txt", "added.txt")
	require.NoError(t, err)
	_, err = git(dir, "commit", "-q", "-m", "incoming")
	require.NoError(t, err)
	_, err = git(dir, "checkout", "-q", "main")
	require.NoError(t, err)

	blocked, err := blockingPaths(dir, "incoming", nil)
	require.NoError(t, err)
	require.Empty(t, blocked, "no dirty paths block nothing")

	blocked, err = blockingPaths(dir, "incoming", []string{"same.txt", "added.txt", "unrelated.txt"})
	require.NoError(t, err)
	require.Equal(t, []string{"same.txt", "added.txt"}, blocked, "a dirty path the incoming tree changes is blocked; an unrelated one is not")

	_, err = blockingPaths(t.TempDir(), "HEAD", []string{"x"})
	require.Error(t, err, "a directory that is not a repository is a refusal")
}
