package store

// Cold reader's sprint: 40 primaries, three streams with needs (a chain, a
// diamond, cross-stream needs), 3 members, 3 readers, driven only by the tick,
// scripted outside facts and coordinator decisions from the open judgments.

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

type crWorld struct {
	h       *harness
	rng     *rand.Rand
	running bool
	// silence: from this round, the class says nothing (0 never)
	silentWorkers, silentReaders, silentMerger, silentCoord int
	stopAt, stopFor                                         int
	crossAge                                                map[string]int
	log                                                     []string
	holderFail                                              []string
	readyToAccept                                           map[string]int // primary -> rounds seen
	droppedDone                                             bool
}

var crMembers = []string{"m1", "m2", "m3"}
var crReaders = []string{"reader-a", "reader-b", "reader-c"}

func crSprint(t *testing.T, seed uint64) *crWorld {
	h := newHarness(t)
	w := &crWorld{h: h, rng: rand.New(rand.NewPCG(seed, 7)), crossAge: map[string]int{}, readyToAccept: map[string]int{}}
	for _, m := range crMembers {
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: m}))
	}
	// s1: a chain c1 <- c2 <- ... <- c6, then eight free
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"c1"}}))
	for i := 2; i <= 6; i++ {
		h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{fmt.Sprintf("c%d", i)}, Needs: []string{fmt.Sprintf("c%d", i-1)}}))
	}
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 8}))
	// s2: a diamond d0 <- d1,d2 <- d3, then nine free
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"d0"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"d1", "d2"}, Needs: []string{"d0"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"d3"}, Needs: []string{"d1", "d2"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", Count: 9}))
	// s3: cross needs, a drop victim and its dependant, then free ones
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"x1"}, Needs: []string{"c3", "d3"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"x2"}, Needs: []string{"x1", "s1-7"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"dropme"}, Needs: []string{"c6"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"needsdrop"}, Needs: []string{"dropme"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s3", Count: 9}))
	h.clean("setup")
	return w
}

func (w *crWorld) all() int {
	s := w.h.snap()
	n := 0
	for _, st := range []string{"s1", "s2", "s3"} {
		for _, col := range []string{sprint.Waiting, sprint.Ready, sprint.Working, sprint.Review, sprint.Merging, sprint.Landed} {
			n += s.Work.Count(st, col)
		}
	}
	return n
}

func (w *crWorld) landed() int {
	s := w.h.snap()
	return s.Work.Count("s1", sprint.Landed) + s.Work.Count("s2", sprint.Landed) + s.Work.Count("s3", sprint.Landed)
}

func openBy(open []sprint.Open) map[string][]string {
	out := map[string][]string{}
	for _, o := range open {
		out[o.Subject()] = append(out[o.Subject()], o.Note.Type)
	}
	return out
}

// holders checks every primary on the table not landed is held by someone or
// named by an open judgment. Called right after a tick of a RUNNING machine.
func (w *crWorld) holders(round int) {
	h := w.h
	s := h.snap()
	open, _ := h.m.OpenNotes(h.ctx)
	by := openBy(open)
	up := s.UpMembers()
	room := false
	for _, m := range up {
		if s.Fleet.Count(m, sprint.Ready) < sprint.MaxReadyPerMember {
			room = true
		}
	}
	fail := func(id, why string) {
		w.holderFail = append(w.holderFail, fmt.Sprintf("round %d: %s %s", round, id, why))
	}
	for _, st := range []string{"s1", "s2", "s3"} {
		sj := len(by[sprint.StreamSubject(st)]) > 0
		for _, col := range []string{sprint.Waiting, sprint.Ready, sprint.Working, sprint.Review, sprint.Merging} {
			for _, c := range s.Work.Cell(st, col) {
				pj := len(by[c.ID]) > 0
				switch col {
				case sprint.Waiting:
					all := true
					for _, n := range sprint.Split(c.F("needs")) {
						if s.StateOf(n) != sprint.Landed {
							all = false
						}
					}
					if all && !pj {
						fail(c.ID, "waiting with every need landed, after a tick")
					}
				case sprint.Ready:
					if len(up) == 0 && len(by["stream:"]) == 0 {
						fail(c.ID, "ready, no member up, no judgment")
					}
					if len(up) > 0 && room {
						fail(c.ID, "ready with room in an up member's queue after a tick")
					}
				case sprint.Working:
					wc := s.Fleet.Placed(c.F("work"))
					if wc == nil || (wc.Col != sprint.Ready && wc.Col != sprint.Working) {
						fail(c.ID, "working with no live work card")
					}
				case sprint.Review:
					if c.F("result") == "failed" {
						if !pj {
							fail(c.ID, "review failed, no judgment")
						}
						continue
					}
					out := 0
					for _, rc := range readsAt(s, c) {
						if rc.Col == sprint.Asked || rc.Col == sprint.Reading {
							out++
						}
					}
					if out > 0 || pj {
						continue
					}
					if crOks(s, c) >= 2 {
						w.readyToAccept[c.ID]++
						continue
					}
					fail(c.ID, "review: no read outstanding, not two oks, no judgment")
				case sprint.Merging:
					m := s.Merge.Placed(c.ID)
					if m == nil {
						fail(c.ID, "merging with no merge card")
						continue
					}
					if m.Col == sprint.Stuck && !sj {
						fail(c.ID, "stuck, stream judgment not open")
					}
				}
			}
		}
	}
}

