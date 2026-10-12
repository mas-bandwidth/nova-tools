package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shellScriptSuffixes are the name endings the rule refuses, matched without case.
var shellScriptSuffixes = []string{".sh", ".bash"}

// TestNoShellScripts is the class rule that the repository tracks no *.sh or
// *.bash file, with no exception list. It reads `git ls-files` and names every
// match; a subtest shows the same scan refusing a script in a temporary tree.
func TestNoShellScripts(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	tracked := gitTracked(t, root)
	require.NotEmpty(t, tracked, "an empty tree is not a pass")

	if got := shellScripts(tracked); len(got) > 0 {
		assert.Failf(t, "no shell scripts allowed", "the tree tracks %d *.sh or *.bash file(s), and there is no exception list:\n  %s", len(got), strings.Join(got, "\n  "))
	}

	t.Run("a tracked script is refused", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		runGit(t, dir, "init", "-q")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755))
		runGit(t, dir, "add", "run.sh")
		assert.Equal(t, []string{"run.sh"}, shellScripts(gitTracked(t, dir)), "the rule must name a tracked *.sh file")
	})
}

// gitTracked is the repository-relative paths `git ls-files` reports under dir.
func gitTracked(t *testing.T, dir string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = dir
	raw, err := cmd.Output()
	require.NoError(t, err, "git ls-files in %s", dir)
	return strings.FieldsFunc(string(raw), func(r rune) bool { return r == 0 })
}

// shellScripts is the paths whose suffix is a shell script's, in the order given.
func shellScripts(paths []string) []string {
	var out []string
	for _, p := range paths {
		lower := strings.ToLower(p)
		for _, suffix := range shellScriptSuffixes {
			if strings.HasSuffix(lower, suffix) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// runGit runs one git command in dir, failing the test on a non-zero exit.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s in %s: %s", strings.Join(args, " "), dir, out)
}
