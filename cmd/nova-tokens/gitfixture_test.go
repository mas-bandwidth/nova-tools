package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The git fixture helpers of the functional boundary test beside it
// (boundary_functional_test.go), which builds real git repositories and no longer has the
// tool in it: the bus is Redis now. Both are kept until a change that may edit
// internal/ci/testdata declares their deletion in deleted-tests.txt.

// gitRunErr runs git the way the fixture does -- no global or system config, a fixed
// clock, and background maintenance/auto-gc turned off -- and returns an error rather than
// failing the test, so a test can run several of them in parallel goroutines.
func gitRunErr(git, dir string, args ...string) error {
	full := append([]string{
		"-c", "user.name=Fixture", "-c", "user.email=fixture@example.com",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main",
		"-c", "receive.autogc=false", "-c", "gc.auto=0", "-c", "gc.autoDetach=false",
		"-c", "maintenance.auto=false", "-c", "maintenance.autoDetach=false",
		"-C", dir,
	}, args...)
	cmd := exec.Command(git, full...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_DATE=2026-09-11T20:00:00Z", "GIT_COMMITTER_DATE=2026-09-11T20:00:00Z")
	if outb, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %v: %s", args, err, outb)
	}
	return nil
}

func gitRun(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	{
		err := gitRunErr(git, dir, args...)
		require.NoError(t, err, err)
	}
}

type treeSnapshot struct {
	digest string
	files  map[string]string
}

func readTree(t *testing.T, root string) treeSnapshot {
	t.Helper()
	sum := sha256.New()
	var names []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		names = append(names, rel)
		return nil
	})
	require.NoError(t, err, err)
	sort.Strings(names)
	files := make(map[string]string, len(names))
	for _, rel := range names {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			// A file git holds open or replaces under us is named, not skipped.
			require.FailNowf(t, "unreadable source file", "%s: %v", rel, err)
		}
		h := sha256.Sum256(raw)
		files[rel] = hex.EncodeToString(h[:])
		sum.Write([]byte(rel))
		sum.Write([]byte{0})
		sum.Write(raw)
		sum.Write([]byte{0})
	}
	require.False(t, len(names) == 0, "nothing under %s; this comparison would hold whatever happened", root)
	return treeSnapshot{
		digest: hex.EncodeToString(sum.Sum(nil)),
		files:  files,
	}
}

func diffTrees(before, after treeSnapshot) string {
	var diffs []string
	for rel, hBefore := range before.files {
		if hAfter, ok := after.files[rel]; !ok {
			diffs = append(diffs, fmt.Sprintf("removed %s", rel))
		} else if hBefore != hAfter {
			diffs = append(diffs, fmt.Sprintf("modified %s (was %s..now %s)", rel, hBefore[:8], hAfter[:8]))
		}
	}
	for rel := range after.files {
		if _, ok := before.files[rel]; !ok {
			diffs = append(diffs, fmt.Sprintf("added %s", rel))
		}
	}
	sort.Strings(diffs)
	if len(diffs) == 0 {
		return "digest mismatch with identical file contents"
	}
	return strings.Join(diffs, ", ")
}

func TestDiffTreesReportsDifferences(t *testing.T) {
	t.Parallel()

	a := treeSnapshot{
		digest: "1",
		files: map[string]string{
			"same": "aaa",
			"mod":  "1111111111",
			"del":  "333",
		},
	}
	b := treeSnapshot{
		digest: "2",
		files: map[string]string{
			"same":  "aaa",
			"mod":   "2222222222",
			"added": "444",
		},
	}
	diff := diffTrees(a, b)
	want := "added added, modified mod (was 11111111..now 22222222), removed del"
	assert.Equal(t, want, diff, "diffTrees = %q; want %q", diff, want)
}
