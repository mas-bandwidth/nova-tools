package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// One selector, one step (docs/SPEC-SPRINT.md, "One selector, one step"; the owner,
// 2026-10-04: "BATCH EVERYTHING"): brief, recut, rework, return, release, rank and drop
// take --stream <s>, --who <friend.<name>|friend|none>, --state <ready|waiting|held|merging>
// and --ids-file <path>, combinable, with --dry-run listing what would change. Each verb's
// front door here sends a call that gives a selector word to the batch path (selVerb), and
// every other call to the verb as it was. The batch path selects on the state its one store
// step reads (sprint.Selected), applies to every selected card or none, and prints one line
// per card and the total.

// selWords is the words that make a call of the verb a selector call. --stream is one for
// the verbs that did not take it before; rework, return and drop keep their --stream (a
// stream's cards, one step already) unless another selector word is given with it.
func selWords(verb string) []string {
	w := []string{"who", "state", "ids-file"}
	switch verb {
	case "brief", "recut":
		w = append(w, "stream", "set-base", "drop-who")
	case "release", "rank":
		w = append(w, "stream")
	}
	return w
}

// selCall says args give one of the verb's selector words before any --.
func selCall(verb string, args []string) bool {
	words := selWords(verb)
	for _, a := range args {
		if a == "--" {
			return false
		}
		name, ok := strings.CutPrefix(a, "--")
		if !ok {
			if name, ok = strings.CutPrefix(a, "-"); !ok {
				continue
			}
		}
		name, _, _ = strings.Cut(name, "=")
		if slices.Contains(words, name) {
			return true
		}
	}
	return false
}

// selFlags is the selector's flags and --dry-run.
type selFlags struct {
	stream, who, state, idsFile string
	dry                         bool
}

func (f *selFlags) register(fs flagSet) {
	fs.StringVar(&f.stream, "stream", "", "select the cards of this stream")
	fs.StringVar(&f.who, "who", "", "select the cards whose WHO line names this worker: friend.<name> (or the name alone) for one friend, friend for any friend's card, none for a card that names no friend")
	fs.StringVar(&f.state, "state", "", "select the cards in this state: "+strings.Join(sprint.SelectorStates, ", ")+" (waiting is not held)")
	fs.StringVar(&f.idsFile, "ids-file", "", "select the cards this file names, one id a line (blank lines and # lines skipped); an id that is no card on the table, or that the rest of the selector does not hold of, refuses the whole call")
	fs.BoolVar(&f.dry, "dry-run", false, "list what the call would change, one line per card and the total, and write nothing")
}

// selector is the flags as a selector, or why they do not make one.
func (f *selFlags) selector() (sprint.Selector, string) {
	q := sprint.Selector{Stream: f.stream, Who: f.who, State: f.state}
	if f.idsFile != "" {
		b, err := os.ReadFile(f.idsFile)
		if err != nil {
			return q, "--ids-file: " + err.Error()
		}
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				q.IDs = append(q.IDs, l)
			}
		}
		if len(q.IDs) == 0 {
			return q, "--ids-file " + f.idsFile + " names no card"
		}
	}
	if !q.Given() {
		return q, "a selector call wants --stream, --who, --state or --ids-file"
	}
	return q, q.Check()
}

