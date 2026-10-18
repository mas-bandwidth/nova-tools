package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTheLastSprintHunksArePorted pins the three behaviours nova-sprint's copy of pkg/swarm
// still held on 2026-10-18 that this package lacked, ported so the copy can go
// (dedupe of nova-sprint pkg/swarm, measured at nova-sprint 404633eb):
//   - dashboard-asked-for has a remedy: nova-sprint 087ab8da (lintchild.go ChildRemedy)
//   - LAND-AFTER: is a header key: nova-sprint c69945c7 (lintheader.go cardHeaderKeys)
//   - NewModels and CardNewModels are exported for add and the re-tier verbs:
//     nova-sprint 7fcfe65a (lintpaths.go)
func TestTheLastSprintHunksArePorted(t *testing.T) {
	t.Parallel()

	t.Run("dashboard-asked-for has its remedy", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, DashboardAskRemedy, ChildRemedy(DefaultChildRules, DashboardAskCheck))
	})

	t.Run("LAND-AFTER is a header key", func(t *testing.T) {
		t.Parallel()
		// A LAND-AFTER: line in the run under the contract line keeps the block going, so
		// PATHS: below it is read as the header and nothing is stranded.
		brief := "RESULT: c sha=000000000000 tier: flash\nKIND: fix\nLAND-AFTER: a, b\nPATHS: internal/s.go\n\nFix it.\n"
		block, stranded := cardHeaderBlock([]byte(brief))
		assert.Empty(t, stranded)
		assert.Equal(t, "a, b", block["LAND-AFTER"].value)
		assert.Equal(t, "internal/s.go", block["PATHS"].value)
		assert.Empty(t, LintHeaderStranded([]byte(brief)))

		// Below the prose it is stranded, as every other header key is.
		stray := "RESULT: c sha=000000000000 tier: flash\nKIND: fix\nPATHS: internal/s.go\n\nFix it.\nLAND-AFTER: a\n"
		_, stranded = cardHeaderBlock([]byte(stray))
		assert.Equal(t, map[string]int{"LAND-AFTER": 6}, stranded)
	})

	t.Run("CardNewModels reads the NEW line only", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{"tla/Lease.tla", "tla/*.tla"}, NewModels([]string{"tla/Lease.tla", "./tla/*.tla", "tla/MCLease.cfg", "pkg/x.go"}))
		assert.Empty(t, NewModels([]string{"tla/MCLease.cfg", "tla/RUNS.tsv"}))

		brief := "RESULT: m sha=000000000000 tier: pro\nKIND: feature\nPATHS: pkg/lease.go, tla/Lease.tla\nNEW: tla/Lease.tla, tla/MCLease.cfg\n\nThe prose names tla/Other.tla, which is not read.\n"
		assert.Equal(t, []string{"tla/Lease.tla"}, CardNewModels([]byte(brief)))

		edit := "RESULT: e sha=000000000000 tier: pro\nKIND: fix\nPATHS: tla/Lease.tla\n\nEdit tla/Lease.tla.\n"
		assert.Empty(t, CardNewModels([]byte(edit)), "editing a model writes no new one")
	})
}
