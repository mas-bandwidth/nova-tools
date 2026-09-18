package merge

import (
	"sort"
	"strings"
)

// THE PLAN IN FRONT OF THE GATE.
//
// 2026-09-18: every batch gate that morning ran two and three rounds. The gate merges the
// members IN ORDER and drops the first head that will not merge, so a batch holding five
// pairwise conflicts costs five rounds to discover -- one drop per round, each round a
// clone, a merge of everything ahead of it, a build, a vet and the whole test suite. The
// conflicts were there before the first round started and none of them needed a test run
// to find.
//
// This is that arithmetic, done once and up front: which members conflict with which, and
// an assignment of the rest into halves small enough for the darwin shards, so the gate
// runs ONCE per half over a list nobody has to whittle down.
//
// Everything here is PURE. It takes the conflict matrix somebody else measured with git
// and answers the halves; it runs no command, reads no file and reaches no forge. That is
// what makes the partition a thing a test can pin exactly, and it is why the greedy below
// is deterministic to the last tie-break: a plan that came out differently on two runs
// over the same inputs is a plan nobody can check.

// PlanMember is one candidate, as the forge described it. HeadRef and Base are read for
// ONE thing: a member whose Base is another member's HeadRef is STACKED on it, and the two
// travel together and in that order or the lower one lands into a branch that is not there.
type PlanMember struct {
	PR      int
	HeadRef string
	Base    string
}

// PlanConflict is one pair that will not merge together, and the files git named. The pair
// is unordered -- A is always the lower number -- because "these two cannot be in the same
// batch" is symmetric even though the measurement that found it merged one onto the other.
type PlanConflict struct {
	A, B  int
	Files []string
}

// PlanDrop is a candidate no half could take, and the reason in words. A member that
// vanished from the plan with no reason is the silent skip this whole verb exists to
// replace.
type PlanDrop struct {
	PR  int
	Why string
}

// BatchPlan is the answer: the halves in order, the conflicts that were found, and the
// candidates that are in no half.
type BatchPlan struct {
	Halves    [][]int
	Conflicts []PlanConflict
	Dropped   []PlanDrop
}

// Members is how many candidates the halves hold between them.
func (p BatchPlan) Members() int {
	n := 0
	for _, half := range p.Halves {
		n += len(half)
	}
	return n
}

// DroppedPRs is the dropped numbers alone, in the order they were dropped.
func (p BatchPlan) DroppedPRs() []int {
	out := make([]int, 0, len(p.Dropped))
	for _, d := range p.Dropped {
		out = append(out, d.PR)
	}
	return out
}

// PlanUnits groups the candidates into the things that must travel together: a STACK is a
// chain of members each based on the one before it, in that order, and every other member
// is a unit of one.
//
// The order is the caller's: units come out in the order their root appeared in the list
// given, and a stack's members come out parent first whatever order they were named in.
// A member based on a branch no member owns is a root, which is every ordinary pull
// request -- its base is dev.
//
// A cycle cannot happen on a forge (a pull request's base is a branch, and a branch cannot
// be based on the pull request based on it) but is HANDLED rather than trusted: a member
// this walk never reaches is emitted as a unit of its own at the end, so a cycle costs a
// worse plan and never a lost member or a hang.
func PlanUnits(members []PlanMember) [][]int {
	byHead := map[string]int{}
	for _, m := range members {
		if ref := strings.TrimSpace(m.HeadRef); ref != "" {
			byHead[ref] = m.PR
		}
	}
	held := map[int]bool{}
	children := map[int][]int{}
	var roots []int
	for _, m := range members {
		held[m.PR] = true
	}
	for _, m := range members {
		parent, ok := byHead[strings.TrimSpace(m.Base)]
		if ok && parent != m.PR && held[parent] {
			children[parent] = append(children[parent], m.PR)
			continue
		}
		roots = append(roots, m.PR)
	}
	var units [][]int
	seen := map[int]bool{}
	for _, root := range roots {
		var unit []int
		// Breadth first from the root, which is the order they land: a member is queued
		// only after the member it is based on is already in the unit.
		queue := []int{root}
		for len(queue) > 0 {
			n := queue[0]
			queue = queue[1:]
			if seen[n] {
				continue
			}
			seen[n] = true
			unit = append(unit, n)
			queue = append(queue, children[n]...)
		}
		units = append(units, unit)
	}
	// Whatever the walk did not reach: every member is in exactly one unit or the plan
	// has quietly lost one.
	for _, m := range members {
		if !seen[m.PR] {
			seen[m.PR] = true
			units = append(units, []int{m.PR})
		}
	}
	return units
}

