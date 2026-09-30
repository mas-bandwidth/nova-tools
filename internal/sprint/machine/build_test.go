package machine

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/stepbuild"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// dealWorld is a world and a whole snapshot that agree: a fleet member m1 up,
// and primaries of s1 in ready, never dealt; and the real deal rule (R6).
func dealWorld(t *testing.T, primaries ...string) (*world, *sprint.Snapshot, sprint.Rule) {
	t.Helper()
	w := newWorld(t)
	w.rows("s1")
	w.verb(tset.Entry{Kind: "rows", Table: sprint.Fleet, Add: []string{"m1"}})
	w.verb(tset.Entry{Kind: "create", Table: sprint.Fleet, To: "m1:ctl", IDs: []string{sprint.CtlID("m1")}, Scores: []string{"0"},
		Set: map[string]string{"status": sprint.Up}, About: []string{"m1"}})
	if len(primaries) != 0 {
		w.verb(create("s1:ready", fresh(), primaries...))
	}
	s := &sprint.Snapshot{Now: w.clk.now(), Work: sprint.NewTable(sprint.Work), Readers: sprint.NewTable(sprint.Readers),
		Merge: sprint.NewTable(sprint.Merge), Fleet: sprint.NewTable(sprint.Fleet)}
	s.Work.SetRows([]string{"s1"})
	s.Fleet.SetRows([]string{"m1"})
	s.Fleet.Put(&sprint.Card{ID: sprint.CtlID("m1"), Row: "m1", Col: "ctl", Rev: 1, Fields: map[string]string{"status": sprint.Up}})
	for i, id := range primaries {
		s.Work.Put(&sprint.Card{ID: id, Row: "s1", Col: "ready", Score: float64(i + 1), Rev: 1, Fields: fresh()})
	}
	for _, r := range sprint.RuleTable() {
		if r.Name == "deal" {
			return w, s, r
		}
	}
	t.Fatal("no deal rule in the rule table")
	return nil, nil, sprint.Rule{}
}

// TestStepBuilderBuildsARealRule: the adapter from the loop's Builder to
// stepbuild.Build turns the real deal rule's plan (R6: each primary's work
// card created in a member's ready queue and the primary moved to working,
// one unit each, with its guards) into one body that finishes its key: the
// count guard is Layer 1's count entry over the member's ready cell, R6's
// guard on sent:s (rcount over a sorted set of the sprint's own) is X's sent
// guard, and the body applies on the twin (1.3.6).
func TestStepBuilderBuildsARealRule(t *testing.T) {
	t.Parallel()
	w, s, deal := dealWorld(t, "p1", "p2")
	now := sprint.Now{R: 1000, Wall: s.Now.UnixMilli(), Running: true}
	rp := deal.Plan(s, []sprint.AgendaKey{{Key: "deal", Seq: 1}}, now)
	if len(rp.Plan.Units) != 2 {
		t.Fatalf("the deal planned %d units", len(rp.Plan.Units))
	}
	l := w.loop("a", nil, Budget{})
	k := &counting{c: w.tw}
	w.tick(l, k) // the lease, for the step's generation
	meta := sprintfn.Meta{Rule: "deal", Tick: true, Gen: l.gen}
	bodies, err := StepBuilder(testNames.Prefix)(rp, meta, stepbuild.Contract())
	if err != nil || len(bodies) != 1 || strings.Join(bodies[0].Done, ",") != "deal" {
		t.Fatalf("built %d bodies (%v): %+v", len(bodies), err, bodies)
	}
	body := bodies[0]
	if c := body.Entries[0]; c.Kind != "count" || c.Table != sprint.Fleet || strings.Join(c.Cells, ",") != "m1:ready" || len(c.CountMax) != 1 {
		t.Fatalf("the count guard's entry: %+v", c)
	}
	sent := 0
	for _, g := range body.Guards {
		if g.Kind == guardCount || g.Kind == guardRCount {
			t.Fatalf("a guard of kind %s went to X: %+v", g.Kind, g)
		}
		if g.Kind == sprintfn.XGuardSent {
			sent++
			if g.Key != "sent:s1 2" {
				t.Fatalf("R6's sent guard: %+v", g)
			}
		}
	}
	if sent != 1 {
		t.Fatalf("%d sent guards: %+v", sent, body.Guards)
	}
	w.step(&sprintfn.Request{Epoch: "0", Meta: meta, Body: body})
	for _, p := range []string{"p1", "p2"} {
		if got, card := w.place(p), w.placeIn(sprint.Fleet, p+".w1"); got != "s1:working" || card != "m1:ready" {
			t.Fatalf("%s is at %q and its work card at %q", p, got, card)
		}
	}
}

