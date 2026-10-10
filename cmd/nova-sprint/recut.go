package main

import (
	"context"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// recut <id> (--tier <t> | --brief-file <path>) [--new <id>]: a card re-cut as its twin
// for another task (sprint.Recut; docs/SPEC-SPRINT.md section 2, "A card replaced by its
// twin"): add --replaces of the old card with the tier or the brief changed, so its
// dependents need the twin, the old card is dropped "replaced by <new>", no blocked
// judgment is raised, and the twin records the id it replaces. A new brief is held to the
// card lint as brief holds one. A twin is for a card whose task changed: a brief
// correction, or a PATHS widening, is brief <id> in place (the owner, 2026-10-06: "We
// gotta stop doing this twin shit. it's waste."), and --widen, retired, says so. The
// coordinator's alone.
func (a *app) cmdRecut(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("recut")
	tier := fs.String("tier", "", "the tier the twin is pinned to ("+cardhdr.RouteList+"): every deal and read of it draws its route from it (default: the old card's pin)")
	briefFile := fs.String("brief-file", "", "the twin's brief, read from this file and held to the card lint as brief holds one; its DEPENDS-ON: line's needs are taken with the old card's (default: the old card's brief)")
	rules := fs.String("rules", "", "with --brief-file: the child rules file the brief is held to (default: the file init --rules recorded, else the built-in general rules)")
	widen := fs.Bool("widen", false, "retired: a PATHS widening edits the card in place, the same id; run: nova-sprint brief <id> --widen")
	fs.String("repo-dir", "", "retired with --widen: give it to brief <id> --widen")
	nw := fs.String("new", "", "the twin's `id` (default: the old id with the next letter, b for a card never re-cut, c for its twin re-cut, and so on)")
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "recut", err.Error())
	}
	if *widen {
		id := "<id>"
		if len(ids) == 1 {
			id = ids[0]
		}
		return refuse(stderr, "recut", "--widen is retired: a PATHS widening edits the card in place, the same id and no twin; nothing was changed; run: nova-sprint brief "+id+" --widen [--repo-dir <clone>]")
	}
	switch {
	case len(ids) != 1 || (*tier == "" && *briefFile == ""):
		return refuse(stderr, "recut", "wants one primary and --tier <"+cardhdr.RouteList+">, --brief-file <path>, or both: what its twin changes")
	case flagValue(fs, "repo-dir") != "":
		return refuse(stderr, "recut", "--repo-dir went with --widen, which is retired: run: nova-sprint brief <id> --widen --repo-dir <clone>")
	case *tier != "" && !cardhdr.IsRoute(*tier):
		return refuse(stderr, "recut", "--tier wants "+cardhdr.RouteList+", found "+oneline.Escape(*tier))
	case *rules != "" && *briefFile == "":
		return refuse(stderr, "recut", "--rules goes with --brief-file: the old brief keeps its rules")
	}
	r := sprint.RecutReq{ID: ids[0], New: *nw, Tier: *tier, Who: c.actor}
	var st *store.Store
	if *briefFile != "" {
		text, err := readBriefFile(*briefFile)
		if err != nil {
			return refuse(stderr, "recut", "--brief-file: "+err.Error())
		}
		rs, code := a.holdBrief("recut", text, *rules, c, &st, stderr)
		if code != 0 {
			return code
		}
		r.Brief, r.Rules, r.Needs = text, cardRules(text, rs).held, uniquify(briefNeeds(text))
	}
	if st == nil {
		if st, err = a.store(*c); err != nil {
			return refuse(stderr, "recut", err.Error())
		}
	}
	if r.Brief != "" {
		// the card's own stream, for its named friend's restriction (holdWho); a card the
		// store does not know is left to the write step's refusal
		stream := ""
		if info, err := st.CardOf(context.Background(), ids[0]); err == nil && info.Primary != nil {
			stream = info.Primary.F("stream")
		}
		if code := a.holdWho("recut", st, stderr, []string{stream}, r.Brief); code != 0 {
			return code
		}
		c.says = append(c.says, unfilledSays("the brief of the twin of "+ids[0], r.Brief)...)
	}
	return a.runStep("recut", *c, st, store.RecutStep(r), stdout, stderr)
}