func readsAt(s *sprint.Snapshot, pr *sprint.Card) []*sprint.Card {
	var out []*sprint.Card
	for _, rc := range s.Readers.Of(pr.ID) {
		if rc.Int("attempt") == pr.Int("attempt") {
			out = append(out, rc)
		}
	}
	return out
}

func crOks(s *sprint.Snapshot, pr *sprint.Card) int {
	seen := map[string]bool{}
	for _, rc := range readsAt(s, pr) {
		if rc.Col == sprint.OK && rc.F("head") == pr.F("head") {
			seen[rc.F("reader")] = true
		}
	}
	return len(seen)
}

func (w *crWorld) round(r int) {
	h := w.h
	if w.stopAt > 0 && r == w.stopAt {
		h.stopMachine()
		w.running = false
	}
	if w.stopAt > 0 && r == w.stopAt+w.stopFor {
		h.startMachine()
		w.running = true
	}
	res := h.machine()
	h.clean(fmt.Sprintf("round %d after the tick", r))
	if w.running {
		w.holders(r)
		// E: a second tick right after changes nothing
		before := h.revisionsAll()
		res2 := h.machine()
		if len(res2.Moved()) > 0 || res2.Notes() > 0 {
			h.t.Fatalf("round %d: second tick moved %v notes %d", r, res2.Moved(), res2.Notes())
		}
		_ = before
	} else if len(res.Parts) > 0 || len(res.Repaired) > 0 {
		h.t.Fatalf("round %d: a STOPPED tick did %+v", r, res)
	}
	h.clean(fmt.Sprintf("round %d after the second tick", r))
	// workers
	if w.silentWorkers == 0 || r < w.silentWorkers {
		for _, m := range crMembers {
			h.run(TakeStep(sprint.TakeReq{As: m, Sel: sprint.Sel{Limit: 100}, Who: m}))
			s := h.snap()
			for _, c := range s.Fleet.Cell(m, sprint.Working) {
				fail := w.rng.Float64() < 0.15
				h.run(FinishStep(sprint.FinishReq{As: m, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Failed: fail, Report: "boom", Who: m}))
			}
		}
		h.clean(fmt.Sprintf("round %d after the workers", r))
	}
	// readers
	if w.silentReaders == 0 || r < w.silentReaders {
		for _, rd := range crReaders {
			h.run(ReadStep(sprint.ReadReq{As: rd, Begin: true, Sel: sprint.Sel{Limit: 100}, Who: rd}))
			s := h.snap()
			for _, c := range s.Readers.Cell(rd, sprint.Reading) {
				v := "ok"
				if w.rng.Float64() < 0.1 {
					v = "broken"
				}
				h.run(ReadStep(sprint.ReadReq{As: rd, Verdict: v, Finding: "f", Sel: sprint.Sel{IDs: []string{c.ID}}, Who: rd}))
			}
		}
		h.clean(fmt.Sprintf("round %d after the readers", r))
	}
	// the coordinator
	if w.silentCoord == 0 || r < w.silentCoord {
		w.coordinate(r)
		h.clean(fmt.Sprintf("round %d after the coordinator", r))
	}
	// the merger
	if w.silentMerger == 0 || r < w.silentMerger {
		s := h.snap()
		for _, st := range []string{"s1", "s2", "s3"} {
			q := s.Merge.Cell(st, sprint.Queued)
			if len(q) == 0 || s.StreamCtl(st).F("state") == sprint.StreamStopped {
				continue
			}
			req := sprint.MergeReq{Stream: st, Batch: 3, Who: "merger"}
			x := w.rng.Float64()
			switch {
			case x < 0.08:
				req.Conflict = q[0].ID
			case x < 0.12:
				req.Red = true
			case x < 0.14:
				req.Rejected = true
			case x < 0.20:
				// a cross need on another stream's placed, unlanded primary
				var others []string
				for _, o := range []string{"s1", "s2", "s3"} {
					if o == st {
						continue
					}
					for _, col := range []string{sprint.Ready, sprint.Working, sprint.Review, sprint.Merging} {
						for _, c := range s.Work.Cell(o, col) {
							others = append(others, c.ID)
						}
					}
				}
				if len(others) > 0 {
					req.Cross = q[0].ID + "=" + others[w.rng.IntN(len(others))]
				}
			}
			h.run(MergeStep(req))
		}
		h.clean(fmt.Sprintf("round %d after the merger", r))
	}
}

