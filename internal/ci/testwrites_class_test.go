package ci

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testwrites_class_test.go is the class rule behind the 2026-10-04 adoption
// pass: a cmd/nova-sprint test ran its watch example with `--state wake.json`
// and the package directory as its working directory, and the file it left
// made `nova-update release build` record no commit. The watch call sites now
// pass a path under t.TempDir(); this test is what keeps the next one from
// landing quietly.
//
// It runs no package test. CI checks the tree out clean and runs the package
// tests in the same go test invocation, so the class test reads the tree
// afterwards: a `git status --porcelain` line that is not the change under
// test is a file a test left, and the failure names every one.

// TestNoTestWritesIntoTheSourceTree fails when the checkout is dirty after
// the package tests, and the failure names each path git status reports.
func TestNoTestWritesIntoTheSourceTree(t *testing.T) {
	t.Parallel()

	root := repoTree(t).Root
	out, err := gitOut(root, "status", "--porcelain")
	require.NoError(t, err, "git status --porcelain in the source tree")
	status := strings.TrimRight(out, "\n")
	if status == "" {
		return
	}
	var left []string
	for _, line := range strings.Split(status, "\n") {
		left = append(left, porcelainPath(line))
	}
	assert.Failf(t, "the source tree is not clean after the package tests",
		"a test left %s in the source tree:\n%s", strings.Join(left, ", "), status)
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