// selVerb is the batch path of one verb: its flags (the selector's, and the verb's own,
// registered by extra), no ids, and the step build makes; --dry-run plans the step on one
// read and writes nothing.
func (a *app) selVerb(verb string, args []string, stdout, stderr io.Writer, extra func(fs flagSet) func() string,
	build func(q sprint.Selector, c *common, st *store.Store) (store.Step, string)) int {
	fs, c := a.verbSetup(verb)
	var f selFlags
	f.register(fs)
	check := func() string { return "" }
	if extra != nil {
		if ch := extra(fs); ch != nil {
			check = ch
		}
	}
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(ids) > 0 {
		return refuse(stderr, verb, "a selector names the cards: give no id with --stream, --who, --state or --ids-file (put ids in an --ids-file)")
	}
	q, why := f.selector()
	if why == "" {
		why = check()
	}
	if why != "" {
		return refuse(stderr, verb, why)
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if err := unaliasFlag(context.Background(), st, fs, "answers"); err != nil {
		return refuse(stderr, verb, "--answers: "+err.Error())
	}
	step, why := build(q, c, st)
	if why != "" {
		return refuse(stderr, verb, why)
	}
	if f.dry {
		return a.selDry(verb, *c, st, step, stdout, stderr)
	}
	return a.runStep(verb, *c, st, step, stdout, stderr)
}

// selDry is --dry-run: the step planned on one read of what it loads, its lines printed,
// nothing written. The plan is made without the model tiers' routes a deal reads, so a
// rework's tier is the step's own to choose when it runs.
func (a *app) selDry(verb string, c common, st *store.Store, step store.Step, stdout, stderr io.Writer) int {
	snap, err := st.Load(context.Background(), step.Load, step.Extras)
	if err != nil {
		return a.readFailed(verb, err, stderr)
	}
	p := step.Plan(snap)
	var lines []string
	for _, u := range p.Units {
		if u.Moved != "" {
			lines = append(lines, "WOULD "+u.Moved)
		}
	}
	for _, s := range p.Said {
		lines = append(lines, "NOTE "+s)
	}
	for _, r := range p.Refused {
		lines = append(lines, "REFUSED "+r.Key+": "+r.Why)
	}
	code, status := 0, "ok"
	if len(p.Refused) > 0 {
		code, status = 1, "refused"
	}
	selected := 0
	for _, s := range p.Said {
		if strings.HasSuffix(s, ": selected") {
			selected++
		}
	}
	for i := range lines {
		lines[i] = oneline.Escape(lines[i])
	}
	sayOK(stdout, c.json, verb, fmt.Sprintf("%s %s DRY-RUN selected=%d changes=%d refused=%d\n%s", token(verb), strings.ToUpper(status), selected, len(p.Units), len(p.Refused), strings.Join(lines, "\n")),
		map[string]any{"status": status, "exit": code, "dry_run": true, "selected": selected, "changes": len(p.Units), "lines": lines, "refused": p.Refused})
	return code
}

// selectStep is a step over named ids made a step over the selection: mk builds the verb's
// own step for the ids selected on the state the step reads (sprint.SelectIDs), its
// records read for them (Extras); it names its cards, so one refusal refuses the whole.
func selectStep(verb string, q sprint.Selector, args any, mk func(ids []string) store.Step) store.Step {
	b := mk(nil)
	b.Named = true
	b.Args = store.ArgsOf(struct {
		Verb string
		Sel  sprint.Selector
		Args any
	}{verb, q, args})
	if !slices.Contains(b.Load, sprint.Work) {
		b.Load = append(slices.Clone(b.Load), sprint.Work)
	}
	b.Extras = func(s *sprint.Snapshot) map[string][]string {
		ids, _ := sprint.Selected(s, q)
		if e := mk(ids).Extras; e != nil {
			return e(s)
		}
		return nil
	}
	b.Plan = func(s *sprint.Snapshot) sprint.Plan {
		return sprint.SelectIDs(s, verb, q, func(ids []string) sprint.Plan { return mk(ids).Plan(s) })
	}
	return b
}

// briefEditFlags is the brief transform's flags: brief and recut by selector take a
// transform instead of a whole file.
func briefEditFlags(fs flagSet, e *sprint.BriefEdit) {
	fs.StringVar(&e.SetBase, "set-base", "", "each selected card's brief with its BASE: line naming this branch (one put under line 1 when it has none)")
	fs.BoolVar(&e.DropWho, "drop-who", false, "each selected card's brief with its WHO: line taken out: it names no friend, and is dealt to whoever its tier finds")
}

func checkTier(tier string) string {
	if tier != "" && !cardhdr.IsRoute(tier) {
		return "--tier wants " + cardhdr.RouteList + ", found " + oneline.Escape(tier)
	}
	return ""
}

// cmdRecutSel is recut's front door: recut by selector (sprint.RecutSel), every selected
// card re-cut as its twin in one step, its tier --tier and its brief its own with the
// transform made, the dependants following the twins.
func (a *app) cmdRecutSel(args []string, stdout, stderr io.Writer) int {
	if !selCall("recut", args) {
		return a.cmdRecut(args, stdout, stderr)
	}
	var edit sprint.BriefEdit
	var tier *string
	return a.selVerb("recut", args, stdout, stderr, func(fs flagSet) func() string {
		briefEditFlags(fs, &edit)
		tier = fs.String("tier", "", "the tier every twin is pinned to ("+cardhdr.RouteList+"); default: each old card's pin")
		return func() string {
			if *tier == "" && !edit.Given() {
				return "recut by selector wants --tier <" + cardhdr.RouteList + ">, --set-base <branch> or --drop-who: what every twin changes"
			}
			return checkTier(*tier)
		}
	}, func(q sprint.Selector, c *common, _ *store.Store) (store.Step, string) {
		r := sprint.RecutSelReq{Sel: q, Tier: *tier, Edit: edit, Who: c.actor}
		return store.Step{Named: true, Args: store.ArgsOf(r), Verb: "recut", Load: store.All, Mirrors: true,
			Extras: func(s *sprint.Snapshot) map[string][]string {
				ids, _ := sprint.Selected(s, q)
				out := map[string][]string{}
				for _, id := range ids {
					for t, xs := range store.RecutStep(sprint.RecutReq{ID: id}).Extras(s) {
						out[t] = append(out[t], xs...)
					}
				}
				return out
			},
			Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.RecutSel(s, r) }}, ""
	})
}

