package bus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRepairCoverHookIndexLockScanForTest covers HookIndexLockScanForTest and the
// runIndexLockScanHooks call that consumes its hook. A hook set on a lock file's
// inode fires once when the scan re-checks that file, then is consumed; a
// non-existent path is refused before any hook is stored.
func TestRepairCoverHookIndexLockScanForTest(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		setupPath func(t *testing.T) string
		wantErr   bool
	}{
		{
			name: "a hook fires once for the inspected lock and is consumed",
			setupPath: func(t *testing.T) string {
				lock := filepath.Join(t.TempDir(), "index.lock")
				require.NoError(t, os.WriteFile(lock, nil, 0o644))
				return lock
			},
		},
		{
			name: "a non-existent lock is refused",
			setupPath: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "does-not-exist")
			},
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := tc.setupPath(t)

			fired := 0
			restore, err := HookIndexLockScanForTest(path, func() {
				fired++
			})
			if tc.wantErr {
				require.Error(t, err, "expected a refusal for a missing path")
				require.Nil(t, restore, "no restore when the hook was never set")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, restore)

			fi, err := os.Lstat(path)
			require.NoError(t, err)

			// The hook matches the inspected lock by inode and fires once.
			runIndexLockScanHooks(fi)
			require.Equal(t, 1, fired, "the afterScan hook did not fire for the matching lock")

			// It is consumed: a second call must not fire it again.
			runIndexLockScanHooks(fi)
			require.Equal(t, 1, fired, "the hook fired more than once; it should fire exactly once")

			// After restore the hook is gone.
			restore()
			runIndexLockScanHooks(fi)
			require.Equal(t, 1, fired, "the hook fired after restore")
		})
	}
}

// TestRepairCoverSetIndexLockOwnerForTest covers SetIndexLockOwnerForTest and the
// indexLockOwner helper that reads its override. An override changes what the lock
// owner reads as until the returned restore runs; a non-existent path is refused.
func TestRepairCoverSetIndexLockOwnerForTest(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		setupPath func(t *testing.T) string
		uid       uint32
		wantErr   bool
	}{
		{
			name: "an override is read until restore runs",
			setupPath: func(t *testing.T) string {
				lock := filepath.Join(t.TempDir(), "index.lock")
				require.NoError(t, os.WriteFile(lock, nil, 0o644))
				return lock
			},
			uid: 42,
		},
		{
			name: "a non-existent lock is refused",
			setupPath: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "does-not-exist")
			},
			uid:     42,
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := tc.setupPath(t)

			restore, err := SetIndexLockOwnerForTest(path, tc.uid)
			if tc.wantErr {
				require.Error(t, err, "expected a refusal for a missing path")
				require.Nil(t, restore, "no restore when the override was never set")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, restore)

			fi, err := os.Lstat(path)
			require.NoError(t, err)

			uid, ok := indexLockOwner(fi)
			require.True(t, ok, "the override was not read")
			require.Equal(t, tc.uid, uid, "the override uid was not returned")

			restore()

			uid, ok = indexLockOwner(fi)
			require.True(t, ok, "the real owner is not readable after restore")
			require.NotEqual(t, tc.uid, uid, "the override is still active after restore")
		})
	}
}

// TestRepairCoverProcDead covers procDead through a fake procReader: the stat
// state character decides dead or alive; a read error or a line that is not a
// stat line is refused.
func TestRepairCoverProcDead(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		statContent string
		readErr     error
		wantDead    bool
		wantErr     bool
	}{
		{
			name:        "a zombie state is dead",
			statContent: "42 (git) Z 1 1",
			wantDead:    true,
		},
		{
			name:        "a dead state is dead",
			statContent: "42 (git) X 1 1",
			wantDead:    true,
		},
		{
			name:        "a sleeping state is alive",
			statContent: "42 (git) S 1 1",
			wantDead:    false,
		},
		{
			name:        "a running state is alive",
			statContent: "42 (git) R 1 1",
			wantDead:    false,
		},
		{
			name:        "a read error is returned",
			statContent: "ignored",
			readErr:     os.ErrPermission,
			wantErr:     true,
		},
		{
			name:        "content that is not a stat line is refused",
			statContent: "not a stat line",
			wantErr:     true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := procReader{
				readFile: func(string) ([]byte, error) {
					if tc.readErr != nil {
						return nil, tc.readErr
					}
					return []byte(tc.statContent), nil
				},
			}
			dead, err := procDead("1", r)
			if tc.wantErr {
				require.Error(t, err, "expected an error for %q", tc.name)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantDead, dead, "procDead(%q) = %v, want %v", tc.name, dead, tc.wantDead)
		})
	}
}

