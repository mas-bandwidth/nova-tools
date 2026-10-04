package docs

// TestAgentsmapCover* cover RunAgentsMap (agentsmap.go:390), the one function
// the unit tier's per-function table names at 0.0%.
//
// RunAgentsMap's main path resolves the repository root from the process
// working directory and then WRITES every page into that root (Write). This
// package's tests run in parallel and the others here render or read those same
// pages from the checkout, so a unit-tier call that reaches Write races them:
// with -count=20, TestRootMapCarriesTheWholeStandard read a half-written
// docs/STANDARD.md and saw the standard drop out of the root page. The package
// exposes no working-directory seam, and the tier forbids a subprocess and a
// chdir, so the main path is left uncovered; Render, Write and Check are
// already held against a temp tree in TestStaleMapFailsUntilRegenerate. What is
// reachable without touching the tree is the argument refusal, pinned below.

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAgentsmapCoverRunAgentsMapRefusesArguments(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"one word", []string{"map"}},
		{"a verb the tool does not have", []string{"check"}},
		{"several arguments", []string{"-v", "map"}},
		{"an empty-string argument still counts", []string{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := RunAgentsMap(tc.args)
			assert.EqualError(t, err, "usage: make map (or go run ./tools/agentsmap)",
				"any argument is refused with the door that regenerates the map")
		})
	}
}