// cmdBriefSel is brief's front door: brief by selector (sprint.BriefSel), every selected
// card's own brief with the transform made, and re-tiered with --tier, in one step.
func (a *app) cmdBriefSel(args []string, stdout, stderr io.Writer) int {
	if !selCall("brief", args) {
		return a.cmdBrief(args, stdout, stderr)
	}
	var edit sprint.BriefEdit
	var tier *string
	return a.selVerb("brief", args, stdout, stderr, func(fs flagSet) func() string {
		briefEditFlags(fs, &edit)
		tier = fs.String("tier", "", "re-tier every selected card ("+cardhdr.RouteList+"), as brief <id> --tier does")
		return func() string {
			if *tier == "" && !edit.Given() {
				return "brief by selector takes a transform instead of a whole file: --set-base <branch>, --drop-who or --tier <" + cardhdr.RouteList + ">"
			}
			return checkTier(*tier)
		}
	}, func(q sprint.Selector, c *common, _ *store.Store) (store.Step, string) {
		r := sprint.BriefSelReq{Sel: q, Tier: *tier, Edit: edit, Who: c.actor}
		return store.Step{Named: true, Args: store.ArgsOf(r), Verb: "brief", Load: []string{sprint.Work},
			Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.BriefSel(s, r) }}, ""
	})
}

// cmdReworkSel is rework's front door: rework by selector, every selected card sent back in
// one step (sprint.Rework).
func (a *app) cmdReworkSel(args []string, stdout, stderr io.Writer) int {
	if !selCall("rework", args) {
		return a.cmdRework(args, stdout, stderr)
	}
	var fix, ans, tier *string
	return a.selVerb("rework", args, stdout, stderr, func(fs flagSet) func() string {
		fix = fs.String("fix", "", "the fix for every selected card; without it each takes its own finding or report")
		ans = fs.String("answers", "", answersWords)
		tier = fs.String("tier", "", "the tier ("+cardhdr.RouteList+") every selected card is pinned to, as rework --tier")
		return func() string { return checkTier(*tier) }
	}, func(q sprint.Selector, c *common, _ *store.Store) (store.Step, string) {
		r := sprint.ReworkReq{Fix: *fix, Answers: answers(*ans), Tier: *tier, Who: c.actor}
		return selectStep("rework", q, r, func(ids []string) store.Step {
			r := r
			r.Sel = sprint.Sel{IDs: ids}
			return store.ReworkStep(r)
		}), ""
	})
}

// cmdReturnSel is return's front door: return by selector (sprint.Return), one step.
func (a *app) cmdReturnSel(args []string, stdout, stderr io.Writer) int {
	if !selCall("return", args) {
		return a.cmdReturn(args, stdout, stderr)
	}
	var reason, ans *string
	return a.selVerb("return", args, stdout, stderr, func(fs flagSet) func() string {
		reason = fs.String("reason", "", "why every selected card goes back to review")
		ans = fs.String("answers", "", answersWords)
		return nil
	}, func(q sprint.Selector, c *common, _ *store.Store) (store.Step, string) {
		r := sprint.ReturnReq{Reason: *reason, Answers: answers(*ans), Who: c.actor}
		return selectStep("return", q, r, func(ids []string) store.Step {
			r := r
			r.Sel = sprint.Sel{IDs: ids}
			return store.ReturnStep(r)
		}), ""
	})
}

