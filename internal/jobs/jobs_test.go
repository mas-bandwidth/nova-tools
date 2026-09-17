package jobs

import (
	"strings"
	"testing"
)

// seedOpenNeed is the section 1 specimen: a needs b, b is an open PR (not merged and
// green), and c needs nothing. a is blocked; b and c are ready.
func seedOpenNeed(t *testing.T) *Graph {
	t.Helper()
	g, err := Seed([]Node{
		{ID: "a", Needs: []string{"b"}},
		{ID: "b"},
		{ID: "c"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return g
}

// launch-reads-the-ready-set-not-the-queue (docs/SPEC-JOBS.md section 1, line 34):
// launch reads the ready set and nothing else; a card whose need is an open PR is
// never on a slot.
func TestLaunchReadsTheReadySetNotTheQueue(t *testing.T) {
	g := seedOpenNeed(t)

	// a's one need is b, an open PR: a cannot proceed, and its row names the exact
	// blocker and the join that resolves it.
	ready, blocker := g.Ready("a")
	if ready {
		t.Fatalf("a is ready while its need b is an open PR")
	}
	if blocker == nil {
		t.Fatalf("a's row is not ready and names no blocker")
	}
	if blocker.Need != "b" {
		t.Fatalf("a's blocker = %q, want the unmet need b", blocker.Need)
	}
	if blocker.State != "open" {
		t.Fatalf("a's blocker state = %q, want open (an open PR)", blocker.State)
	}
	if blocker.Resolver != "nova-merge queue" {
		t.Fatalf("a's resolver = %q, want the nova-merge queue join", blocker.Resolver)
	}

	// The queue holds a first. Launch must read the ready set, so a stays off a slot.
	launch := g.Launch([]string{"a", "c"})
	if len(launch) != 1 || launch[0] != "c" {
		t.Fatalf("launch(%v) = %v, want [c]: a is queued but its need is an open PR", []string{"a", "c"}, launch)
	}

	// The ready set is the nodes whose count of unmet needs is zero, in seed order.
	if got := g.ReadySet(); !sameIDs(got, []string{"b", "c"}) {
		t.Fatalf("ready set = %v, want [b c]", got)
	}
}

// a-needs-cycle-refuses-at-seed (docs/SPEC-JOBS.md section 1, line 35): a :deps cycle
// is refused before publication, so the graph can never deadlock.
func TestANeedsCycleRefusesAtSeed(t *testing.T) {
	_, err := Seed([]Node{
		{ID: "a", Needs: []string{"b"}},
		{ID: "b", Needs: []string{"a"}},
	})
	if err == nil {
		t.Fatal("a :deps cycle seeded without a refusal")
	}
	// The refusal is validator rule 3, it names the :deps cycle, and it names the
	// nodes the cycle runs through.
	for _, want := range []string{"rule 3", ":deps", "cycle", "a", "b"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("cycle refusal %q does not name %q", err, want)
		}
	}
}

// blocks is the reverse edge written by the same insert as needs.
func TestBlocksIsTheReverseEdgeOfTheSameInsert(t *testing.T) {
	g := seedOpenNeed(t)
	if got := g.Needs("a"); !sameIDs(got, []string{"b"}) {
		t.Fatalf("needs(a) = %v, want [b]", got)
	}
	if got := g.Blocks("b"); !sameIDs(got, []string{"a"}) {
		t.Fatalf("blocks(b) = %v, want [a]", got)
	}
	if got := g.ReadySet(); !sameIDs(got, []string{"b", "c"}) {
		t.Fatalf("ready set = %v, want [b c]", got)
	}
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
