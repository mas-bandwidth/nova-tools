package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// split_plan_test.go pins that the plan for moving nova-sprint out of
// nova-tools is written down in this repository (Glenn, 2026-10-04: nova-sprint
// moves to its own repository after nova-tools v1.2.0). It checks that the
// record exists and carries each part of the plan, not that the plan is
// accepted; acceptance is Glenn's and Rowan's to give.

// TestSplitPlanIsWritten pins the sections the split plan must carry: the
// classification of every cmd, the one binary, the packages that move, the
// shared packages and their generality pass, the docs and models that move,
// and the order.
func TestSplitPlanIsWritten(t *testing.T) {
	t.Parallel()

	src := readFile(t, filepath.Join(repoRoot(t), "docs", "SPLIT-NOVA-SPRINT.md"))

	for _, section := range []string{
		"## Every cmd of nova-tools, classified",
		"## The one binary",
		"## (a) Packages that move",
		"## (b) Shared packages",
		"### The generality pass",
		"## (c) Docs, specs, TLA, tests, testdata, ledgers",
		"## (d) The order",
		"## Open nova-tools PRs",
	} {
		assert.Contains(t, src, section)
	}
	for _, cmd := range []string{"nova-sprint", "nova-card", "nova-work", "nova-worker", "nova-decide"} {
		assert.Truef(t, strings.Contains(src, "| "+cmd+" |"), "the classification omits %s", cmd)
	}
	require.Contains(t, src, "Status: written")
}
