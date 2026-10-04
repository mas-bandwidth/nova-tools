package update

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// staleGitTransactionLocks names only the Git transaction locks this test can
// leave behind. In particular, next-index-* is Git's temporary replacement
// index; this list is intentionally narrower than a wildcard lock cleanup.
func staleGitTransactionLocks(gitDir string) []string {
	var found []string
	for _, pattern := range []string{"index.lock", "HEAD.lock", "next-index-*.lock", "refs/heads/*.lock", "logs/HEAD.lock", "logs/refs/heads/*.lock"} {
		matches, _ := filepath.Glob(filepath.Join(gitDir, pattern))
		found = append(found, matches...)
	}
	sort.Strings(found)
	return found
}

// removeStaleGitTransactionLocks performs the named operator repair for the
// disposable checkout used by these tests. Callers must establish that the
// killed process group is gone before invoking it.
func removeStaleGitTransactionLocks(t *testing.T, gitDir string) []string {
	t.Helper()
	var removed []string
	for _, lock := range staleGitTransactionLocks(gitDir) {
		if err := os.Remove(lock); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			require.Fail(t, fmt.Sprintln(err))
		}
		removed = append(removed, lock)
	}
	return removed
}

func TestRemoveStaleGitTransactionLocksNamesOnly(t *testing.T) {
	t.Parallel()

	gitDir := filepath.Join(t.TempDir(), ".git")
	for _, dir := range []string{gitDir, filepath.Join(gitDir, "refs", "heads"), filepath.Join(gitDir, "logs", "refs", "heads")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			require.NoError(t, err, err)
		}
	}
	for _, name := range []string{
		"HEAD.lock", "index.lock", "next-index-11397.lock",
		"refs/heads/main.lock", "logs/HEAD.lock", "logs/refs/heads/main.lock",
		"config.lock", "next-index.lock",
	} {
		if err := os.WriteFile(filepath.Join(gitDir, filepath.FromSlash(name)), nil, 0o600); err != nil {
			require.NoError(t, err, err)
		}
	}

	removed := removeStaleGitTransactionLocks(t, gitDir)
	var names []string
	for _, path := range removed {
		rel, err := filepath.Rel(gitDir, path)
		if err != nil {
			require.NoError(t, err, err)
		}
		names = append(names, filepath.ToSlash(rel))
	}
	sort.Strings(names)
	want := []string{"HEAD.lock", "index.lock", "logs/HEAD.lock", "logs/refs/heads/main.lock", "next-index-11397.lock", "refs/heads/main.lock"}
	if strings.Join(names, "\n") != strings.Join(want, "\n") {
		require.Failf(t, "", "removed %v, want %v", names, want)
	}
	for _, name := range []string{"config.lock", "next-index.lock"} {
		if _, err := os.Stat(filepath.Join(gitDir, name)); err != nil {
			require.NoErrorf(t, err, "cleanup removed unrelated lock %s: %v", name, err)
		}
	}
}