// TestStepBuilderKeepsUnitsWhole: a plan cut into several bodies cuts between
// units, never inside one, and carries its Done on none of them; a unit that
// no body holds alone refuses the plan (1.3.6, 1.3.5).
func TestStepBuilderKeepsUnitsWhole(t *testing.T) {
	t.Parallel()
	_, s, deal := dealWorld(t, "p1", "p2", "p3")
	rp := deal.Plan(s, []sprint.AgendaKey{{Key: "deal", Seq: 1}}, sprint.Now{R: 1000, Wall: s.Now.UnixMilli(), Running: true})
	b := stepbuild.Contract()
	b.Candidates = 3 // a unit is a create and a move: one unit a body
	bodies, err := StepBuilder(testNames.Prefix)(rp, sprintfn.Meta{Rule: "deal", Tick: true, Gen: 1}, b)
	if err != nil || len(rp.Plan.Units) < 2 || len(bodies) != len(rp.Plan.Units) {
		t.Fatalf("built %d bodies: %v", len(bodies), err)
	}
	for i, body := range bodies {
		ids := map[string]bool{}
		for _, e := range body.Entries {
			for _, id := range e.IDs {
				ids[id] = true
			}
		}
		p := "p" + string(rune('1'+i))
		if len(body.Done) != 0 || len(ids) != 2 || !ids[p] || !ids[p+".w1"] || (i == 0) != (len(body.Guards) != 0) {
			t.Fatalf("body %d: %+v", i, body)
		}
	}
	b.Candidates = 1
	if _, err := StepBuilder(testNames.Prefix)(rp, sprintfn.Meta{Rule: "deal", Tick: true, Gen: 1}, b); err == nil || !strings.Contains(err.Error(), "units whole") {
		t.Fatalf("a unit over the candidates alone: %v", err)
	}
}

// TestStepBuilderRefusesWhatItDoesNotCarry: a plan with writes or guards the
// adapter has no wire for is refused naming them, so the loop parks its keys and names the
// gap (1.3.5), and never sends a step that drops them.
func TestStepBuilderRefusesWhatItDoesNotCarry(t *testing.T) {
	t.Parallel()
	build := StepBuilder(testNames.Prefix)
	for name, c := range map[string]struct {
		rp   sprint.RulePlan
		want string
	}{
		"a revs fold": {sprint.RulePlan{Guards: []sprint.XGuard{{Kind: guardRevs, Key: sprint.Work, Score: 7}}}, "the fold of the revisions of the work cards read"},
		"a zguard X has no kind for": {sprint.RulePlan{Guards: []sprint.XGuard{sprint.SetGuard{Kind: sprint.GuardZGuard, Key: "sent:s1", Min: "-inf", Max: "5"}.XGuard()}},
			"a set guard of kind zguard"},
		"bumps": {sprint.RulePlan{Plan: sprint.Plan{Units: []sprint.Unit{{Key: "p1", Bumps: []sprint.Bump{{Table: sprint.Work, ID: "p1", Field: "n", Delta: 1}}}}}}, "unit p1: bumps, notes or closes of a unit"},
	} {
		_, err := build(c.rp, sprintfn.Meta{Rule: "x", Tick: true, Gen: 1}, stepbuild.Contract())
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: refused with %v, want the reason %q", name, err, c.want)
		}
	}
}

// TestNewLoopBuildsWithStepBuilder: a loop given no builder builds with
// StepBuilder, so Run serves the real rules.
func TestNewLoopBuildsWithStepBuilder(t *testing.T) {
	t.Parallel()
	_, s, deal := dealWorld(t, "p1")
	l, err := NewLoop(Config{Names: testNames, Owner: "token-a", Name: "a", Rules: []sprint.Rule{deal}})
	if err != nil || l.cfg.Build == nil {
		t.Fatalf("%v", err)
	}
	rp := deal.Plan(s, []sprint.AgendaKey{{Key: "deal", Seq: 1}}, sprint.Now{R: 1000, Wall: s.Now.UnixMilli(), Running: true})
	if bodies, err := l.cfg.Build(rp, sprintfn.Meta{Rule: "deal", Tick: true, Gen: 1}, stepbuild.Contract()); err != nil || len(bodies) != 1 {
		t.Fatalf("the default builder: %d bodies, %v", len(bodies), err)
	}
}
