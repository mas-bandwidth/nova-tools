package secrets

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unreadableStore is a store directory holding one file nobody can read (mode
// 000): a scan that skipped it would pass over a file it never checked.
func unreadableStore(t *testing.T) (store, file string) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a 000-mode file is unreadable only to a non-root user on a POSIX system")
	}
	store = t.TempDir()
	file = filepath.Join(store, "locked.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	require.NoError(t, os.Chmod(file, 0))
	t.Cleanup(func() { _ = os.Chmod(file, 0o600) })
	return store, file
}

// TestInvariant5ReportsAnUnreadableFile: the private-key scan fails on a file it
// cannot read, names it, and says how to fix it (never-silent: "no private key
// here" is not what an unread file means).
func TestInvariant5ReportsAnUnreadableFile(t *testing.T) {
	t.Parallel()
	store, _ := unreadableStore(t)

	fails := CheckInvariant5(store, filepath.Join(t.TempDir(), "key.txt"))
	require.Len(t, fails, 1, "want one failure for the unreadable file, got %+v", fails)
	f := fails[0]
	assert.Equal(t, "store-private-key", f.Kind, "failure = %+v, want kind store-private-key on locked.txt", f)
	assert.Equal(t, "locked.txt", f.File, "failure = %+v, want kind store-private-key on locked.txt", f)
	assert.Contains(t, f.Reason, "could not be read, so it was not checked", "the reason does not say it was not checked and how to fix it: %q", f.Reason)
	assert.Contains(t, f.Reason, "fix its permissions and rerun", "the reason does not say it was not checked and how to fix it: %q", f.Reason)
}

// TestInvariant7ReportsAnUnreadableFile is the same for the untracked-plaintext
// scan, on a file the tracked set does not name.
func TestInvariant7ReportsAnUnreadableFile(t *testing.T) {
	t.Parallel()
	store, _ := unreadableStore(t)

	fails := CheckInvariant7(store, map[string]bool{})
	require.Len(t, fails, 1, "want one failure for the unreadable file, got %+v", fails)
	f := fails[0]
	assert.Equal(t, "untracked-plaintext", f.Kind, "failure = %+v, want kind untracked-plaintext on locked.txt", f)
	assert.Equal(t, "locked.txt", f.File, "failure = %+v, want kind untracked-plaintext on locked.txt", f)
	assert.Contains(t, f.Reason, "could not be read, so it was not checked", "the reason does not say it was not checked and how to fix it: %q", f.Reason)
	assert.Contains(t, f.Reason, "fix its permissions and rerun", "the reason does not say it was not checked and how to fix it: %q", f.Reason)
}
