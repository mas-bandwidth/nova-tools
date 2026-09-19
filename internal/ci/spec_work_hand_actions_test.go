package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSpecWorkCarriesTheHandActionRule pins that docs/SPEC-WORK.md states item 7
// of nova-tools#854 BEFORE any code implements it, which is the order this
// repository works in: spec, then reads, then cards.
//
// The hurt is class F of pit stop 3 (#828). A hand action -- a card launched
// from a shell, a PR enqueued in the forge's UI, a runner killed by ssh, a lease
// cleared with redis-cli -- is a real change to real state that writes nothing
// the tree can read. So the tree's account of the fleet is true only while
// nobody helps, and the next reader believes it anyway.
//
// Like the launch-gate test beside it, this checks that the SPEC says this and
// never that the code does it. Nothing implements the rule. A test pretending
// otherwise would be the manufactured green this repository refuses.
func TestSpecWorkCarriesTheHandActionRule(t *testing.T) {
	t.Parallel()

	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-WORK.md"))

	for _, want := range []string{
		// The rule itself, and the deadline on the finding.
		"Every hand action has a verb",
		"a hand action taken without one\nis a finding filed the same hour",
		// A missing verb is a finding against the tool, not a licence to reach past it.
		"has found a missing verb, not a reason to reach past the tool",
		// The record names the hand, and the reason is required.
		"`by=<operator>`",
		"`instead-of=",
		"reason is required and is not a free-text afterthought",
		// The count is a health signal, and zero is explicitly NOT the target.
		"Zero is not the target",
		"an *unrecorded* hand action is\n   always a defect",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("docs/SPEC-WORK.md no longer carries the #854 item 7 rule %q; it is specified ahead of its code and a card is cut from this text", want)
		}
	}

	for _, replay := range []string{
		"a-hand-action-without-a-verb-is-a-finding-the-same-hour",
		"a-hand-action-records-by-at-reason-and-instead-of",
		"a-hand-action-with-no-reason-is-refused",
		"the-window-counts-hand-actions-beside-the-work",
	} {
		if !strings.Contains(spec, replay) {
			t.Errorf("docs/SPEC-WORK.md no longer names the #854 item 7 replay %q; the replay names ARE the acceptance list", replay)
		}
	}

	// The slice says what it does NOT do, so the next card does not assume it.
	if !strings.Contains(spec, "it does not detect a hand action nobody reported") {
		t.Error("docs/SPEC-WORK.md's hand-action slice no longer says what it leaves undone; a slice that does not name its edge is read as complete")
	}
}
