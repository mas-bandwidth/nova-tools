package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/cardlimits"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// cmdBrief replaces the brief of a primary that has not started, on a running
// machine as on a stopped one (changing a sprint happens through the verbs,
// never by hand; docs/SPEC-SPRINT.md, the brief verb): the brief held to the
// card lint as add's is (holdBrief), then one step (sprint.BriefOrTwin), refused for
// a card dealt unless a judgment offering brief holds it at its bound, when the card is
// replaced by its twin with the new brief; else the card keeps its id, stream, score and needs. --dir replaces
// one brief per file (cmdBriefDir).
func (a *app) cmdBrief(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("brief")
	brief := fs.String("brief", "", fmt.Sprintf("the new brief: a child's whole brief, at most %d KiB, held to the card lint as add holds one (--rules, else the file init --rules recorded, else the built-in general rules) and refused, exit 2, nothing written, when it fails; a primary waiting or ready with no work card dealt takes one, on a STOPPED machine or a RUNNING one (there applied at its next tick), and a card dealt keeps its brief, unless a judgment offering brief holds it at its bound: then it is replaced by its twin with the new brief (recut's twin id, the old card dropped \"replaced by\" the twin, every waiting card that needed it re-pointed, the judgment answered), in one step; one that differs in its DEPENDS-ON: line alone is taken in any state, the machine running or the card dealt, and re-points the card's needs", cardlimits.MaxBriefBytes>>10))
	briefFile := fs.String("brief-file", "", "the new brief, read from this file: its bytes as they are, its one trailing newline cut; not with --brief")
	dir := fs.String("dir", "", "a directory of new briefs: one per *.md file, the card its base name without .md, each read and held as --brief-file's; one bad file refuses the whole call, nothing written; not with an id, --brief or --brief-file")
	rules := fs.String("rules", "", "the child rules file the brief is held to (default: the file init --rules recorded, else the built-in general rules)")
	tier := fs.String("tier", "", "re-tier the card instead of replacing its brief: the tier ("+cardhdr.RouteList+") every later deal and read of the card draws its route from, kept on the card as rework --tier keeps it (it pins the card, never escalated past it); taken on a RUNNING machine and for a card dealt, where it applies to the next attempt; not with --brief or --brief-file")
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "brief", err.Error())
	}
	if *tier != "" {
		switch {
		case len(ids) != 1 || *brief != "" || *briefFile != "" || *rules != "" || *dir != "":
			return refuse(stderr, "brief", "--tier wants one primary and no --brief, --brief-file, --dir or --rules: it re-tiers the card and replaces nothing")
		case !cardhdr.IsRoute(*tier):
			return refuse(stderr, "brief", "--tier wants "+cardhdr.RouteList+", found "+oneline.Escape(*tier))
		}
		st, err := a.store(*c)
		if err != nil {
			return refuse(stderr, "brief", err.Error())
		}
		return a.runStep("brief", *c, st, store.BriefStep(sprint.BriefReq{ID: ids[0], Tier: *tier, Who: c.actor}), stdout, stderr)
	}
	if *dir != "" {
		if len(ids) > 0 || *brief != "" || *briefFile != "" {
			return refuse(stderr, "brief", "--dir names each card by its file: give no id, --brief or --brief-file with it")
		}
		return a.cmdBriefDir(*dir, *rules, c, stdout, stderr)
	}
	if len(ids) != 1 || (*brief == "") == (*briefFile == "") {
		return refuse(stderr, "brief", "wants one primary and one of --brief <text>, --brief-file <path>, --dir <dir> alone, or --tier <t>")
	}
	if *briefFile != "" {
		text, err := readBriefFile(*briefFile)
		if err != nil {
			return refuse(stderr, "brief", "--brief-file: "+err.Error())
		}
		*brief = text
	}
	var st *store.Store
	rs, code := a.holdBrief("brief", *brief, *rules, c, &st, stderr)
	if code != 0 {
		return code
	}
	return a.replaceBriefs([]sprint.CardAdd{{ID: ids[0], Brief: *brief}}, rs, c, st, stdout, stderr)
}

