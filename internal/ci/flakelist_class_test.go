package ci

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// THE FLAKE LIST ONLY SHRINKS.
//
// `nova-merge batch --land --flakes tools/ci/flakes.txt` may rerun a red ci-ok once when
// every failing test on it is named in that file. That is a real power and it has a real
// cost: every line of that file is a place the suite is allowed to lie, and a file whose
// entries are added faster than they are fixed is a suite nobody trusts, one line at a time.
//
// So the file is held from this side. It shipped with TWO entries -- the two the fleet had
// on 2026-09-18, both timing, neither any batch's fault -- and a third one is a red run
// here. The way to change this number is DOWN: fix the test, delete its line, and lower the
// ceiling with it. Adding one is a decision for a person and not a thing a landing does to
// get itself green.
//
// This is a class test and not a bug fix, for the reason pit stop 3 gave: a fixed instance
// comes back under another name, and a fixed class cannot.

// flakeCeiling is how many known flakes this tree admits. It has one lawful direction.
const flakeCeiling = 2

// flakeListPath is the one list the landing verb reads in this repository.
const flakeListPath = "tools/ci/flakes.txt"

func TestTheFlakeListOnlyShrinks(t *testing.T) {
	t.Parallel()
	path := filepath.Join(repoRoot(t), filepath.FromSlash(flakeListPath))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s is the list `nova-merge batch --land --flakes` reads; it must exist, even empty: %v", flakeListPath, err)
	}
	// It is read by THE PARSER THE VERB USES, so a file this test calls fine and the verb
	// refuses cannot exist: an entry with no reason is a refusal on both sides.
	flakes, err := merge.ParseFlakes(string(raw))
	if err != nil {
		t.Fatalf("%s is not a list the landing verb can read: %v", flakeListPath, err)
	}
	if flakes.Len() > flakeCeiling {
		t.Errorf("%s holds %d entries and this tree admits %d. The list only shrinks: fix the test, delete its line, and lower flakeCeiling with it. Adding one is a decision for a person, not a thing a landing does to get itself green.",
			flakeListPath, flakes.Len(), flakeCeiling)
	}
	// AN ENTRY WITH NO REASON IS UNREMOVABLE, so every one carries one. The parser already
	// refuses a line with fewer than three fields; this reads the reason for content, so a
	// line whose reason is a dash or a "flaky" passes nothing on to whoever inherits it.
	for _, e := range flakes.Entries {
		if len([]rune(e.Reason)) < 20 {
			t.Errorf("%s %s is on the list with the reason %q; a reason is the sentence that lets somebody else decide the test is fixed, so it is a sentence and not a word",
				e.Package, e.Test, e.Reason)
		}
	}
}