func (h *harness) revisionsAll() [4]uint64 {
	var out [4]uint64
	for i, tb := range All {
		out[i] = h.m.Revision(h.st.Names.Table(tb))
	}
	return out
}

func (w *crWorld) coordinate(r int) {
	h := w.h
	s := h.snap()
	open, _ := h.m.OpenNotes(h.ctx)
	if !w.droppedDone && r == 3 {
		h.run(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"dropme"}}, Reason: "not wanted"}))
		w.droppedDone = true
	}
	var rework, drop []string
	stopped := map[string]string{}
	for _, o := range open {
		sub := o.Subject()
		switch o.Note.Type {
		case sprint.NWorkFailed, sprint.NReadBroken, sprint.NReadsExhausted, sprint.NCIRed, sprint.NRepairSkipped:
			if pr := s.Work.Placed(sub); pr != nil && pr.Col == sprint.Review {
				rework = append(rework, sub)
			}
		case sprint.NBlocked:
			drop = append(drop, sub)
		case sprint.NConflict, sprint.NRed, sprint.NRejected, sprint.NCross:
			stopped[strings.TrimPrefix(sub, "stream:")] = o.Note.Type
		}
	}
	sort.Strings(rework)
	if len(rework) > 0 {
		h.run(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{Only: dedupe(rework)}, Fix: "fix it"}))
	}
	if len(drop) > 0 {
		h.run(DropStep(sprint.DropReq{Sel: sprint.Sel{Only: dedupe(drop)}, Reason: "blocked"}))
	}
	for st, typ := range stopped {
		if typ == sprint.NCross {
			w.crossAge[st]++
			if w.crossAge[st] < 4 {
				continue // wait: the tick resumes it when the card lands
			}
			// a long cross wait: return the stuck cards and resume
			var ids []string
			for _, c := range h.snap().Merge.Cell(st, sprint.Stuck) {
				ids = append(ids, c.ID)
			}
			if len(ids) > 0 {
				h.run(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: ids}, Reason: "cross wait"}))
			}
			w.crossAge[st] = 0
		}
		h.run(ResumeStep(sprint.ResumeReq{Stream: st, Did: "fixed"}))
	}
	for _, st := range []string{"s1", "s2", "s3"} {
		h.run(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{Stream: st}}))
	}
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func (w *crWorld) runTo(rounds int, step time.Duration) (int, bool) {
	w.running = true
	w.h.startMachine()
	for r := 1; r <= rounds; r++ {
		w.round(r)
		d := step
		if !w.running {
			d = time.Hour // stopped time is long: no deadline may count it
		}
		w.h.tick(d)
		if w.landed() == w.all() {
			return r, true
		}
	}
	return rounds, false
}

