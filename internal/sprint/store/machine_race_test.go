package store

// Cold reader: a coordinator verb injected at every store call of a tick.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// injector runs fn once, before the k-th Acquire/Apply/Release/ReadFence the
// tick makes.
type injector struct {
	*Mem
	k, n  int
	fn    func()
	fired bool
	armed bool
}

func (x *injector) hit() {
	if !x.armed || x.fired {
		return
	}
	x.n++
	if x.n == x.k {
		x.fired = true
		x.fn()
	}
}

func (x *injector) ReadFence(ctx context.Context) (Fence, error) {
	x.hit()
	return x.Mem.ReadFence(ctx)
}
func (x *injector) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	x.hit()
	return x.Mem.Acquire(ctx, gen, op)
}
func (x *injector) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	x.hit()
	return x.Mem.Apply(ctx, m)
}
func (x *injector) Release(ctx context.Context, op OpRecord, commit bool) error {
	x.hit()
	return x.Mem.Release(ctx, op, commit)
}

// raceScene: s1 four ready (the deal), s2 "rv" in review unasked (the ask) and
// "acc" in review with two oks, s2 "b" landed; s3 "a" stuck needing b (the
// resume), s3 "w" waiting on b (the resolve).
func raceScene(t *testing.T) *harness {
	h := newHarness(t)
	for _, m := range []string{"m1", "m2"} {
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: m}))
	}
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"b"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"a"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"acc", "rv"}}))
	h.through("a", "b", "acc")
	h.must(MergeStep(sprint.MergeReq{Stream: "s3", Cross: "a=b"}))
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"w"}, Needs: []string{"b"}}))
	// acc back to review with two oks (return), rv to review unasked
	h.must(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"acc"}}, Reason: "hold"}))
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"rv"}}}))
	s := h.snap()
	c := s.Fleet.Card(s.Work.Card("rv").F("work"))
	h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
	h.must(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
	h.must(MergeStep(sprint.MergeReq{Stream: "s2", Batch: 1}))
	if h.state("b") != sprint.Landed {
		t.Fatalf("b %s", h.state("b"))
	}
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 4}))
	h.clean("scene")
	return h
}

func TestCRTickRacesEveryVerbAtEveryCall(t *testing.T) {
	t.Parallel()
	one := 1.0
	verbs := map[string]func() Step{
		"accept": func() Step { return AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"acc"}}}) },
		"rework": func() Step { return ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"rv"}}, Fix: "f"}) },
		"drop-ready": func() Step {
			return DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}, Reason: "r"})
		},
		"drop-review": func() Step { return DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"rv"}}, Reason: "r"}) },
		"drop-wait":   func() Step { return DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"w"}}, Reason: "r"}) },
		"drop-stuck":  func() Step { return DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"a"}}, Reason: "r"}) },
		"rank":        func() Step { return RankStep(sprint.RankReq{IDs: []string{"s1-4", "w"}, Score: &one}) },
		"return":      func() Step { return ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"a"}}, Reason: "r"}) },
		"resume":      func() Step { return ResumeStep(sprint.ResumeReq{Stream: "s3", Did: "d"}) },
		"fleet-down":  func() Step { return FleetStep(sprint.FleetReq{Op: "down", Member: "m1"}) },
		"fleet-up":    func() Step { return FleetStep(sprint.FleetReq{Op: "up", Member: "m3"}) },
	}
	for name, mk := range verbs {
		for k := 1; k <= 200; k++ {
			h := raceScene(t)
			h.startMachine()
			other := &Store{B: h.m, Names: h.st.Names, Actor: "coordinator", Now: h.st.Now, NewID: h.st.NewID, Sleep: func(time.Duration) {}}
			var vres Result
			var verr error
			x := &injector{Mem: h.m, k: k}
			x.fn = func() { vres, verr = other.Run(h.ctx, mk()) }
			h.st.B = x
			x.armed = true
			tres, terr := h.st.Tick(h.ctx)
			x.armed = false
			h.st.B = h.m
			if !x.fired {
				break
			}
			where := fmt.Sprintf("%s at call %d", name, k)
			if terr != nil {
				t.Errorf("%s: the tick failed: %v", where, terr)
			}
			if verr != nil {
				t.Errorf("%s: the verb failed: %v", where, verr)
			}
			if len(vres.Moved) == 0 && len(vres.Refused) == 0 {
				t.Errorf("%s: the verb neither moved nor was refused: %+v", where, vres)
			}
			for _, r := range vres.Refused {
				if r.Why == "" {
					t.Errorf("%s: refused without a reason: %+v", where, r)
				}
			}
			_ = tres
			h.clean(where + " after the race")
			h.machine()
			h.machine()
			h.clean(where + " after two more ticks")
			s := h.snap()
			for _, id := range []string{"s1-1", "s1-2", "s1-3", "s1-4", "rv", "acc", "a", "w", "b"} {
				c := s.Work.Card(id)
				if c == nil {
					_, _, _, f, ok := h.m.Record(h.st.Names.Table(sprint.Work), h.st.Names.MemberPrefix(sprint.Work)+id)
					if !ok {
						_, _, _, f, ok = h.m.Record(h.st.Names.Table(sprint.Work), id)
					}
					if !ok || f["outcome"] != "dropped" {
						t.Errorf("%s: %s lost (record %v %v)", where, id, ok, f)
					}
					continue
				}
				if !c.Placed() && c.F("outcome") != "dropped" {
					t.Errorf("%s: %s off the table without an outcome: %v", where, id, c.Fields)
				}
				live := 0
				for _, fc := range s.Fleet.Of(id) {
					if fc.Col == sprint.Ready || fc.Col == sprint.Working {
						live++
					}
				}
				if live > 1 || (c.Placed() && c.Col == sprint.Working) != (live == 1) {
					t.Errorf("%s: %s in %s with %d live work cards", where, id, c.Col, live)
				}
				if c.Placed() && c.Col == sprint.Review && c.F("result") != "failed" {
					n := 0
					for _, rc := range s.Readers.Of(id) {
						if rc.Int("attempt") == c.Int("attempt") && (rc.Col == sprint.Asked || rc.Col == sprint.Reading) {
							n++
						}
					}
					if n > 2 {
						t.Errorf("%s: %s asked of %d readers", where, id, n)
					}
				}
			}
			if n := h.written(sprint.NResumed); n > 1 {
				t.Errorf("%s: resumed written %d times", where, n)
			}
			notes, _, _ := h.m.NotesSince(h.ctx, "", 100000)
			seen := map[string]int{}
			for _, nn := range notes {
				if nn.Kind == sprint.Judgment {
					for _, p := range nn.Subjects() {
						seen[nn.Type+"|"+p+"|"+nn.What]++
					}
				}
			}
			for k2, v := range seen {
				if v > 1 && !strings.HasPrefix(k2, sprint.NWorkFailed) {
					t.Errorf("%s: judgment written %d times: %s", where, v, k2)
				}
			}
		}
	}
}