// cmdDropSel is drop's front door: drop by selector (sprint.Drop), one step.
func (a *app) cmdDropSel(args []string, stdout, stderr io.Writer) int {
	if !selCall("drop", args) {
		return a.cmdDrop(args, stdout, stderr)
	}
	var reason, ans *string
	return a.selVerb("drop", args, stdout, stderr, func(fs flagSet) func() string {
		reason = fs.String("reason", "", "why every selected card leaves the table; kept with its record")
		ans = fs.String("answers", "", answersWords)
		return func() string {
			if strings.TrimSpace(*reason) == "" {
				return "drop by selector wants --reason <text>"
			}
			return ""
		}
	}, func(q sprint.Selector, c *common, st *store.Store) (store.Step, string) {
		r := sprint.DropReq{Reason: *reason, Answers: answers(*ans), Who: c.actor}
		c.after = func(ctx context.Context, st *store.Store, res store.Result) []string {
			return a.droppedBriefs(ctx, st, res, *reason)
		}
		return selectStep("drop", q, r, func(ids []string) store.Step {
			r := r
			r.Sel = sprint.Sel{IDs: ids}
			return store.DropStep(r)
		}), ""
	})
}

// cmdReleaseSel is release's front door: release by selector (sprint.Release), the
// selected sentinels and held cards released in one step.
func (a *app) cmdReleaseSel(args []string, stdout, stderr io.Writer) int {
	if !selCall("release", args) {
		return a.cmdRelease(args, stdout, stderr)
	}
	var reason, ans *string
	return a.selVerb("release", args, stdout, stderr, func(fs flagSet) func() string {
		reason = fs.String("reason", "", "what you looked at and found: recorded on every sentinel or held card released")
		ans = fs.String("answers", "", answersWords)
		return func() string {
			if strings.TrimSpace(*reason) == "" {
				return "release by selector wants --reason <text>"
			}
			return ""
		}
	}, func(q sprint.Selector, c *common, st *store.Store) (store.Step, string) {
		coordinator, err := st.B.Coordinator(context.Background())
		if err != nil {
			return store.Step{}, "the coordinator did not read: " + err.Error()
		}
		q.Sentinels = true // a stream's stops are released with its held cards
		r := sprint.ReleaseReq{Reason: *reason, Coordinator: coordinator, Answers: answers(*ans), Who: c.actor}
		return selectStep("release", q, r, func(ids []string) store.Step {
			r := r
			r.IDs = ids
			return store.ReleaseStep(r)
		}), ""
	})
}

// cmdRankSel is rank's front door: rank by selector (sprint.Rank), the selected cards
// ranked in one step, in work order.
func (a *app) cmdRankSel(args []string, stdout, stderr io.Writer) int {
	if !selCall("rank", args) {
		return a.cmdRank(args, stdout, stderr)
	}
	var score, before, ans *string
	var first *bool
	return a.selVerb("rank", args, stdout, stderr, func(fs flagSet) func() string {
		score = fs.String("score", "", "the new score of the first selected card; the rest follow it")
		first = fs.Bool("first", false, "the selected cards ahead of every primary")
		before = fs.String("before", "", "the selected cards in line in front of this primary of their own stream")
		ans = fs.String("answers", "", answersWords)
		return func() string {
			given := 0
			for _, on := range []bool{*score != "", *first, *before != ""} {
				if on {
					given++
				}
			}
			if given != 1 {
				return "rank by selector wants one of --score <n>, --first, --before <id>"
			}
			return ""
		}
	}, func(q sprint.Selector, c *common, _ *store.Store) (store.Step, string) {
		r := sprint.RankReq{First: *first, Before: *before, Answers: answers(*ans), Who: c.actor}
		if *score != "" {
			f, err := strconv.ParseFloat(*score, 64)
			if err != nil {
				return store.Step{}, "--score wants a number"
			}
			r.Score = &f
		}
		return selectStep("rank", q, r, func(ids []string) store.Step {
			r := r
			r.IDs = ids
			return store.RankStep(r)
		}), ""
	})
}
