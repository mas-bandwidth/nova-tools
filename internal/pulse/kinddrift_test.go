package pulse

// The cards as they are actually cut (Rowan, 2026-09-19): 35 of the 91 cards under
// tmp/session-0919b carry a KIND: that SPEC-TOOLWORK §5's table does not hold at any
// head, `origin/dev` included -- and several of them say `SPEC: docs/SPEC-TOOLWORK.md §5
// kind <name>` while doing it. The gate already abstains on a kind it does not know
// (§5 rule 3); what was missing was a refusal a person can act on.

import (
	"strings"
	"testing"
)

// cutKinds is the measured set, name and count, from
// `grep -h '^KIND:' tmp/session-0919b/*/cards/*.md tmp/session-0919b/queue/*/*.md`.
var cutKinds = []struct {
	name    string
	count   int
	inTable bool
}{
	{"fix-red", 43, true},
	{"fix-with-red-test", 20, false},
	{"dogfood", 11, false},
	{"transcript-test", 8, true},
	{"sweep", 3, false}, // named in §5's table, not yet built in this binary (T13)
	{"new-verb", 2, false},
	{"row-test", 1, false},
	{"rebase", 1, false},        // §5's table, T13
	{"mutation-kill", 1, false}, // §5's table, T13
	{"docs-fix", 1, false},
}

func TestEveryCutKindIsEitherInTheTableOrHasANearestName(t *testing.T) {
	drifted, gated := 0, 0
	for _, k := range cutKinds {
		_, held := KindNamed(k.name)
		if held != k.inTable {
			t.Errorf("%s: KindNamed=%v, want %v", k.name, held, k.inTable)
		}
		if held {
			gated += k.count
			continue
		}
		drifted += k.count
		near, ok := NearestKind(k.name)
		if !ok {
			// A name §5 declares but this binary does not build yet (sweep, rebase,
			// mutation-kill) has no nearest name and must not be given one: its remedy
			// is the task that builds it, not a different kind.
			if k.name != "sweep" && k.name != "rebase" && k.name != "mutation-kill" {
				t.Errorf("%s (%d cards) has no name in the table and no nearest one: the refusal can only say no", k.name, k.count)
			}
			continue
		}
		if _, ok := KindNamed(near); !ok {
			t.Errorf("%s: the nearest name %q is not in the table either", k.name, near)
		}
		remedy := UnknownKindRemedy(k.name)
		if !strings.Contains(remedy, near) || !strings.Contains(remedy, "SPEC-TOOLWORK") {
			t.Errorf("%s: the remedy names neither the nearest kind nor the spec road: %q", k.name, remedy)
		}
	}
	if drifted == 0 || gated == 0 {
		t.Fatalf("the measurement is empty: gated=%d drifted=%d", gated, drifted)
	}
}

func TestNearestKindIsSilentForAKindTheTableHolds(t *testing.T) {
	for _, k := range Kinds {
		if near, ok := NearestKind(k.Name); ok {
			t.Errorf("%s is in the table and must not be drift, got nearest %q", k.Name, near)
		}
	}
	if r := UnknownKindRemedy("fix-everything"); !strings.Contains(r, "no default kind") {
		t.Errorf("a name nobody has ever cut gets the plain refusal, got %q", r)
	}
}
