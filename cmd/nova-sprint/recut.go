package main

import (
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// recut <card> (--tier <route> | --brief-file <path>) [--id <new-id>]:
// re-cuts an existing card for another tier or scope while keeping id lineage
// via --replaces, instead of drop plus add (docs/SPEC-SPRINT.md section 2,
// "A card replaced by its twin"; item 21). Recut makes the twin, relinks dependants,
// drops the old with the lineage recorded, on the twin store.
func (a *app) cmdRecut(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("recut")
	tier := fs.String("tier", "", fmt.Sprintf("the tier to re-cut the card for (%s)", cardhdr.RouteList))
	briefFile := fs.String("brief-file", "", "the new brief, read from this file: its bytes as they are, its one trailing newline cut; the file's base name without .md is the twin's id unless --id is given")
	id := fs.String("id", "", "the twin's card id (default: derived from the card id and tier, or the brief file's base name)")
	rules := fs.String("rules", "", "the child rules file the brief is held to (default: the file init --rules recorded, else the built-in general rules)")
	ans := fs.String("answers", "", "the judgment notifications this answers, comma separated; coordinator-only; one invalid answer refuses the whole step, writing nothing")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "recut", err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, "recut", "wants one card to re-cut")
	}
	if *tier == "" && *briefFile == "" {
		return refuse(stderr, "recut", "recut wants --tier <t> or --brief-file <f>")
	}
	if *tier != "" && !cardhdr.IsRoute(*tier) {
		return refuse(stderr, "recut", "--tier wants "+cardhdr.RouteList+", found "+*tier)
	}
	var briefText string
	var rs0 ruleSet
	var st *store.Store
	if *briefFile != "" {
		text, err := readBriefFile(*briefFile)
		if err != nil {
			return refuse(stderr, "recut", "--brief-file: "+err.Error())
		}
		briefText = text
		if *tier != "" {
			briefText = sprint.SetBriefTier(briefText, *tier)
		}
		var code int
		if rs0, code = a.holdBrief("recut", briefText, *rules, c, &st, stderr); code != 0 {
			return code
		}
		c.says = append(c.says, unfilledSays("the brief", briefText)...)
	}
	if st == nil {
		var err error
		if st, err = a.store(*c); err != nil {
			return refuse(stderr, "recut", err.Error())
		}
	}
	if briefText != "" {
		if code := a.holdWho("recut", st, stderr, briefText); code != 0 {
			return code
		}
	}
	var heldRules string
	if briefText != "" {
		heldRules = cardRules(briefText, rs0).held
	}
	r := sprint.RecutReq{
		Old:       pos[0],
		New:       *id,
		Tier:      *tier,
		Brief:     briefText,
		BriefFile: *briefFile,
		Rules:     heldRules,
		Who:       c.actor,
		Answers:   answers(*ans),
	}
	return a.runStep("recut", *c, st, store.RecutStep(r), stdout, stderr)
}
