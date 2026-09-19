package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSpecWorkCarriesTheDependencyLaunchGate pins that docs/SPEC-WORK.md states
// the launch gate of nova-tools#785 BEFORE any code implements it, which is the
// order this repository works in: spec, then reads, then cards.
//
// The hurt it answers is Glenn's own, 2026-09-16: "When work is parallel, we do
// it in parallel by default. When work is serial and has dependencies, we are
// careful, and do not launch work until the dependencies are tested, ready and
// green." The `:deps` slice that landed gives `ready` the right ANSWER; it stops
// nothing, because reading and admitting were two different acts. Pit stop 3
// (#828 C) is what that costs: hand launches under STOP, with the gate living in
// a person's memory.
//
// Each string below is a rule a later card must not quietly drop while moving
// the text around. This test does not check that the launch gate WORKS -- no
// code implements it yet, and a test that pretended otherwise would be the
// manufactured green this repo refuses. It checks that the spec still says what
// the cards will be cut from.
func TestSpecWorkCarriesTheDependencyLaunchGate(t *testing.T) {
	t.Parallel()

	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-WORK.md"))

	for _, want := range []string{
		// The gate exists and is one predicate, not one per launcher.
		"The launch gate",
		"every launch is admitted by the same predicate `ready` reads",
		// Held is visible state with a blocker on it.
		"held-by=",
		// No hand launch under STOP, and no flag that buys one.
		"A hand launch of a held node is refused",
		"there is no flag for it",
		// Merged is not green.
		"A need whose PR merged with\n   no green run recorded is **not** terminal accepted",
		// The release is an effect of the settle, not a sweep.
		"in the same command, in the single writer's total order",
		// A revert reaches a dependent that is already working.
		"needs-broken-while-working",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("docs/SPEC-WORK.md no longer carries the #785 launch-gate rule %q; the launch gate is specified ahead of its code and a card is cut from this text", want)
		}
	}

	// The six replays are the acceptance list a card reads. A replay name that
	// vanishes from the spec is a slice nobody will write.
	for _, replay := range []string{
		"a-held-dependent-is-cut-and-never-on-a-slot",
		"launch-admits-exactly-what-ready-returns",
		"a-hand-launch-of-a-held-node-is-refused-naming-the-need",
		"a-merged-need-with-no-green-run-does-not-admit",
		"settling-a-need-releases-its-dependents-in-one-command",
		"reverting-a-need-under-a-working-dependent-is-a-finding-the-same-tick",
	} {
		if !strings.Contains(spec, replay) {
			t.Errorf("docs/SPEC-WORK.md no longer names the #785 launch-gate replay %q; the replay names ARE the acceptance list", replay)
		}
	}

	// The paragraph that landed the `:deps` field must point at this one rather
	// than still listing the launch gate among the things nobody has written.
	if strings.Contains(spec, "no held-in-tree launch gate — those remain the rest of #785") {
		t.Error("docs/SPEC-WORK.md's `:deps` paragraph still counts the launch gate among the unwritten rest of #785; it is specified directly below it now")
	}
}