// TestRepairCoverExistsInTree covers existsInTree: a path present in a tree is
// found; a path absent from that tree is a plain "no", not an error; a git that
// cannot answer at all is an error rather than a silent "no".
func TestRepairCoverExistsInTree(t *testing.T) {
	t.Parallel()
	dir, shas := datedRepo(t)
	for _, tc := range []struct {
		name    string
		ref     string
		rel     string
		nonRepo bool
		want    bool
		wantErr bool
	}{
		{
			name: "a path present in the tree",
			ref:  shas[2],
			rel:  "from-ada/na.md",
			want: true,
		},
		{
			name: "a path present in an earlier tree",
			ref:  shas[0],
			rel:  "from-ada/na.md",
			want: true,
		},
		{
			name: "a path absent from the tree",
			ref:  shas[0],
			rel:  "from-ada/nc.md",
			want: false,
		},
		{
			name: "a path that never existed",
			ref:  shas[2],
			rel:  "from-ada/ghost.md",
			want: false,
		},
		{
			name: "a git that cannot answer is an error, not a no",
			ref:  shas[0],
			rel:  "from-ada/na.md",
			// A directory outside any repository makes git fail with a reason
			// existsInTree does not swallow, so the error is returned rather
			// than read as "the path is absent".
			nonRepo: true,
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			useDir := dir
			if tc.nonRepo {
				useDir = t.TempDir()
			}
			got, err := existsInTree(useDir, tc.ref, tc.rel)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got, "existsInTree(%q, %q) = %v, want %v", tc.ref, tc.rel, got, tc.want)
		})
	}
}

// TestRepairCoverBlockingPaths covers blockingPaths: a dirty path whose content
// differs between HEAD and the target ref blocks the fast-forward; a dirty
// untracked path that already exists in the target ref blocks; a path that does
// not differ and is not untracked does not; an empty dirty list refuses nothing;
// a ref git cannot resolve is refused rather than read as "nothing blocks".
func TestRepairCoverBlockingPaths(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		ref       func(t *testing.T) (string, string)
		dirty     []string
		wantBlock []string
		wantErr   bool
	}{
		{
			name: "a changed path blocks",
			ref: func(t *testing.T) (string, string) {
				dir, shas := datedRepo(t)
				// nb.md exists in shas[2] (HEAD) but not in shas[0]; writing
				// dirty content makes it a dirty, changed path.
				require.NoError(t, os.WriteFile(filepath.Join(dir, "from-ada", "nb.md"), []byte("dirty\n"), 0o644))
				return dir, shas[0]
			},
			dirty:     []string{"from-ada/nb.md"},
			wantBlock: []string{"from-ada/nb.md"},
		},
		{
			name: "a path unchanged between HEAD and ref does not block",
			ref: func(t *testing.T) (string, string) {
				dir, shas := datedRepo(t)
				// na.md is identical in shas[0] and shas[2]; it is tracked and
				// clean, so neither "changed" nor "untracked" nor "in tree".
				return dir, shas[0]
			},
			dirty:     []string{"from-ada/na.md"},
			wantBlock: nil,
		},
		{
			name: "a dirty untracked path that exists in ref blocks",
			ref: func(t *testing.T) (string, string) {
				dir, shas := datedRepo(t)
				// from-ada is a tracked directory in HEAD and in ref. Replacing
				// it with an untracked regular file leaves its tracked files
				// reported as deletions under "from-ada/..." and the file itself
				// as "?? from-ada", so pathUntracked("from-ada") is true. The
				// path still exists in ref, so the fast-forward would overwrite
				// the untracked file and it blocks.
				require.NoError(t, os.RemoveAll(filepath.Join(dir, "from-ada")))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "from-ada"), []byte("dirty\n"), 0o644))
				return dir, shas[0]
			},
			dirty:     []string{"from-ada"},
			wantBlock: []string{"from-ada"},
		},
		{
			name: "a ref git cannot resolve is refused",
			ref: func(t *testing.T) (string, string) {
				dir, _ := datedRepo(t)
				return dir, "no-such-ref"
			},
			dirty:   []string{"from-ada/na.md"},
			wantErr: true,
		},
		{
			name: "an empty dirty list refuses nothing",
			ref: func(t *testing.T) (string, string) {
				dir, shas := datedRepo(t)
				return dir, shas[0]
			},
			dirty:     nil,
			wantBlock: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir, ref := tc.ref(t)
			got, err := blockingPaths(dir, ref, tc.dirty)
			if tc.wantErr {
				require.ErrorContains(t, err, ref, "the refusal must name the ref it could not resolve")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantBlock, got, "blockingPaths = %v, want %v", got, tc.wantBlock)
		})
	}
}
