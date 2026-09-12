package merge

import (
	"fmt"
	"sort"
	"strings"
)

// Work list 7: the read condition.
//
//	a hold anywhere          -> HOLD, never merges, whatever the checks say
//	needs_read=no            -> satisfied
//	needs_read=yes and an approve recorded by a line that is NOT the entry's author,
//	  for this entry's CURRENT head
//	                         -> satisfied
//	anything else            -> NEEDS-READ, waits
//
// Two of those words carry a failure each. NOT THE AUTHOR: the prototype counted any
// recorded approve, which made a self-approve indistinguishable from a read, and the
// whole point of the condition is that somebody other than the writer looked. CURRENT
// HEAD: a reader finished reading H1, the author pushed H2, and the approve recorded a
// minute later was stamped H2 -- so the verdict carries the sha the READER supplied and
// a pass counts it only while that sha is the entry's oid.

// Standing is one entry's reads, as the pass sees them.
type Standing struct {
	Approves  int      // for the current head, whoever recorded them
	Holds     int      // for the current head
	Stale     int      // kept, counted, and authorizing nothing
	Approvers []string // the names whose approve SATISFIES the condition, for MERGE OK
	Held      bool
	Satisfied bool
}

// Field is the read=<n>a/<n>h token every entry line carries.
func (s Standing) Field() string { return fmt.Sprintf("%da/%dh", s.Approves, s.Holds) }

// ReadNames is MERGE OK's read= field: who the merge rests on, or none-required.
func (s Standing) ReadNames(needsRead bool) string {
	if !needsRead {
		return "none-required"
	}
	if len(s.Approvers) == 0 {
		return "-"
	}
	return strings.Join(s.Approvers, ",")
}

// EvaluateReads applies the condition to one entry's folded records.
//
// Per (who, head) the newest `at` decides, and two with one `at` to the second fold
// HOLD-LAST, as rule 18 folds red-last: a tie between a hold and an approve is a tie
// this tool resolves in the direction that does not merge. The tool deletes no record,
// and the lane branch's history keeps the hold.
func EvaluateReads(e *Entry, author string) Standing {
	var st Standing
	newest := map[string]Read{}
	order := []string{}
	records := append([]Read{}, e.Reads...)
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].At != records[j].At {
			return records[i].At < records[j].At
		}
		// One `at` to the second: the hold folds last, so it is the one that decides.
		return records[i].Verdict == "approve" && records[j].Verdict == "hold"
	})
	for _, r := range records {
		if r.Head != e.OID || e.OID == "" {
			st.Stale++
			continue
		}
		key := r.Who + "\x00" + r.Head
		if _, seen := newest[key]; !seen {
			order = append(order, key)
		}
		newest[key] = r
	}
	for _, key := range order {
		r := newest[key]
		if r.Verdict == "hold" {
			st.Holds++
			st.Held = true
			continue
		}
		st.Approves++
		// An approve from the author is recorded, is shown, and does not count: the
		// condition is that somebody other than the writer looked.
		if !sameLine(r.Who, author) {
			st.Approvers = append(st.Approvers, r.Who)
		}
	}
	sort.Strings(st.Approvers)
	st.Satisfied = !st.Held && (e.NeedsRead != "yes" || len(st.Approvers) > 0)
	if st.Held {
		st.Satisfied = false
	}
	return st
}

// sameLine resolves a recorded `who` against the entry's author. The resolution is by
// name, case-folded, and by nothing the host says about identity: a host login is
// evidence about an ACCOUNT, and `who` is a line at a keyboard.
func sameLine(who, author string) bool {
	return author != "" && strings.EqualFold(strings.TrimSpace(who), strings.TrimSpace(author))
}

// GateStand is rule 18's standing of an entry's gate records, in one word.
//
//	merge  a record for (oid, current base sha) -- the predicate's first line, whatever
//	       its colour: the NEWEST one decides, and a red never loses a tie
//	head   a green record for oid against an OLDER base: a candidate, not a verdict
//	stale  a record whose head is not the entry's current oid: the gate was spent
//	-      none
type GateStand struct {
	Kind   string
	Record *Gate
}

// StandOfGates picks the record rule 18 names. Records are ordered by `at`, and two
// records with one `at` to the second fold with the RED LAST, so a red never loses a tie:
// an older green must never survive a newer red, and a tie is the case where "older" is
// not a word that means anything.
func StandOfGates(gates []Gate, entry, oid, baseSHA string) GateStand {
	mine := []Gate{}
	for _, g := range gates {
		if g.ID() == entry {
			mine = append(mine, g)
		}
	}
	sort.SliceStable(mine, func(i, j int) bool {
		if mine[i].At != mine[j].At {
			return mine[i].At < mine[j].At
		}
		return mine[i].Verdict == "green" && mine[j].Verdict == "red"
	})
	var pair *Gate
	var head *Gate
	stale := false
	for i := range mine {
		g := mine[i]
		switch {
		case oid != "" && g.Head == oid && g.Base == baseSHA:
			pair = &mine[i]
		case oid != "" && g.Head == oid && g.Verdict == "green":
			head = &mine[i]
		case oid == "" || g.Head != oid:
			stale = true
		}
	}
	switch {
	case pair != nil:
		return GateStand{Kind: "merge", Record: pair}
	case head != nil:
		return GateStand{Kind: "head", Record: head}
	case stale:
		return GateStand{Kind: "stale"}
	default:
		return GateStand{Kind: "-"}
	}
}

// Green reports whether the deciding record for (oid, base) is a green one -- the
// predicate's first line.
func (s GateStand) Green() bool {
	return s.Kind == "merge" && s.Record != nil && s.Record.Verdict == "green"
}

// Red reports whether the deciding record for (oid, base) is a red one, which makes the
// entry RED with both shas in detail.
func (s GateStand) Red() bool {
	return s.Kind == "merge" && s.Record != nil && s.Record.Verdict == "red"
}
