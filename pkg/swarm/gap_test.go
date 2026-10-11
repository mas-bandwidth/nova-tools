package swarm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Behaviours the 2026-10-02 break-it probes found no unit test for: each test
// here goes red when its line is broken.

// The header tokens come in one byte-stable order: by name.
func TestCardHeaderChecksAreInNameOrder(t *testing.T) {
	t.Parallel()
	for range 32 {
		require.Equal(t, []string{"kind-declared", "paths-declared", "paused", "test-named"}, CardHeaderChecks())
	}
}

// An unknown leg names the fleet's leg table in name order, however many legs
// it holds.
func TestLegInFleetNamesTheTableInNameOrder(t *testing.T) {
	t.Parallel()
	bc := BaseCheck{Legs: FleetLegs{"zig": true, "go": true, "sbcl": true, "rust": true, "ocaml": true}, P95: KindP95{"fix": 1}}
	for range 32 {
		fs := findingsFor(LintCardBase(baseCard(map[string]string{"LEG": "lisp"}), bc), "leg-in-fleet")
		require.Len(t, fs, 1, "findings: %v", fs)
		require.Contains(t, fs[0].Excerpt, "(go, ocaml, rust, sbcl, zig)", "findings: %v", fs)
	}
}
