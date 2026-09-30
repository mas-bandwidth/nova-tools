package machine

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
)

// TestTickStoppedLookEveryTen: while STOPPED a tick is RT1 alone, and every
// StoppedLookEvery of the store's wall time the loop looks: RT2 as RUNNING
// (the ingest and each rule's read), the rules plan dry, and moves are due
// when a dry plan changes a card; RT3 only for a step of notes and sprint keys
// (R17's), never one that moves a card (1.4.5, T2; SprintEvents.tla
// PlanOrLook's STOPPED branch).
func TestTickStoppedLookEveryTen(t *testing.T) {
	t.Parallel()
	for _, withR17 := range []bool{false, true} {
		w := newWorld(t)
		w.rows("s1")
		w.step(&sprintfn.Request{Epoch: "0", Meta: sprintfn.Meta{Verb: "init", Actor: "coordinator"}, Clock: &sprintfn.ClockPart{Verb: sprintfn.ClockInit}})
		w.verb(create("s1:ready", fresh(), "p1"))
		var looks int
		cfg := Config{Names: testNames, Owner: "token-a", Name: "a", Rules: []sprint.Rule{dealRule(64)}, Build: testBuild(builderOpts{})}
		if withR17 {
			// A stand-in for IT10's R17: a judgment of the sprint when a dry
			// plan would move a card.
			cfg.Look = func(dry []sprint.RulePlan, c Clock, wall int64) sprint.RulePlan {
				looks++
				if !movesDue(dry) || !c.Stopped {
					return sprint.RulePlan{}
				}
				return sprint.RulePlan{Notes: []sprint.NoteReq{{Op: "open", Type: "the machine is STOPPED and moves are due",
					Cause: "stopped", Subjects: []string{"sprint"}, Text: "moves are due"}}}
			}
		}
		l, err := NewLoop(cfg)
		if err != nil {
			t.Fatal(err)
		}
		k := &counting{c: w.tw}
		if rep := w.tick(l, k); !rep.Looked || rep.Running || rep.RoundTrips != 1 {
			t.Fatalf("the first tick STOPPED looks (with nothing ingested yet): %+v", rep)
		}
		for i := 1; i < 10; i++ {
			w.clk.add(TickEvery)
			if rep := w.tick(l, k); rep.Looked || rep.RoundTrips != 1 {
				t.Fatalf("second %d STOPPED: %+v", i, rep)
			}
		}
		if w.hash("heartbeat")["looked_at"] == "" {
			t.Fatal("the lease step of a STOPPED loop wrote no looked_at")
		}
		w.clk.add(TickEvery)
		rep := w.tick(l, k)
		want := 2
		if withR17 {
			want = 3
		}
		if !rep.Looked || !rep.MovesDue || rep.RoundTrips != want || w.place("p1") != "s1:ready" {
			t.Fatalf("the look at ten seconds (R17 %v): %+v; p1 at %s", withR17, rep, w.place("p1"))
		}
		if withR17 {
			if rep.Applied != 1 || len(w.hash("jopen:sprint@0")) == 0 {
				t.Fatalf("R17's step of notes did not apply: %+v, jopen %v", rep, w.hash("jopen:sprint@0"))
			}
			if looks != 2 {
				t.Fatalf("R17 ran at %d looks, want 2", looks)
			}
		}
		for i := 1; i < 10; i++ {
			w.clk.add(TickEvery)
			if rep := w.tick(l, k); rep.Looked || rep.RoundTrips != 1 {
				t.Fatalf("second %d after the look: %+v", 10+i, rep)
			}
		}
		// Started, the next tick plans what the look ingested, and deals.
		w.step(&sprintfn.Request{Epoch: "0", Meta: sprintfn.Meta{Verb: "start", Actor: "coordinator"}, Clock: &sprintfn.ClockPart{Verb: sprintfn.ClockStart}})
		w.clk.add(TickEvery)
		if rep := w.tick(l, k); !rep.Running || w.place("p1") != "s1:working" {
			t.Fatalf("the first tick RUNNING: %+v; p1 at %s", rep, w.place("p1"))
		}
	}
}
