package member

import (
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/cardtree"
)

// treeFinish is a tree card's result read step by step (docs/SPEC-SPRINT.md, a card is a
// tree of steps; the owner, 2026-10-02: a failed step n "lands 1..n-1 and redeals n.. as a
// new card"). The result's step lines name a verdict per work step; the first that is not ok
// is the failed step. With no failed step, or a card that is no tree, the result is as it
// was. A failed first step is a failed finish naming the step (Result.Step). A later failed
// step pushes the commit of the last ok step before it and finishes ok there, so steps
// 1..n-1 are read and land on the unchanged path, and the report names the remainder card
// `<id>-r<n>` (cardtree.Remainder: the brief from step n, needing this card).
func treeFinish(p Packet, r Result) Result {
	if !r.Shaped || r.Verdict == "nothing" {
		return r
	}
	t := cardtree.Parse(p.Brief)
	if !t.IsTree() || len(t.Work()) == 0 {
		return r
	}
	v := cardtree.ParseVerdicts(r.Report + "\n" + r.Body)
	if len(v) == 0 {
		return r // no step lines at all: the card's own verdict stands, as for a flat card
	}
	failed, land := cardtree.Land(t, v)
	if failed == nil {
		return r
	}
	why := "step " + failed.Num + " " + failed.Verdict
	if failed.Words != "" {
		why += ": " + oneLine(failed.Words)
	}
	if land == "" {
		r.Step = why
		return r
	}
	r.Head, r.Verdict, r.Step = land, "ok", ""
	r.Report = cardhdr.RemainderKey + cardtree.RemainderID(p.Card, failed.Num) + " " + why
	return r
}
