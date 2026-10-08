package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// never_force_class_test.go is the `never-force` class test in docs/SPEC-CI.md:
// nothing in nova-tools may rewrite a shared ref. The test reads the
// repository's Go sources, shell scripts, the Makefile, workflow YAML and card
// templates for the five force patterns of the card's PATTERNS TO REFUSE
// paragraph used against a shared ref: origin/<anything>, dev or main. It
// fails naming the file and line of each, and the legitimate uses are rows of
// internal/ci/never_force_allowlist.txt, a reasoned list that only shrinks.
//
// TestNeverForceCheckerScans is the witness: a fixture script that force-pushes
// to dev is refused naming its file and line, a clean push and a force push
// with no shared ref are not, and the tree with its allowlist is green.

// readNeverForceAllowlist reads the reasoned allowlist: one `path:line reason`
// per row. It returns the keys by lookup and in file order, so a row the tree
// no longer needs is red and is deleted rather than kept.
func readNeverForceAllowlist(t *testing.T) (map[string]bool, []string) {
	t.Helper()
	allow := map[string]bool{}
	var keys []string
	for _, line := range strings.Split(readFile(t, neverForceAllowlistPath), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, reason, ok := strings.Cut(line, " ")
		require.True(t, ok && strings.TrimSpace(reason) != "", "allowlist row %q is not `path:line reason`", line)
		if !allow[key] {
			keys = append(keys, key)
		}
		allow[key] = true
	}
	return allow, keys
}

// TestNoForcePushOrHardResetOfASharedRef reads the tree and refuses every force
// pattern used against a shared ref that is not a reasoned row of the
// allowlist, and every allowlist row the tree no longer needs.
func TestNoForcePushOrHardResetOfASharedRef(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	findings, err := neverForceFindings(root)
	require.NoError(t, err)
	require.NotEmpty(t, findings, "the scan found no file to read; the rule is looking in the wrong place")

	allow, rows := readNeverForceAllowlist(t)

	found := map[string]bool{}
	for _, f := range findings {
		found[f.key()] = true
		if allow[f.key()] {
			continue
		}
		t.Errorf("%s:%d uses %s against a shared ref: add it to internal/ci/%s with a reason, or change the pattern to not touch shared refs",
			f.Rel, f.Line, f.Pattern, neverForceAllowlistPath)
	}
	for _, key := range rows {
		assert.True(t, found[key], "internal/ci/%s row %q names no force pattern against a shared ref in the tree; delete the stale row",
			neverForceAllowlistPath, key)
	}
}

// TestNeverForceCheckerScans is the witness that holds the checker's decisions:
// a fixture script that force-pushes to dev is refused naming its file and
// line, and a clean push and a force push with no shared ref are not.
func TestNeverForceCheckerScans(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "force-dev.sh"), []byte("git push --force origin dev\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "clean.sh"), []byte("git push origin HEAD:refs/heads/my-job\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plain.sh"), []byte("git push --force\n"), 0o600))

	findings, err := neverForceFindings(dir)
	require.NoError(t, err)
	require.Len(t, findings, 1, "one fixture force-pushes a shared ref")
	assert.Equal(t, "force-dev.sh:1", findings[0].key())
	assert.Contains(t, findings[0].Pattern, "push")
}