// cmdBriefDir is brief --dir (docs/SPEC-SPRINT.md, the brief verb): one brief
// per *.md file of dir in byte order of file name, read as add --brief-dir
// reads them (decide.CardFilePaths), each card the file's base name without
// .md; every brief is held to the size bound and the card lint before
// anything is written, one bad file refusing the whole call naming it, and one
// step replaces them all or none, its moved= the count replaced.
func (a *app) cmdBriefDir(dir, rules string, c *common, stdout, stderr io.Writer) int {
	files, err := decide.CardFilePaths(dir)
	if err != nil {
		return refuse(stderr, "brief", "--dir: "+err.Error())
	}
	if len(files) == 0 {
		return refuse(stderr, "brief", fmt.Sprintf("--dir %s holds no *.md file; write one brief per card there, named <id>.md", dir))
	}
	cards := make([]sprint.CardAdd, 0, len(files))
	for _, path := range files {
		id := strings.TrimSuffix(filepath.Base(path), ".md")
		if !sprint.ValidID(id) {
			return refuse(stderr, "brief", fmt.Sprintf("%s: the card id is the file's base name without .md, and %q is not one (letters, digits, _ and -)", path, id))
		}
		text, err := readBriefFile(path)
		if err != nil {
			return refuse(stderr, "brief", err.Error())
		}
		if len(text) > store.MaxBriefBytes {
			return refuse(stderr, "brief", fmt.Sprintf("%s: the brief is %d bytes, over the %d bytes a brief may be; a brief is a child's whole brief; shorten it", path, len(text), store.MaxBriefBytes))
		}
		cards = append(cards, sprint.CardAdd{ID: id, Brief: text, File: path})
	}
	var st *store.Store
	rs, code := a.briefRules("brief", rules, c, &st, stderr)
	if code != 0 {
		return code
	}
	if code := lintBriefFiles("brief", cards, rs, c.max, stderr); code != 0 {
		return code
	}
	return a.replaceBriefs(cards, rs, c, st, stdout, stderr)
}

// replaceBriefs is brief's write, one step for every card (sprint.Brief): each
// brief's WHO line held to the friends table (holdWho), each card naming the
// rules the member injects into it (cardRules).
func (a *app) replaceBriefs(cards []sprint.CardAdd, rs ruleSet, c *common, st *store.Store, stdout, stderr io.Writer) int {
	if st == nil { // --rules named the rule set: briefRules opened no store
		var err error
		if st, err = a.store(*c); err != nil {
			return refuse(stderr, "brief", err.Error())
		}
	}
	req := sprint.BriefReq{Who: c.actor}
	texts := make([]string, len(cards))
	for i, cd := range cards {
		texts[i] = cd.Brief
		req.Cards = append(req.Cards, sprint.BriefCard{ID: cd.ID, Brief: cd.Brief, Rules: cardRules(cd.Brief, rs).held, Needs: uniquify(briefNeeds(cd.Brief))})
		c.says = append(c.says, unfilledSays("the brief of "+cd.ID, cd.Brief)...)
	}
	if code := a.holdWho("brief", st, stderr, texts...); code != 0 {
		return code
	}
	return a.runStep("brief", *c, st, briefStep(req), stdout, stderr)
}

// briefStep is the brief verb's step (sprint.BriefOrTwin): it reads what recut reads, every
// table and the ids the twin may take, since a card at its bound is re-cut as its twin; on
// any other card it is Brief's step.
func briefStep(req sprint.BriefReq) store.Step {
	plain := store.BriefStep(req)
	if len(req.Cards) != 1 {
		return plain
	}
	b := req.Cards[0]
	step := store.RecutStep(sprint.RecutReq{ID: b.ID, Brief: b.Brief, Rules: b.Rules, Needs: b.Needs, Who: req.Who})
	step.Verb, step.Args = plain.Verb, plain.Args
	step.Plan = func(s *sprint.Snapshot) sprint.Plan { return sprint.BriefOrTwin(s, req) }
	return step
}
