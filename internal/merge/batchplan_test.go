package merge

import (
	"fmt"
	"reflect"
	"testing"
)

// The planner's own tests. Everything here is pure: a list of candidates, a conflict
// matrix somebody else measured, and the halves that come out.

// members is the shorthand these tests are written in: "#n on <base> as <head>".
func members(rows ...[3]string) []PlanMember {
	out := make([]PlanMember, 0, len(rows))
	for _, r := range rows {
		var n int
		fmt.Sscanf(r[0], "%d", &n)
		out = append(out, PlanMember{PR: n, HeadRef: r[1], Base: r[2]})
	}
	return out
}

// THE WHOLE POINT, IN ONE TEST: a pair that conflicts never shares a half.
//
// THE PAIR IS #1 AND #3, NOT #1 AND #2, and that is the whole of what makes this a test.
// Balancing alone deals the list alternately -- #1 to the first half, #2 to the second,
// #3 back to the first -- so a planner that had NO conflict rule at all would still put
// #1 and #2 apart, and a test written on that pair would pass over a planner that ignored
// its matrix entirely. #3 is the member balancing sends back to #1's half.
func TestPlanBatchesKeepsAConflictingPairApart(t *testing.T) {
	t.Parallel()
	in := members(
		[3]string{"1", "rowan/a", "dev"},
		[3]string{"2", "rowan/b", "dev"},
		[3]string{"3", "rowan/c", "dev"},
		[3]string{"4", "rowan/d", "dev"},
	)
	// Where balancing alone would put them, which is what the assertion below has to beat.
	if !sameHalf(PlanBatches(in, nil, 2, 8), 1, 3) {
		t.Fatal("with no conflicts at all #1 and #3 share a half; this test's whole point is that the matrix is what moves #3, so if they no longer do, pick the member balancing puts beside #1 and use that")
	}
	plan := PlanBatches(in, []PlanConflict{{A: 1, B: 3, Files: []string{"base/shared.go"}}}, 2, 8)
	if plan.Members() != 4 {
		t.Fatalf("every member is in a half: %v", plan.Halves)
	}
	if sameHalf(plan, 1, 3) {
		t.Errorf("#1 and #3 conflict and are in one half: %v", plan.Halves)
	}
	if len(plan.Dropped) != 0 {
		t.Errorf("nothing needed dropping here, got %v", plan.Dropped)
	}
	// The matrix comes back normalised: lower number first, one entry per pair, sorted.
	if len(plan.Conflicts) != 1 || plan.Conflicts[0].A != 1 || plan.Conflicts[0].B != 3 {
		t.Errorf("conflicts = %v, want the one pair 1,3", plan.Conflicts)
	}
}

// A member that conflicts with somebody in EVERY half is dropped by name, with the reason.
// The alternative is a half carrying a pair the gate will spend a whole round discovering,
// which is the cost this verb exists to remove.
func TestPlanBatchesDropsAMemberThatConflictsWithEveryHalf(t *testing.T) {
	t.Parallel()
	in := members(
		[3]string{"1", "rowan/a", "dev"},
		[3]string{"2", "rowan/b", "dev"},
		[3]string{"3", "rowan/c", "dev"},
	)
	plan := PlanBatches(in, []PlanConflict{{A: 1, B: 3}, {A: 2, B: 3}}, 2, 8)
	if got := plan.DroppedPRs(); !reflect.DeepEqual(got, []int{3}) {
		t.Fatalf("dropped = %v, want [3]; #3 conflicts with #1 and with #2, which are in the two halves", got)
	}
	if plan.Dropped[0].Why == "" {
		t.Error("a member dropped with no reason is the silent skip this verb replaces")
	}
	if plan.Members() != 2 {
		t.Errorf("the other two still land: %v", plan.Halves)
	}
}

