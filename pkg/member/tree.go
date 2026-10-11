package member

import (
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/cardtree"
)

// treeFinishWith is a tree card's result read step by step (docs/SPEC-SPRINT.md, a card is a
// tree of steps, the coordinator's failed-step rule of 2026-10-02). The step lines of the
// result's body name a verdict per work step; the first that is not ok is the failed step. With
// no failed step, a card that is no tree, or a body with no step line, the result is as it was.
// A failed first step is a failed finish naming the step (Result.Step). A later failed step
// pushes the commit of the last ok step before it and finishes ok there, so steps 1..n-1 are
// read and land on the unchanged path, and the report names the remainder card `<id>-r<n>`
// (cardtree.Remainder: the brief from step n, staged at that commit, needing this card).
// resolve turns a step's commit into a sha: the finish paths pass stepResolve, which reads the
// branch, or acceptFullSha (a 40-hex sha as itself) when the member has no clone.
func treeFinishWith(p Packet, r Result, resolve cardtree.ShaResolve) Result {
	if !r.Shaped || r.Verdict == "nothing" {
		return r
	}
	t := cardtree.Parse(p.Brief)
	if !t.IsTree() || len(t.Work()) == 0 {
		return r
	}
	v := cardtree.ParseVerdicts(r.Body, resolve)
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
