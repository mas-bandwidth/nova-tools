package main

import (
	"context"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/typedrec"
)

func init() { verbClasses["twin"] = classCoordinator }

// twin <card> [--paths <extra,...>] [--needs <card,...>] [--before <card>] [--tier <t>]
// [--instruction <text>] [--carry]: a card replaced by its twin in one step (sprint.Twin;
// docs/SPEC-SPRINT.md section 2, "A card replaced by its twin"; tla/SprintRules.tla, Replace).
// A merging card is returned first, then twinned. The coordinator's alone.
func (a *app) cmdTwin(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("twin")
	paths := fs.String("paths", "", "globs joined to the brief's PATHS: line, comma separated, each once (a glob that climbs out with .. is refused)")
	needs := fs.String("needs", "", "cards the twin needs besides the card's own needs, comma separated")
	before := fs.String("before", "", "the card of the stream the twin stands in front of (default: the card twinned, its place)")
	tier := fs.String("tier", "", "the tier the twin is pinned to ("+cardhdr.RouteList+"; default: the card's pin)")
	instruction := fs.String("instruction", "", "the correction, written verbatim at the head of the brief's THE TASK")
	carry := fs.Bool("carry", false, "the twin starts from the pushed head of the card's latest attempt: THE TASK names that branch and head, and the brief's CARRY: line holds it; refused, exit 1, when no attempt pushed a full head")
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "twin", err.Error())
	}
	switch {
	case len(ids) != 1:
		return refuse(stderr, "twin", "wants one card: nova-sprint twin <card> [--paths <extra,...>] [--needs <card,...>] [--before <card>] [--tier <t>] [--instruction <text>] [--carry]")
	case *tier != "" && !cardhdr.IsRoute(*tier):
		return refuse(stderr, "twin", "--tier wants "+cardhdr.RouteList+", found "+oneline.Escape(*tier))
	}
	r := sprint.TwinReq{ID: ids[0], Paths: sprint.Split(*paths), Needs: sprint.Split(*needs), Before: *before, Tier: *tier,
		Instruction: *instruction, Who: c.actor}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "twin", err.Error())
	}
	v, err := st.CardOf(context.Background(), ids[0])
	if err != nil {
		return a.readFailed("twin", err, stderr)
	}
	if *carry {
		var last *sprint.Card
		for _, w := range v.Work {
			if last == nil || w.Int("attempt") > last.Int("attempt") {
				last = w
			}
		}
		if last == nil || !typedrec.IsFullSha(last.F("head")) {
			refuse(stderr, "twin", "--carry: no attempt of "+ids[0]+" pushed a full head to start the twin from; twin it without --carry; nothing was changed")
			return 1
		}
		k := member.Carry{Card: ids[0], Attempt: last.Int("attempt"), Head: last.F("head")}
		r.Carry = &sprint.TwinCarry{Attempt: k.Attempt, Branch: last.F("branch"), Head: k.Head, Line: member.CarryLine(k)}
	}
	merging := v.Primary != nil && v.Primary.Placed() && v.Primary.Col == sprint.Merging
	if merging {
		// returned first: the step returns it (and says so), and the next makes the twin
		if code := a.runStep("twin", *c, st, twinStep(r), stdout, stderr); code != 0 || c.dry {
			// --dry-run plans the return and stops: nothing was written, so the twin's own
			// step would plan the same return again
			return code
		}
		c.says = nil
	}
	return a.runStep("twin", *c, st, twinStep(r), stdout, stderr)
}

// twinStep twins a card (sprint.Twin): it reads every table, as the replace does, and the
// records of the card, its needs, the twin's, --before's and every id the twin may take,
// placed or not.
func twinStep(r sprint.TwinReq) store.Step {
	return store.Step{Named: true, Args: store.ArgsOf(r), Verb: "twin", Load: store.All, Mirrors: true,
		Extras: func(s *sprint.Snapshot) map[string][]string {
			ids := append(append([]string{r.ID}, r.Needs...), sprint.TwinNumberIDs(r.ID)...)
			if r.Before != "" {
				ids = append(ids, r.Before)
			}
			out := map[string][]string{sprint.Work: ids}
			if c := s.Work.Placed(r.ID); c != nil {
				out[sprint.Work] = append(out[sprint.Work], sprint.Split(c.F("needs"))...)
				out[sprint.Merge] = []string{sprint.CtlID(c.Row)}
			}
			return out
		},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Twin(s, r) }}
}
