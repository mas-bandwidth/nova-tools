package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// devRedDecisions are the judgment a red run on the base raises after a
// promotion merged: the fix cards are cut, so fix is the default.
var devRedDecisions = []string{"fix", "skip"}

// devRed is the base's red runs at the last promotion's merge
// (refs/promoted/last): each failed run not judged before cuts one fix card per
// failing test and raises one judgment naming the cards. A pass with no
// promotion recorded, or no new red run, raises none; a run list that cannot be
// read is a NOTE on stderr and the pass goes on.
func (p *promoter) devRed(ctx context.Context, stdout, stderr io.Writer) *promoteJudgment {
	sha, err := p.rev(ctx, "refs/promoted/last")
	if err != nil || sha == "" {
		return nil
	}
	runs, err := p.forger().FailedRuns(ctx, p.base, sha)
	if err != nil {
		fmt.Fprintf(stderr, "NOTE promote: the runs of %s at %s cannot be read: %s; run: gh run list --branch %s --commit %s\n", oneline.Field(p.base), sha, oneline.Err(err), oneline.Field(p.base), sha)
		return nil
	}
	if p.redRuns == nil {
		p.redRuns = map[int]bool{}
	}
	var fresh []promoteRun
	for _, r := range runs {
		if !p.redRuns[r.ID] {
			p.redRuns[r.ID] = true
			fresh = append(fresh, r)
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	j := p.redJudgment(ctx, fresh, p.base+" was red after the promotion "+sha, p.base+" at "+sha, devRedDecisions, stderr)
	fmt.Fprintf(stdout, "JUDGMENT dev red base=%s sha=%s decisions=%s cards=%s open=%s\n%s\n", oneline.Field(p.base), sha, strings.Join(j.Decisions, ","),
		oneline.Field(dashed(strings.Join(j.Cards, ","))), oneline.Field(dashed(strings.Join(j.Open, ","))), j.Tail)
	return j
}

// redJudgment reads each red run's failed log, cuts one fix card per distinct
// failing test (p.red), and is the one judgment naming the cards: what, the
// logs' tail, the decisions, the cards cut and the tests an open card already
// names. A cut that fails is a NOTE on stderr and the judgment names no card.
func (p *promoter) redJudgment(ctx context.Context, runs []promoteRun, what, where string, decisions []string, stderr io.Writer) *promoteJudgment {
	var logs []string
	var reds []sprint.RedTest
	for _, r := range runs {
		run := strconv.Itoa(r.ID)
		text, err := p.forger().RunLog(ctx, r.ID)
		if err != nil {
			text = "the failing run's log could not be read: " + oneline.Err(err)
		}
		logs = append(logs, text)
		for _, t := range sprint.RedTests(text) {
			if !slices.ContainsFunc(reds, func(x sprint.RedTest) bool { return x.Test == t.Test }) {
				t.Run = run
				reds = append(reds, t)
			}
		}
	}
	j := &promoteJudgment{What: what, Tail: logTail(strings.Join(logs, "\n")), Decisions: append([]string(nil), decisions...)}
	if p.red == nil || len(reds) == 0 {
		return j
	}
	cut, open, err := p.red(ctx, reds, p.redSpec(ctx, where))
	if err != nil {
		fmt.Fprintf(stderr, "NOTE promote: no fix card was cut: %s; run: nova-sprint promote --dry-run\n", oneline.Err(err))
		return j
	}
	j.Cards, j.Open = cut, open
	if len(cut) > 0 {
		j.What += "; fix cards cut: " + strings.Join(cut, ", ")
	}
	return j
}

// redSpec is what the fix cards of a red run share: the repository the forge names
// for the clone, the live sprint branch they start from, the module of its
// go.mod, and the day's promote-red stream.
func (p *promoter) redSpec(ctx context.Context, where string) sprint.FixSpec {
	// ignored: a repository the forge cannot name leaves the fix cards' REPO empty, and the module is read from go.mod
	repo, _ := p.forger().Repo(ctx)
	module := ""
	if mod, err := p.git(ctx, "show", "--end-of-options", p.live+":go.mod"); err == nil {
		for line := range strings.SplitSeq(mod, "\n") {
			if m, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
				module = strings.Trim(strings.TrimSpace(m), `"`)
				break
			}
		}
	}
	if module == "" && repo != "" {
		module = "github.com/" + repo
	}
	return sprint.FixSpec{Repo: repo, Base: p.live, Module: module, Stream: sprint.RedStreamPrefix + p.now.Format("2006-01-02"), Where: where}
}

// redCutter is the promote step's cut (docs/SPEC-SPRINT.md section 11,
// promote): one heavy card per failing test into the promote-red stream, its
// brief held to the card lint as add holds one, ranked first and deduplicated
// against the open cards by test name inside the one add step
// (sprint.FixCards), recorded as the machine's.
func (a *app) redCutter(c common) func(context.Context, []sprint.RedTest, sprint.FixSpec) ([]string, []string, error) {
	return func(ctx context.Context, reds []sprint.RedTest, sp sprint.FixSpec) ([]string, []string, error) {
		var st *store.Store
		var why strings.Builder
		rs, code := a.briefRules("promote", "", &c, &st, &why)
		if code != 0 {
			return nil, nil, errors.New(strings.TrimSpace(why.String()))
		}
		if st == nil {
			var err error
			if st, err = a.store(c); err != nil {
				return nil, nil, err
			}
		}
		req := sprint.AddReq{Stream: sp.Stream, Who: sprint.MachineActor}
		for _, r := range reds {
			brief := sprint.FixBrief(r, sp)
			if cs := cardRules(brief, rs); cs.held == "" && len(cs.rules) > 0 {
				// the members hold no rules file for the repository: the card carries its own
				brief += "\n\n" + strings.TrimSuffix(swarm.RulesParagraph(cs.rules), "\n")
			}
			id := sprint.FixCardID(r.Test)
			req.Cards = append(req.Cards, sprint.CardAdd{ID: id, File: id, Brief: brief, Rules: cardRules(brief, rs).held, Base: sp.Base, Repo: swarm.ReadCardBase([]byte(brief)).Named})
		}
		var lint strings.Builder
		if lintBriefFiles("promote", req.Cards, rs, 0, &lint) != 0 {
			return nil, nil, errors.New(strings.TrimSpace(lint.String()))
		}
		var cut, open []string
		step := store.AddStep(req)
		step.Actor = sprint.MachineActor
		step.Plan = func(s *sprint.Snapshot) sprint.Plan {
			add, dup := sprint.FixCards(s, req)
			cut, open = nil, dup
			for _, card := range add.Cards {
				cut = append(cut, card.ID)
			}
			if len(add.Cards) == 0 {
				return sprint.Plan{}
			}
			return sprint.Add(s, add)
		}
		res, err := st.Run(ctx, step)
		if err != nil {
			return nil, nil, err
		}
		if len(res.Refused) > 0 {
			return nil, nil, fmt.Errorf("the add of %s refused %s: %s", strings.Join(cut, ","), res.Refused[0].Key, res.Refused[0].Why)
		}
		return cut, open, nil
	}
}
