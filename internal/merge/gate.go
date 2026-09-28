package merge

import (
	"sort"
	"strings"
)

// GateStanding summarizes approvals and holds for a set of verdicts at a given head.
type GateStanding struct {
	Approves  int
	Holds     int
	Approvers []string
	Held      bool
	Satisfied bool
}

// CountTypedApproves counts unique approvers with a valid record-grade APPROVE
// at the target head, who are not the author and have no active unreleased hold.
func CountTypedApproves(vs []Verdict, head, author string, rs *ReviewerSet) int {
	st := EvaluateVerdicts(vs, head, author, rs)
	return st.Approves
}

// CountApproves counts unique approvers with a valid APPROVE at the target head.
func CountApproves(vs []Verdict, head, author string, rs *ReviewerSet) int {
	return CountTypedApproves(vs, head, author, rs)
}

// EvaluateVerdicts evaluates all verdicts (lane records and forge comments/reviews)
// for an entry at current head, returning approval and hold standing.
//
// Approvals strictly require record provenance (v.Source == "record"); forge claims
// without record provenance are never counted as approvals.
func EvaluateVerdicts(vs []Verdict, head, author string, rs *ReviewerSet) GateStanding {
	var st GateStanding
	unreleased := UnliftedHolds(vs, head, author, rs)
	st.Holds = len(unreleased)
	st.Held = len(unreleased) > 0

	activeHolderMap := make(map[string]bool)
	for _, h := range unreleased {
		activeHolderMap[strings.ToLower(strings.TrimSpace(h.Who))] = true
	}

	seen := make(map[string]bool)
	for _, v := range vs {
		if v.Kind != "" && v.Kind != "line" {
			continue
		}
		if v.Word != "approve" {
			continue
		}
		// S6 / review-record authority: forge claims without record provenance
		// are not counted as approvals.
		if v.Source != "record" {
			continue
		}
		if !headMatch(v.Head, head) {
			continue
		}
		if v.Scope != "" {
			continue
		}
		who := strings.TrimSpace(v.Who)
		if who == "" || strings.EqualFold(who, "unknown") {
			continue
		}
		if sameLine(who, author) {
			continue
		}
		if rs != nil && !rs.MayHold(who) {
			continue
		}
		whoLower := strings.ToLower(who)
		if activeHolderMap[whoLower] {
			continue
		}
		if !seen[whoLower] {
			seen[whoLower] = true
			st.Approves++
			st.Approvers = append(st.Approvers, who)
		}
	}
	sort.Strings(st.Approvers)
	st.Satisfied = !st.Held && st.Approves > 0
	return st
}