func TestCRFortyPrimariesLandByTheTick(t *testing.T) {
	t.Parallel()
	for seed := uint64(1); seed <= 20; seed++ {
		w := crSprint(t, seed)
		r, ok := w.runTo(600, time.Second)
		if !ok {
			s := w.h.snap()
			open, _ := w.h.m.OpenNotes(w.h.ctx)
			t.Errorf("seed %d: not landed after %d rounds: landed %d of %d; open %v; streams %s %s %s", seed, r, w.landed(), w.all(), openBy(open),
				s.StreamCtl("s1").F("state"), s.StreamCtl("s2").F("state"), s.StreamCtl("s3").F("state"))
		}
		if len(w.holderFail) > 0 {
			t.Errorf("seed %d: holders: %d failures, first: %v", seed, len(w.holderFail), w.holderFail[:min(5, len(w.holderFail))])
		}
		for _, st := range []string{"s1", "s2", "s3"} {
			if got := w.h.snap().StreamCtl(st).F("state"); got != sprint.StreamLanded {
				t.Errorf("seed %d: stream %s is %s", seed, st, got)
			}
		}
		// no deadline judgment was written in a run where everyone answers each second
		for _, typ := range []string{sprint.NWorkLate, sprint.NReadLate, sprint.NMergeLate, sprint.NInvariant} {
			if n := w.h.written(typ); n > 0 {
				t.Errorf("seed %d: %s written %d times", seed, typ, n)
			}
		}
	}
}

// The same with the machine stopped at every round from 1 to 30 for 3 rounds
// (an hour of clock each), outside actors going on.
func TestCRStopAtEveryPoint(t *testing.T) {
	t.Parallel()
	base := map[string]bool{}
	{
		w := crSprint(t, 5)
		if _, ok := w.runTo(600, time.Second); !ok {
			t.Fatalf("base did not land")
		}
		for _, st := range []string{"s1", "s2", "s3"} {
			for _, c := range w.h.snap().Merge.Cell(st, sprint.Merged) {
				base[c.ID] = true
			}
		}
	}
	for at := 1; at <= 30; at++ {
		w := crSprint(t, 5)
		w.stopAt, w.stopFor = at, 3
		r, ok := w.runTo(600, time.Second)
		if !ok {
			t.Errorf("stop at %d: not landed after %d rounds", at, r)
			continue
		}
		got := map[string]bool{}
		for _, st := range []string{"s1", "s2", "s3"} {
			for _, c := range w.h.snap().Merge.Cell(st, sprint.Merged) {
				got[c.ID] = true
			}
		}
		if len(got) != len(base) {
			t.Errorf("stop at %d: merged %d, base %d", at, len(got), len(base))
		}
		for _, typ := range []string{sprint.NWorkLate, sprint.NReadLate, sprint.NMergeLate} {
			if n := w.h.written(typ); n > 0 {
				t.Errorf("stop at %d: %s written %d times (stopped time counted?)", at, typ, n)
			}
		}
		if len(w.holderFail) > 0 {
			t.Errorf("stop at %d: holders: %v", at, w.holderFail[:min(3, len(w.holderFail))])
		}
	}
}

// Each outside class going silent at a random round: what is visible after
// sixty more rounds, five minutes apart.
func TestCRSilentActors(t *testing.T) {
	t.Parallel()
	for _, who := range []string{"workers", "readers", "merger", "coordinator"} {
		for seed := uint64(1); seed <= 3; seed++ {
			w := crSprint(t, seed)
			at := 3 + w.rng.IntN(15)
			switch who {
			case "workers":
				w.silentWorkers = at
			case "readers":
				w.silentReaders = at
			case "merger":
				w.silentMerger = at
			case "coordinator":
				w.silentCoord = at
			}
			w.running = true
			w.h.startMachine()
			for r := 1; r <= at+60; r++ {
				w.round(r)
				if r < at {
					w.h.tick(time.Second)
				} else {
					w.h.tick(5 * time.Minute)
				}
			}
			open, _ := w.h.m.OpenNotes(w.h.ctx)
			types := map[string]int{}
			for _, o := range open {
				types[o.Note.Type]++
			}
			stuckNoJudgment := dedupe(w.holderFail)
			t.Logf("%s silent from round %d (seed %d): landed %d/%d; open judgments by type %v; holder failures %d; ready-to-accept primaries never judged %d",
				who, at, seed, w.landed(), w.all(), types, len(stuckNoJudgment), len(w.readyToAccept))
		}
	}
}
