package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// testwrites_class_test.go is the class rule behind the 2026-10-04 adoption
// pass: a cmd/nova-sprint test ran its watch example with `--state wake.json`
// and the package directory as its working directory, and the file it left
// made `nova-update release build` record no commit.
//
// The test runs no package test and it does not run git. CI's own run is the
// proof: ci.yml's unit leg runs `ci unit-test`, which runs `make test`, and
// that recipe records `git status --porcelain` before the package tests and
// again after them. A line that was not there before is a file a test left;
// the recipe prints it and fails. The three wake.json call sites already pass
// a path under t.TempDir(); this test keeps the proof from being deleted.

// filesATestLeft are the pieces of the `test` recipe's shell, in the order
// the recipe must hold them. `$$` is make's escaped dollar, as the Makefile
// writes it.
var filesATestLeft = []string{
	"before=$$(git status --porcelain) || exit 1",
	"$(GO) test",
	"after=$$(git status --porcelain) || exit 1",
	"comm -13",
	"a test left files in the source tree:",
	"status=1",
	"exit $$status",
}

// TestNoTestWritesIntoTheSourceTree opens in parallel, runs no package test,
// and refuses a `make test` recipe that does not name files a test left.
func TestNoTestWritesIntoTheSourceTree(t *testing.T) {
	t.Parallel()

	// A recipe that runs the package tests and exits says nothing about what
	// they wrote. A checker that had quietly stopped matching would accept it.
	bare := "GOFLAGS=-json $(GO) test $$PKGS; status=0; exit $$status"
	require.NotEmpty(t, recipeNamesFilesATestLeft(bare), "a test recipe with no git status --porcelain after the package tests was accepted")

	root := repoRoot(t)
	makefile := readFile(t, filepath.Join(root, "Makefile"))
	script, err := makefileTestScript(makefile)
	require.NoError(t, err)
	require.Empty(t, recipeNamesFilesATestLeft(script))
}

// recipeNamesFilesATestLeft reports why script does not prove the tree names
// any file a test left, or "" when the pieces stand in order.
func recipeNamesFilesATestLeft(script string) string {
	at := 0
	for _, piece := range filesATestLeft {
		i := strings.Index(script[at:], piece)
		if i < 0 {
			return fmt.Sprintf("make test does not prove the tree is clean after the package tests: missing %q after what precedes it. The recipe must record git status --porcelain before $(GO) test and again after, and print any line that appeared (a test left that file) before exit $$status", piece)
		}
		at += i + len(piece)
	}
	return ""
}

// makefileTestScript is the single-quoted bash script of the `test` target,
// the one entry ci.yml's unit leg runs.
func makefileTestScript(makefile string) (string, error) {
	const needle = "bash -o pipefail -c '"
	inTest := false
	for _, line := range strings.Split(makefile, "\n") {
		if strings.HasPrefix(line, "test:") {
			inTest = true
			continue
		}
		if !inTest {
			continue
		}
		if strings.HasPrefix(line, "\t") {
			idx := strings.Index(line, needle)
			if idx < 0 {
				continue
			}
			open := idx + len(needle) - 1
			closeRel := strings.LastIndex(line[open+1:], "'")
			if closeRel < 0 {
				return "", fmt.Errorf("test recipe has no closing quote: %s", strings.TrimSpace(line))
			}
			return line[open+1 : open+1+closeRel], nil
		}
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		inTest = false
	}
	return "", os.ErrNotExist
}
