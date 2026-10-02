package tokens

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFoldLockSymlinkPreservesTarget(t *testing.T) {
	t.Parallel()
	for _, missing := range []bool{false, true} {
		name := "existing-target"
		if missing {
			name = "missing-target"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			out := filepath.Join(root, "out")
			target := filepath.Join(root, "unrelated")
			require.NoError(t, os.Mkdir(out, 0700))
			const body = "unrelated data must survive\n"
			if !missing {
				require.NoError(t, os.WriteFile(target, []byte(body), 0600))
			}
			link := filepath.Join(out, LockName)
			require.NoError(t, os.Symlink(target, link))
			release, err := TakeFoldLock(out, 0)
			if release != nil {
				release()
			}
			t.Logf("TakeFoldLock err=%v", err)
			assert.Error(t, err, "expected symlink refusal before touching target")
			raw, readErr := os.ReadFile(target)
			if missing {
				assert.Truef(t, os.IsNotExist(readErr), "created symlink target: %q (%v)", raw, readErr)
			} else {
				assert.NoErrorf(t, readErr, "target changed: %q", raw)
				assert.Equalf(t, body, string(raw), "target changed")
			}
			info, lstatErr := os.Lstat(link)
			if assert.NoErrorf(t, lstatErr, "link changed: %v", info) {
				assert.NotZerof(t, info.Mode()&os.ModeSymlink, "link changed: %v", info)
			}
		})
	}
}

func TestFoldLockRefusesDirectory(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	path := filepath.Join(out, LockName)
	require.NoError(t, os.Mkdir(path, 0700))
	release, err := TakeFoldLock(out, 0)
	if release != nil {
		release()
	}
	require.Error(t, err, "directory lock was accepted")
	info, statErr := os.Lstat(path)
	require.NoErrorf(t, statErr, "directory changed: %v", info)
	require.Truef(t, info.IsDir(), "directory changed: %v", info)
}
