package ci

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// testwrites_class_test.go is the class rule behind the 2026-10-04 adoption
// pass: a cmd/nova-sprint test ran `watch --wake --state wake.json` with the
// package directory as its working directory, and the file it left made
// `nova-update release build` record no commit.
//
// The test runs no test. CI runs the package tests in its own run; this check
// reads `git status --porcelain` afterwards and names any file a test left.
// A path a test writes belongs under t.TempDir(), never in the source tree.

// TestNoTestWritesIntoTheSourceTree fails when the checkout is dirty, and the
// failure names each path git status reports.
func TestNoTestWritesIntoTheSourceTree(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = root
	out, err := cmd.Output()
	require.NoError(t, err, "git status --porcelain in %s", root)
	status := strings.TrimRight(string(out), "\n")
	if status == "" {
		return
	}
	var left []string
	for _, line := range strings.Split(status, "\n") {
		left = append(left, porcelainPath(line))
	}
	t.Errorf("a test left %s in the source tree:\n%s", strings.Join(left, ", "), status)
}

// porcelainPath is the path a `git status --porcelain` line names. A rename
// names the destination, which is the file left in the tree.
func porcelainPath(line string) string {
	path := line
	if len(line) > 3 && line[2] == ' ' {
		path = line[3:]
	}
	if i := strings.LastIndex(path, " -> "); i >= 0 {
		path = path[i+len(" -> "):]
	}
	return path
}