// PlanBatches assigns the candidates to halves, and it is deterministic.
//
// THE RULE IS ZERO CONFLICTS INSIDE A HALF. A half is a batch somebody will run the gate
// over, and a batch holding a conflicting pair is the round that costs a drop and a rerun
// -- the very cost this plan exists to remove. So a unit goes into a half only when it
// conflicts with nothing already there and the half has room for it; a unit that fits
// nowhere is DROPPED BY NAME, which is a list a caller can look at rather than a plan that
// quietly holds a round it is going to lose.
//
// Greedy, in the caller's order, is enough. The exact partition is NP-hard and the input
// is a handful of pull requests picked by a person; what matters is that the answer is the
// same every time it is asked, so the tie-breaks are total: fewest members first, then the
// lower-numbered half.
func PlanBatches(members []PlanMember, conflicts []PlanConflict, halves, maxMembers int) BatchPlan {
	if halves < 1 {
		halves = 1
	}
	if maxMembers < 1 {
		maxMembers = 1
	}
	conflicting := map[[2]int]bool{}
	for _, c := range conflicts {
		conflicting[pairKey(c.A, c.B)] = true
	}
	plan := BatchPlan{Halves: make([][]int, halves), Conflicts: sortedConflicts(conflicts)}
	for _, unit := range PlanUnits(members) {
		at, why := placeUnit(plan.Halves, unit, conflicting, maxMembers)
		if at < 0 {
			for _, n := range unit {
				plan.Dropped = append(plan.Dropped, PlanDrop{PR: n, Why: why})
			}
			continue
		}
		plan.Halves[at] = append(plan.Halves[at], unit...)
	}
	return plan
}

// placeUnit is the choice, and the reason when there is none.
func placeUnit(halves [][]int, unit []int, conflicting map[[2]int]bool, maxMembers int) (int, string) {
	best, noRoom, clash := -1, 0, 0
	for i, half := range halves {
		if len(half)+len(unit) > maxMembers {
			noRoom++
			continue
		}
		if conflictsWith(half, unit, conflicting) {
			clash++
			continue
		}
		if best < 0 || len(half) < len(halves[best]) {
			best = i
		}
	}
	if best >= 0 {
		return best, ""
	}
	switch {
	case clash > 0 && noRoom > 0:
		return -1, "it conflicts with a member of every half that had room for it, and the rest are full"
	case clash > 0:
		return -1, "it conflicts with a member of every half"
	default:
		return -1, "every half is at --max-members"
	}
}

// conflictsWith reports whether any member of the unit conflicts with any member already
// in the half.
func conflictsWith(half, unit []int, conflicting map[[2]int]bool) bool {
	for _, a := range half {
		for _, b := range unit {
			if conflicting[pairKey(a, b)] {
				return true
			}
		}
	}
	return false
}

// pairKey is the unordered pair, lower number first, so a conflict recorded one way round
// is found the other.
func pairKey(a, b int) [2]int {
	if a > b {
		a, b = b, a
	}
	return [2]int{a, b}
}

// sortedConflicts normalises the matrix for printing: every pair lower number first, the
// list in pair order, and each pair once. Two runs over the same repository print the same
// lines in the same order or the plan is not a thing anybody can diff.
func sortedConflicts(in []PlanConflict) []PlanConflict {
	seen := map[[2]int]bool{}
	out := make([]PlanConflict, 0, len(in))
	for _, c := range in {
		key := pairKey(c.A, c.B)
		if seen[key] {
			continue
		}
		seen[key] = true
		files := append([]string(nil), c.Files...)
		sort.Strings(files)
		out = append(out, PlanConflict{A: key[0], B: key[1], Files: files})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	if len(out) == 0 {
		return nil
	}
	return out
}
