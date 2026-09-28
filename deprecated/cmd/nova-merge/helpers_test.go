package main

import (
	"strings"
	"testing"
)

// labBatchGate is the landing gate's suite AS THE TESTS RUN IT: batchGate with the cross
// vet taken out, and nothing else taken out.
//
// The gate a friend runs is batchGate, and these tests run the rest of it for real -- the
// build, the vet, CI's own `go test -json` command and the lisp suite. The one step that
// cannot be paid here is `vet-windows`, because `GOOS=windows go vet ./...` must build the
// WINDOWS STANDARD LIBRARY into the cache first: seconds on an idle 64-core bench and far
// more on a darwin runner sharing its machine with seven others. Paid inside `go test`, it
// comes out of this package's own -timeout, and the package's serial tests are what its
// parallel tests wait behind -- which is exactly how the merge group's darwin leg reached
// `panic: test timed out after 1m40s` with ten parallel tests starved at 14 s each on
// 2026-09-18.
//
// TestTheGateCrossVetsForWindows pins the step in the real list and pins that this list
// differs from it by that one name and no other, so the exemption cannot quietly grow.
func labBatchGate() []batchStep {
	out := make([]batchStep, 0, len(batchGate))
	for _, step := range batchGate {
		if step.name == crossVetStep {
			continue
		}
		out = append(out, step)
	}
	return out
}

func contains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("want %q in:\n%s", needle, haystack)
	}
}

func absent(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("did not want %q in:\n%s", needle, haystack)
	}
}
