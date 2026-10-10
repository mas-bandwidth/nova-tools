package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// THE LANDER'S GATE VERDICT (docs/SPEC-SPRINT.md section 7; SPEC-NOVA-DECIDE section 12). A
// batch whose --check is red on go test failures has each failure classified by nova-decide's
// gate decision at the sprint row's bars (decide_gate_flaky, decide_gate_preexisting) over its
// lines, the batch's PATHS and its diff from the base; the base is not run. Every decision is
// recorded in <land root>/decide/gate.jsonl under land/<stream>@<tip 12>@gate/<pkg>.<Test> and
// shown in the red batch's reason. When the flaky bar is set, no failure is caused and one is
// flaky at or above it, the check is run once more: green, the batch lands; red, it is red as
// before; the rerun's result is attached to each flaky decision as its outcome. The bar is
// empty by default, and then nothing is rerun. No key, bars it cannot read, or a backend that
// fails: the red batch is red as before, with why the decision was not made.

// landGate is what a red batch gate is decided with: the backend and its clock, the bars, and
// the record.
type landGate struct {
	backend decide.Backend
	now     func() time.Time
	bars    decide.GateBars
	record  string
}

// landGate is land's gate decision, read once a run: nil with why when one cannot be made.
func (a *app) landGate(ctx context.Context, st *store.Store) (*landGate, string) {
	raw, err := st.GateBars(ctx)
	if err != nil {
		return nil, "the sprint row's gate bars could not be read: " + oneline.Err(err)
	}
	bars, err := decide.ParseGateBars(raw[0], raw[1])
	if err != nil {
		return nil, err.Error()
	}
	g := &landGate{bars: bars, now: time.Now}
	if a.gateBackend != nil {
		g.backend, g.now = a.gateBackend()
	} else if key := a.getenv(decide.JevSecret); key != "" {
		g.backend = decide.JevHTTP(key, decide.JevTimeout)
	} else {
		return nil, decide.JevSecret + " is absent from land's environment"
	}
	root, err := a.landRoot()
	if err != nil {
		return nil, "no directory for the gate record: " + oneline.Err(err)
	}
	g.record = filepath.Join(root, "decide", "gate.jsonl")
	if err := os.MkdirAll(filepath.Dir(g.record), 0o755); err != nil {
		return nil, "the gate record's directory: " + oneline.Err(err)
	}
	return g, ""
}

// gateRerun is a red batch check's gate decision (why and out are the check's): "" when every
// failure that is not pre-existing was flaky and the check run once more passed; else why,
// with what the decision said, or why none was made.
func (l *lander) gateRerun(ctx context.Context, dir, stream, base, tip string, cards []landCard, why, out string) string {
	failures := decide.ParseGateOutput(out)
	switch {
	case len(failures) == 0:
		return why
	case l.gate == nil:
		return why + " (no gate decision: " + l.gateNote + ")"
	}
	in := decide.GateInput{Failures: failures}
	for _, c := range cards {
		for _, p := range c.paths {
			if !slices.Contains(in.Paths, p) {
				in.Paths = append(in.Paths, p)
			}
		}
	}
	if diff, err := l.git(ctx, dir, "diff", "-M", "--no-color", "--end-of-options", "refs/remotes/origin/"+base, tip); err == nil {
		in.Diff = decide.DiffSummary(diff)
	}
	op := "land/" + stream + "@" + tip[:min(12, len(tip))] + "@gate"
	dctx, cancel := context.WithTimeout(ctx, time.Minute)
	res, err := decide.Gate(dctx, l.gate.backend, l.gate.bars, in, l.gate.record, op, l.gate.now())
	cancel()
	switch {
	case err != nil:
		return why + " (no gate decision: " + oneline.Err(err) + ")"
	case res.Route != decide.Flaky:
		return why + " (the gate decision, " + op + ": " + res.Classes() + "; " + gateRouted(l.gate.bars, res.Route) + ")"
	}
	again, out2 := l.runCheck(ctx, dir)
	red := map[string]bool{}
	if again != "" {
		got := decide.ParseGateOutput(out2)
		for _, f := range res.Rerun() {
			red[f.Key()] = len(got) == 0 || slices.ContainsFunc(got, func(g decide.Failure) bool { return g.Pkg == f.Pkg && (g.Test == f.Test || g.Test == "") })
		}
	}
	if _, err := decide.SettleGate(l.gate.record, res, red, nil, l.gate.now()); err != nil {
		again += " (the rerun's result was not attached to its decisions: " + oneline.Err(err) + ")"
	}
	if again == "" {
		return ""
	}
	return again + " (run once more: the gate decision classed " + decide.Names(res.Rerun()) + " flaky, op " + op + ")"
}

// gateRouted says what a red batch's gate route did: nothing, when the flaky bar is unset.
func gateRouted(bars decide.GateBars, route string) string {
	if !decide.Set(bars.Flaky) {
		return "recorded; the flaky bar is unset, so nothing is rerun"
	}
	return route + ", not rerun"
}
