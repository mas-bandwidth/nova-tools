package main

// issue2255_test.go covers the licence rule-table edges from SPEC-DECIDE.md reading 6
// that had no command-level test: once the table classes a red as rerunnable, a decider's
// answer that withdraws the licence must turn rerun=licensed into rerun=no finding=yes, and
// the withdrawal must never grant a licence.

import (
	"os"
	"path/filepath"

	"testing"
)

// makeFlakes writes one flake table to a file inside t.TempDir and returns its path.
func makeFlakes(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "flakes.tsv")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
