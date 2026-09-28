//go:build functional

package swarm

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

// A READ VERDICT WHOSE RUN CARRIES A KNOWN FAILURE SIGNATURE IS ABSTAIN reason=signature,
// never done. The card's RESULT.md carries a verdict=APPROVE contract line and a BRANCH
// disposition, and the run's own harness-output.log holds the go toolchain's own words, so
// the batch must score reason=signature and never print the BRANCH.
func TestBatchScoresSignature(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: READ pr-1 at abc123 verdict=APPROVE\nBRANCH: rowan/swarm-signatures at abc123"},
	})
	runner := runnerDoing(t, dir, "signature",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "write", Path: "{job}/harness-output.log",
			Body: "go: download go1.26 for linux/amd64: toolchain not available\n"},
		publishCard("{job}"),
	)
	code, out, _ := runBatch(t, tsv, root, runner, 10*time.Second)
	if code != 1 {
		t.Fatalf("a card whose run carries a failure signature abstains, exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, `a slot=1: ABSTAIN reason=signature sig="toolchain not available" class=toolchain`) {
		t.Fatalf("a read verdict whose run carries a known failure signature scores reason=signature:\n%s", out)
	}
	if strings.Contains(out, "a slot=1: BRANCH") {
		t.Fatalf("the BRANCH is never printed beside a failure signature:\n%s", out)
	}
}