// A STACK TRAVELS TOGETHER AND IN ORDER. #2 is based on #1's head branch, so the two are
// one unit: split across halves, the lower one lands into a branch that is not there.
func TestPlanBatchesKeepsAStackTogetherAndInOrder(t *testing.T) {
	t.Parallel()
	in := members(
		[3]string{"1", "rowan/a", "dev"},
		[3]string{"2", "rowan/b", "rowan/a"},
		[3]string{"3", "rowan/c", "dev"},
		[3]string{"4", "rowan/d", "dev"},
	)
	plan := PlanBatches(in, nil, 2, 8)
	if !sameHalf(plan, 1, 2) {
		t.Fatalf("#2 is based on #1's branch and they are in different halves: %v", plan.Halves)
	}
	for _, half := range plan.Halves {
		at1, at2 := indexOf(half, 1), indexOf(half, 2)
		if at1 >= 0 && at2 >= 0 && at1 > at2 {
			t.Errorf("#2 lands before #1 it is based on: %v", half)
		}
	}
	// The stack is named in the order it lands whatever order the list named it in.
	units := PlanUnits(members(
		[3]string{"2", "rowan/b", "rowan/a"},
		[3]string{"1", "rowan/a", "dev"},
	))
	if len(units) != 1 || !reflect.DeepEqual(units[0], []int{1, 2}) {
		t.Errorf("units = %v, want one unit [1 2] whichever order they were named in", units)
	}
}

// --max-members is the ceiling on ONE half, and it is what keeps a half inside the darwin
// merge leg's shard budget (#1372). A unit that fits in no half is dropped saying so, and
// the reason distinguishes "full" from "conflicts" -- two different things to do about it.
func TestPlanBatchesHoldsEveryHalfToMaxMembers(t *testing.T) {
	t.Parallel()
	in := members(
		[3]string{"1", "rowan/a", "dev"},
		[3]string{"2", "rowan/b", "dev"},
		[3]string{"3", "rowan/c", "dev"},
		[3]string{"4", "rowan/d", "dev"},
		[3]string{"5", "rowan/e", "dev"},
	)
	plan := PlanBatches(in, nil, 2, 2)
	for i, half := range plan.Halves {
		if len(half) > 2 {
			t.Errorf("half %d holds %d members, over --max-members 2: %v", i+1, len(half), half)
		}
	}
	if got := plan.DroppedPRs(); !reflect.DeepEqual(got, []int{5}) {
		t.Fatalf("dropped = %v, want [5]: two halves of two hold four", got)
	}
	if plan.Dropped[0].Why != "every half is at --max-members" {
		t.Errorf("why = %q; a caller reading it must be able to tell a full plan from a conflicting one", plan.Dropped[0].Why)
	}
}

// THE SAME INPUTS ANSWER THE SAME PLAN. A plan that came out differently on two runs is a
// plan nobody can diff, which is the whole use a caller has for one.
func TestPlanBatchesIsDeterministic(t *testing.T) {
	t.Parallel()
	in := members(
		[3]string{"885", "rowan/a", "dev"},
		[3]string{"744", "rowan/b", "dev"},
		[3]string{"660", "rowan/c", "rowan/b"},
		[3]string{"739", "rowan/d", "dev"},
		[3]string{"846", "rowan/e", "dev"},
	)
	conflicts := []PlanConflict{{A: 744, B: 846}, {A: 885, B: 739}}
	first := PlanBatches(in, conflicts, 2, 8)
	for i := 0; i < 5; i++ {
		again := PlanBatches(in, conflicts, 2, 8)
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("run %d answered a different plan:\n%v\n%v", i, first, again)
		}
	}
	// And the stacked member is with its parent even when the parent has a conflict of
	// its own to place around.
	if !sameHalf(first, 744, 660) {
		t.Errorf("#660 is based on #744's branch and they are apart: %v", first.Halves)
	}
}

// A member whose base is a branch no member owns is a root, which is every ordinary pull
// request -- and a cycle, which a forge cannot make, costs a worse plan and never a lost
// member or a hang.
func TestPlanUnitsLosesNobody(t *testing.T) {
	t.Parallel()
	for _, in := range [][]PlanMember{
		members([3]string{"1", "rowan/a", "dev"}, [3]string{"2", "rowan/b", "dev"}),
		members([3]string{"1", "rowan/a", "rowan/b"}, [3]string{"2", "rowan/b", "rowan/a"}),
		members([3]string{"1", "rowan/a", "rowan/a"}),
		members([3]string{"1", "", ""}, [3]string{"2", "", ""}),
	} {
		seen := map[int]int{}
		for _, unit := range PlanUnits(in) {
			for _, n := range unit {
				seen[n]++
			}
		}
		for _, m := range in {
			if seen[m.PR] != 1 {
				t.Errorf("#%d is in %d units, want exactly one (input %v)", m.PR, seen[m.PR], in)
			}
		}
	}
}

func sameHalf(plan BatchPlan, a, b int) bool {
	for _, half := range plan.Halves {
		if indexOf(half, a) >= 0 && indexOf(half, b) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(half []int, n int) int {
	for i, m := range half {
		if m == n {
			return i
		}
	}
	return -1
}
