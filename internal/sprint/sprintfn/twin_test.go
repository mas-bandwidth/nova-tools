package sprintfn

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// writingTwin is a twin with every writer of the write path active: traced X,
// derive and J, a lease part that writes a key, and the four tables seeded
// (which wrote X's and the lease's keys once), so a refused step that still
// applied any writer's commands changes the image. The tracer records what the
// next steps run.
func writingTwin(t *testing.T) (*Twin, *tset.Mem, *MemLog, *tracer) {
	t.Helper()
	tr := &tracer{}
	tw, m, log := newTestTwin(t, tracedPhases(tr))
	if err := tw.parts.Register(PartLease, tracedLease(tr)); err != nil {
		t.Fatal(err)
	}
	seed := seedRequest()
	seed.Lease = &LeasePart{Owner: "seed", HoldMS: 1000}
	mustStep(t, tw, seed)
	tw.trace = tr.add
	tr.reset()
	return tw, m, log, tr
}

// withWriters makes every writer of a request's step active: an intent (derive
// adds an entry of its own), a note request (J), and a lease part.
func withWriters(req *Request) *Request {
	req.Body.Intents = []Intent{{Kind: "needmet", Card: "p2", Need: "n0", Waiters: []string{"p2"}}}
	req.Body.Notes = []NoteReq{{Op: "open", Type: "blocked", Cause: "c", Subjects: []string{"p1"}}}
	req.Lease = &LeasePart{Owner: "tok", HoldMS: 5000}
	return req
}

// commandHooks are the trace entries of the phase that only computes
// commands, which run after the table plan: none of them may run for a step
// that is refused before that phase.
var commandHooks = []string{"hook:X.cmds", "hook:J.cmds", "hook:lease.cmds"}

func ranAny(trace []string, names []string) bool {
	for _, s := range trace {
		for _, n := range names {
			if s == n {
				return true
			}
		}
	}
	return false
}

// TestTwinRefusesLikeLayerOne: a step Layer 1 refuses comes back from the
// twin with Layer 1's own code, detail and message, as a bare Mem given the
// same step refuses it, and leaves the twin byte-equal: the Mem's exported
// state, the sprint's keys and the log. It is refused with every writer
// active (X, derive and J deciding, a lease part, a note request), so a twin
// whose refusal path went on to apply X's, J's or a part's commands would
// change the image. A refusal from a phase of the pre stage, from a part's
// pre, and OPCONFLICT leave it byte-equal too.
func TestTwinRefusesLikeLayerOne(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		entry tset.Entry
	}{
		{"a move from a cell the card is not in", tset.Entry{Kind: "move", Table: sprint.Work, From: "s1:ready", To: "s1:working",
			IDs: []string{"p1"}, About: []string{"p1"}}},
		{"a guard at a revision the card is past", tset.Entry{Kind: "guard", Table: sprint.Work, From: "s1:waiting",
			IDs: []string{"p1"}, Revs: []tset.Decimal{"7"}}},
		// p1, not p2: derive's entry moves p2, and a step that names a card twice
		// is refused for that instead.
		{"a create of a card that exists", tset.Entry{Kind: "create", Table: sprint.Work, To: "s1:waiting",
			IDs: []string{"p1"}, Scores: []string{"9"}, About: []string{"p1"}}},
		{"a create into a row that does not exist", tset.Entry{Kind: "create", Table: sprint.Work, To: "s9:waiting",
			IDs: []string{"p9"}, Scores: []string{"9"}, About: []string{"p9"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tw, m, log, tr := writingTwin(t)

			bare := newTestMem(t)
			seed, ref := encodeStep(testPrefix, seedRequest())
			if ref != nil {
				t.Fatal(ref)
			}
			if _, err := bare.Step(context.Background(), seed.step); err != nil {
				t.Fatal(err)
			}
			single := &Request{Epoch: "0", Meta: Meta{Rule: "test", Tick: true}, Body: Body{Entries: []tset.Entry{c.entry}}}
			enc, ref := encodeStep(testPrefix, single)
			if ref != nil {
				t.Fatal(ref)
			}
			_, err := bare.Step(context.Background(), enc.step)
			var want *tset.Refusal
			if !errors.As(err, &want) {
				t.Fatalf("the bare Mem did not refuse: %v", err)
			}

			req := withWriters(&Request{Epoch: "0", Meta: Meta{Rule: "test", Tick: true}, Body: Body{Entries: []tset.Entry{c.entry}}})
			img := image(t, tw, m, log)
			res, err := Step(context.Background(), tw, req)
			if err != nil || res.Refusal == nil {
				t.Fatalf("twin: result %+v, err %v; want a refusal", res, err)
			}
			if res.Refusal.Code != want.Code || !reflect.DeepEqual(res.Refusal.Detail.RefusalDetail, want.Detail) ||
				res.Refusal.Message != want.Message || res.Refusal.Phase != PhasePlan {
				t.Fatalf("twin refused %+v in %s; Layer 1 refused %+v", res.Refusal, res.Refusal.Phase, want)
			}
			trace := tr.list()
			for _, hook := range []string{"hook:X.pre", "hook:derive", "hook:J", "hook:lease.pre"} {
				if !ranAny(trace, []string{hook}) {
					t.Fatalf("%s did not run, so the refusal was not tested with that writer active: %v", hook, trace)
				}
			}
			if got := image(t, tw, m, log); string(got) != string(img) {
				t.Fatalf("a refused step changed the twin:\nbefore %s\nafter  %s", img, got)
			}
			if ranAny(trace, commandHooks) {
				t.Fatalf("a step refused at plan computed commands: %v", trace)
			}
		})
	}

	// A refusal from a phase of the pre stage or from a part's pre, with every
	// later writer registered: the phases after it never run.
	pre := []struct {
		name  string
		phase string
		code  string
		set   func(tw *Twin)
	}{
		{"X.pre", PhaseXPre, CodeXGuard, func(tw *Twin) {
			tw.phases.XPre = func(*State, *Request, *Before) *Refusal { return refuse("", CodeXGuard, RefusalDetail{}) }
		}},
		{"derive", PhaseDerive, CodeCounter, func(tw *Twin) {
			tw.phases.Derive = func(*State, []Intent, *Before) ([]tset.Entry, []NoteReq, *Refusal) {
				return nil, nil, refuse("", CodeCounter, RefusalDetail{})
			}
		}},
		{"J", PhaseJ, CodeDropping, func(tw *Twin) {
			tw.phases.JDecide = func(*State, []NoteReq, *Before) ([]tset.Note, JPlan, *Refusal) {
				return nil, JPlan{}, refuse("", CodeDropping, RefusalDetail{})
			}
		}},
		{"a part's pre", PhaseParts, CodeStaleGen, func(tw *Twin) {
			refusing := PartFuncs{
				PreFunc: func(*State, *Request, *Before) (any, *Refusal) { return nil, refuse("", CodeStaleGen, RefusalDetail{}) },
				CmdsFunc: func(*State, any, LogPlan) ([]Cmd, *Refusal) {
					return []Cmd{Command("HSET", testPrefix+"sprint:beat", kindHash, "f", "v")}, nil
				},
			}
			if err := tw.parts.Register(PartBeat, refusing); err != nil {
				panic(err)
			}
		}},
	}
	for _, c := range pre {
		t.Run("a refusal of "+c.name, func(t *testing.T) {
			t.Parallel()
			tw, m, log, tr := writingTwin(t)
			c.set(tw)
			req := withWriters(moveRequest("waiting", "ready"))
			req.Beat = &BeatPart{Members: []BeatMember{{Member: "m1"}}}
			img := image(t, tw, m, log)
			res, err := Step(context.Background(), tw, req)
			if err != nil || res.Refusal == nil || res.Refusal.Code != c.code || res.Refusal.Phase != c.phase {
				t.Fatalf("result %+v, err %v; want %s from %s", res, err, c.code, c.phase)
			}
			if got := image(t, tw, m, log); string(got) != string(img) {
				t.Fatalf("a refusal of %s changed the twin", c.name)
			}
			if ranAny(tr.list(), commandHooks) {
				t.Fatalf("a step refused in %s computed commands: %v", c.phase, tr.list())
			}
		})
	}

	t.Run("OPCONFLICT with every writer active", func(t *testing.T) {
		t.Parallel()
		tw, m, log, tr := writingTwin(t)
		first := moveRequest("waiting", "ready")
		first.Body.Op = &Op{ID: "op-1/p1", Intent: "move p1"}
		mustStep(t, tw, first)
		tr.reset()
		conflict := withWriters(moveRequest("ready", "working"))
		conflict.Body.Op = &Op{ID: "op-1/p1", Intent: "another intent"}
		img := image(t, tw, m, log)
		res, err := Step(context.Background(), tw, conflict)
		if err != nil || res.Refusal == nil || res.Refusal.Code != "OPCONFLICT" {
			t.Fatalf("an op with another intent: %+v, %v", res, err)
		}
		if got := tr.list(); !sameStrings(got, []string{PhaseOpen}) {
			t.Fatalf("a conflicting op ran %v; it must stop at open", got)
		}
		if got := image(t, tw, m, log); string(got) != string(img) {
			t.Fatal("an OPCONFLICT refusal changed the twin")
		}
	})
}

// refusingLog is a log twin that refuses every plan, for a refusal of the log
// phase, and serves everything else as the stub does.
type refusingLog struct {
	*MemLog
	ref *Refusal
}

func (l *refusingLog) Plan(LogInput) (LogPlan, LogApply, *Refusal) { return LogPlan{}, nil, l.ref }

// TestTwinRefusalAfterPlanLeavesNothing: a refusal from any phase after plan
// (the log's LIMIT or OVERFLOW, a part's Cmds, prepare's foreign key, its
// WRONGTYPE and its shared bounds), with every writer active, is a refusal
// that leaves the Mem's exported state, the sprint's keys and the log
// byte-equal, as the store's plan-then-commit leaves them (errata E6, E7.1):
// the twin plans Layer 1 with Mem.Plan and commits with Mem.Commit only after
// every later phase passed (S15). No commit hook runs, the twin is not left
// broken, and the next step applies.
func TestTwinRefusalAfterPlanLeavesNothing(t *testing.T) {
	t.Parallel()
	many := func(prefix string, n int) []Cmd {
		out := make([]Cmd, n)
		for i := range out {
			out[i] = Command("HSET", prefix+strconv.Itoa(i), kindHash, "f", "v")
		}
		return out
	}
	cases := []struct {
		name  string
		phase string
		code  string
		set   func(tw *Twin, log *MemLog)
	}{
		{"the log's LIMIT", PhaseLog, CodeLimit, func(tw *Twin, log *MemLog) {
			tw.log = &refusingLog{MemLog: log, ref: refuse(PhaseLog, CodeLimit, RefusalDetail{RefusalDetail: tset.RefusalDetail{Budget: "line_bytes"}})}
		}},
		{"the log's OVERFLOW", PhaseLog, "OVERFLOW", func(tw *Twin, log *MemLog) {
			tw.log = &refusingLog{MemLog: log, ref: refuse(PhaseLog, "OVERFLOW", RefusalDetail{})}
		}},
		{"a part's Cmds", PhaseXPlan, CodeLimit, func(tw *Twin, log *MemLog) {
			refusing := PartFuncs{
				PreFunc:  func(*State, *Request, *Before) (any, *Refusal) { return "plan", nil },
				CmdsFunc: func(*State, any, LogPlan) ([]Cmd, *Refusal) { return nil, refuse("", CodeLimit, RefusalDetail{}) },
			}
			if err := tw.parts.Register(PartBeat, refusing); err != nil {
				panic(err)
			}
		}},
		{"prepare's foreign key", PhasePrepare, CodeRequest, func(tw *Twin, log *MemLog) {
			tw.phases.XCmds = func(*State, TablePlan, LogPlan) []Cmd {
				return []Cmd{Command("HSET", testPrefix+"sprint:log@0", kindHash, "f", "v")}
			}
		}},
		{"prepare's WRONGTYPE", PhasePrepare, CodeWrongType, func(tw *Twin, log *MemLog) {
			tw.phases.XCmds = func(*State, TablePlan, LogPlan) []Cmd {
				return []Cmd{Command("HSET", testPrefix+"sprint:lease", kindHash, "f", "v"),
					Command("ZADD", testPrefix+"sprint:lease", kindZSet, "1", "m")}
			}
		}},
		{"prepare's shared bounds", PhasePrepare, CodeLimit, func(tw *Twin, log *MemLog) {
			tw.phases.XCmds = func(*State, TablePlan, LogPlan) []Cmd { return many(testPrefix+"sprint:k", tset.MaxPlannedCommands+1) }
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tw, m, log, tr := writingTwin(t)
			c.set(tw, log)
			req := withWriters(moveRequest("waiting", "ready"))
			if c.phase == PhaseXPlan {
				req.Beat = &BeatPart{Members: []BeatMember{{Member: "m1"}}}
			}
			before := capture(t, tw, m, log)
			res, err := Step(context.Background(), tw, req)
			if err != nil || res.Err != nil || res.Refusal == nil || res.Refusal.Code != c.code || res.Refusal.Phase != c.phase {
				t.Fatalf("result %+v, err %v; want %s from %s", res, err, c.code, c.phase)
			}
			unchanged(t, "a refusal of "+c.name, before, capture(t, tw, m, log))
			if ranAny(tr.list(), []string{PhaseCommit}) {
				t.Fatalf("a refusal of %s reached commit: %v", c.name, tr.list())
			}
			// The twin still stands for the store: the same move, with nothing
			// refusing, applies.
			restoreWriters(tw, log)
			mustStep(t, tw, withWriters(moveRequest("waiting", "ready")))
		})
	}
}

// restoreWriters undoes what a case of TestTwinRefusalAfterPlanLeavesNothing
// set: the log twin back, X's commands back to the traced ones, and the
// refusing beat part out of the registry.
func restoreWriters(tw *Twin, log *MemLog) {
	tw.log = log
	tw.phases.XCmds = tracedPhases(&tracer{}).XCmds
	tw.parts.mu.Lock()
	delete(tw.parts.parts, PartBeat)
	tw.parts.mu.Unlock()
}

// bigX is X whose commands fill the shared argv bound to within room bytes of
// its end, counting the log's bytes, which X is handed: one HSET whose value
// takes up the rest.
func bigX(room int) Phases {
	phases := passX()
	phases.XCmds = func(st *State, tp TablePlan, lp LogPlan) []Cmd {
		key := st.Prefix + "sprint:big"
		size := tset.MaxPlannedArgvBytes - room - lp.ArgvBytes - len("HSET") - len(key) - len("f")
		return []Cmd{Command("HSET", key, kindHash, "f", strings.Repeat("v", size))}
	}
	return phases
}

// TestTwinSharedBoundCountsLayerOneAtCommit: the one case the twin cannot
// refuse before it writes. Prepare's shared bound holds Layer 1's planned
// argv bytes with the log's and the sprint's (L1 6); tset.MemPlan does not
// carry Layer 1's counts, which arrive with Mem.Commit. So a step whose log
// and sprint bytes fit, and which only Layer 1's bytes put over the bound, is
// found after the commit: the store refuses it and writes nothing, the twin
// has written its tables, and it says so with a DivergedError that names the
// store's refusal, on that call and every later one. A step that fits with
// Layer 1's bytes counted applies; one that the log's and the sprint's bytes
// alone put over the bound is refused before anything is written.
func TestTwinSharedBoundCountsLayerOneAtCommit(t *testing.T) {
	t.Parallel()

	t.Run("fits with Layer 1's share", func(t *testing.T) {
		t.Parallel()
		tw, _, _ := newTestTwin(t, bigX(4096))
		mustStep(t, tw, seedRequest())
	})

	t.Run("over without Layer 1's share", func(t *testing.T) {
		t.Parallel()
		tw, m, log := newTestTwin(t, bigX(-1))
		before := capture(t, tw, m, log)
		res, err := Step(context.Background(), tw, seedRequest())
		if err != nil || res.Refusal == nil || res.Refusal.Code != CodeLimit || res.Refusal.Detail.Budget != "argv_bytes" ||
			*res.Refusal.Detail.Actual != int64(tset.MaxPlannedArgvBytes+1) || *res.Refusal.Detail.Limit != int64(tset.MaxPlannedArgvBytes) {
			t.Fatalf("result %+v, err %v; want LIMIT argv_bytes one over the bound, before the commit", res, err)
		}
		unchanged(t, "a step over the bound without Layer 1's share", before, capture(t, tw, m, log))
	})

	t.Run("over only with Layer 1's share", func(t *testing.T) {
		t.Parallel()
		tw, m, _ := newTestTwin(t, bigX(1))
		res, err := Step(context.Background(), tw, seedRequest())
		var diverged *DivergedError
		if err != nil || res.Refusal != nil || !errors.As(res.Err, &diverged) || diverged.Phase != PhasePrepare ||
			diverged.Reason.Code != CodeLimit || diverged.Reason.Detail.Budget != "argv_bytes" {
			t.Fatalf("result %+v, err %v; want a DivergedError naming LIMIT argv_bytes", res, err)
		}
		var ref *Refusal
		if errors.As(res.Err, &ref) {
			t.Fatal("a DivergedError unwraps to a refusal, which would read as nothing was changed")
		}
		snap, err := m.Snapshot(testPrefix)
		if err != nil || snap.Epochs["0"].Tables[sprint.Work].Records["p1"].Revision == "" {
			t.Fatalf("the Mem has not written the step (%v): the case does not reach Layer 1's share", err)
		}
		next, err := Read(context.Background(), tw, &ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{{Kind: "rows", Table: sprint.Work}}})
		if err != nil || !errors.As(next.Err, &diverged) {
			t.Fatalf("the call after divergence: %+v, %v", next, err)
		}
	})
}

// TestReadOneSnapshot: an atomic read on the twin answers every query from
// one state and under one time: the pipeline read, step, read sees the step
// in the second read only, each read's count agrees with its range, and the
// twin samples its clock once a call. (The store's half is in redis_test.go:
// one FCALL_RO carries every query.)
func TestReadOneSnapshot(t *testing.T) {
	t.Parallel()
	tw, _, _ := newTestTwin(t, passX())
	mustStep(t, tw, seedRequest())
	samples := 0
	tw.SetClock(func() time.Time {
		samples++
		return testTime.Add(time.Duration(samples) * time.Millisecond)
	})
	read := &ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{
		{Kind: "count", Table: sprint.Work, Cells: []string{"s1:waiting", "s1:ready"}},
		{Kind: "range", Table: sprint.Work, Cell: "s1:waiting", Min: "-inf", Max: "+inf", Limit: 10},
		{Kind: "last"},
	}}
	results, err := tw.Pipeline(context.Background(), []Item{{Read: read}, {Step: moveRequest("waiting", "ready")}, {Read: read}})
	if err != nil {
		t.Fatal(err)
	}
	if samples != 3 {
		t.Fatalf("the twin sampled its clock %d times for 3 calls", samples)
	}
	first, second := results[0].Read, results[2].Read
	if first == nil || second == nil || results[1].Step == nil {
		t.Fatalf("results %+v", results)
	}
	if !reflect.DeepEqual(first.Tset[0].Counts, []uint64{2, 0}) || !reflect.DeepEqual(first.Tset[1].IDs, []string{"p1", "p2"}) ||
		first.Tset[2].LastSeq != "2" {
		t.Fatalf("first read %+v", first.Tset)
	}
	if !reflect.DeepEqual(second.Tset[0].Counts, []uint64{1, 1}) || !reflect.DeepEqual(second.Tset[1].IDs, []string{"p2"}) ||
		second.Tset[2].LastSeq != "3" {
		t.Fatalf("second read %+v", second.Tset)
	}
	if first.TimeMS == second.TimeMS || first.ActiveEpoch != "0" {
		t.Fatalf("read times %s and %s, active %s", first.TimeMS, second.TimeMS, first.ActiveEpoch)
	}
}

// TestTwinReadAnswersInInputOrder: a read that mixes Layer 1's and Layer 2's
// queries answers each in its own place, whatever the mix: the twin answers
// Layer 2's queries from the log and Layer 1's from the Mem in one call, and
// the answers come back aligned with the queries, not grouped by source.
func TestTwinReadAnswersInInputOrder(t *testing.T) {
	t.Parallel()
	tw, _, _ := newTestTwin(t, passX())
	mustStep(t, tw, seedRequest())
	count := tset.ReadQuery{Kind: "count", Table: sprint.Work, Cells: []string{"s1:waiting"}}
	rng := tset.ReadQuery{Kind: "range", Table: sprint.Work, Cell: "s1:waiting", Min: "-inf", Max: "+inf", Limit: 10}
	last := tset.ReadQuery{Kind: "last"}
	lines := tset.ReadQuery{Kind: "lines", AfterSeq: "0", Limit: 10}
	for _, qs := range [][]tset.ReadQuery{
		{count, last, rng, lines},
		{last, count, lines, rng},
		{lines, last, count, rng, last, count},
	} {
		res, err := Read(context.Background(), tw, &ReadRequest{Epoch: "0", Tset: qs})
		if err != nil || res.Read == nil {
			t.Fatalf("read %v: %+v, %v", qs, res, err)
		}
		if len(res.Read.Tset) != len(qs) {
			t.Fatalf("%d answers for %d queries", len(res.Read.Tset), len(qs))
		}
		for i, q := range qs {
			if res.Read.Tset[i].Kind != q.Kind {
				t.Fatalf("answer %d is a %q, the query is a %q: answers are out of place %+v", i, res.Read.Tset[i].Kind, q.Kind, res.Read.Tset)
			}
		}
	}
}

// TestTwinReadRefusesAtTheFirstIndex: when Layer 1's and Layer 2's queries both
// refuse, the twin reports the lower index, as the store checks the queries in
// input order (L1 8), whichever layer answers it.
func TestTwinReadRefusesAtTheFirstIndex(t *testing.T) {
	t.Parallel()
	tw, _, _ := newTestTwin(t, passX())
	mustStep(t, tw, seedRequest())
	badL1 := tset.ReadQuery{Kind: "count", Table: "nosuch", Cells: []string{"s1:waiting"}}
	okL1 := tset.ReadQuery{Kind: "count", Table: sprint.Work, Cells: []string{"s1:waiting"}}
	// A lines query whose after_seq is past the log's tail is CURSOR.
	badL2 := tset.ReadQuery{Kind: "lines", AfterSeq: "999", Limit: 5}
	okL2 := tset.ReadQuery{Kind: "last"}
	for _, c := range []struct {
		name string
		qs   []tset.ReadQuery
		at   int
	}{
		{"layer 1 then layer 2", []tset.ReadQuery{badL1, badL2}, 0},
		{"layer 2 then layer 1", []tset.ReadQuery{badL2, badL1}, 0},
		{"answers before, layer 1 refuses first", []tset.ReadQuery{okL1, okL2, badL1, badL2}, 2},
		{"answers before, layer 2 refuses first", []tset.ReadQuery{okL2, okL1, badL2, badL1}, 2},
		{"only layer 2 refuses", []tset.ReadQuery{okL1, badL2}, 1},
		{"only layer 1 refuses", []tset.ReadQuery{okL2, badL1}, 1},
		{"layer 2 refuses twice, layer 1 between", []tset.ReadQuery{badL2, badL1, badL2}, 0},
		{"layer 1 refuses twice, layer 2 between", []tset.ReadQuery{badL1, badL2, badL1}, 0},
	} {
		res, err := Read(context.Background(), tw, &ReadRequest{Epoch: "0", Tset: c.qs})
		if err != nil || res.Refusal == nil || res.Refusal.Detail.QueryIndex == nil {
			t.Fatalf("%s: result %+v, err %v; want a refusal that names its query", c.name, res, err)
		}
		if got := *res.Refusal.Detail.QueryIndex; got != c.at {
			t.Fatalf("%s: refused at query %d, want %d (%s)", c.name, got, c.at, res.Refusal)
		}
	}
	// A sprint query comes after every Layer 1 and Layer 2 query: their refusal
	// is reported and the sprint query is not asked.
	asked := false
	tw.phases.Query = func(*State, SprintQuery) (json.RawMessage, *Refusal) {
		asked = true
		return json.RawMessage(`{}`), nil
	}
	res, err := Read(context.Background(), tw, &ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{badL2}, Sprint: []SprintQuery{relatedQuery}})
	if err != nil || res.Refusal == nil || *res.Refusal.Detail.QueryIndex != 0 || asked {
		t.Fatalf("a refusing layer 2 query before a sprint query: %+v, %v, sprint asked %v", res, err, asked)
	}
}

// TestTwinReplayCarriesLogSeqs: an op's replay on the twin returns the seqs
// its step wrote, although tset.Mem's receipt, having no log, holds "0", and
// runs no phase.
func TestTwinReplayCarriesLogSeqs(t *testing.T) {
	t.Parallel()
	tr := &tracer{}
	tw, _, _ := newTestTwin(t, passX())
	req := seedRequest()
	req.Body.Op = &Op{ID: "add-1/p1", Intent: "add s1 2"}
	first := mustStep(t, tw, req)
	tw.trace = tr.add
	again := mustStep(t, tw, req)
	if !again.Reply.Replay || again.Reply.FirstSeq != first.Reply.FirstSeq || again.Reply.LastSeq != first.Reply.LastSeq ||
		first.Reply.LastSeq != "2" {
		t.Fatalf("first %+v, replay %+v", first.Reply, again.Reply)
	}
	if got := tr.list(); !sameStrings(got, []string{PhaseOpen}) {
		t.Fatalf("a replay ran %v", got)
	}
	done, err := Read(context.Background(), tw, &ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{{Kind: "done",
		Ops: []tset.DoneIdentity{{Epoch: "0", Op: "add-1/p1", IntentDigest: "0000000000000000000000000000000000000000"}}}}})
	if err != nil || done.Read == nil || done.Read.Tset[0].Done[0].Status != "conflict" {
		t.Fatalf("done of another intent: %+v, %v", done, err)
	}
	conflict := seedRequest()
	conflict.Body.Op = &Op{ID: "add-1/p1", Intent: "another"}
	res, err := Step(context.Background(), tw, conflict)
	if err != nil || res.Refusal == nil || res.Refusal.Code != "OPCONFLICT" {
		t.Fatalf("an op with another intent: %+v, %v", res, err)
	}
	if _, err := json.Marshal(again.Reply); err != nil {
		t.Fatal(err)
	}
}

// TestTwinRefusesAMissingPhase: a twin assembled without X, or without derive
// for a step that carries intents, refuses CONFIG and writes nothing, so a
// partial assembly never writes as if the phase had run.
func TestTwinRefusesAMissingPhase(t *testing.T) {
	t.Parallel()
	tw, m, log := newTestTwin(t, Phases{})
	img := image(t, tw, m, log)
	res, err := Step(context.Background(), tw, seedRequest())
	if err != nil || res.Refusal == nil || res.Refusal.Code != CodeConfig || res.Refusal.Phase != PhaseXPre {
		t.Fatalf("no X: %+v, %v", res, err)
	}
	tw.phases = passX()
	req := seedRequest()
	req.Body.Intents = []Intent{{Kind: "waitfor", Card: "p1", Needs: []string{"n1"}}}
	res, err = Step(context.Background(), tw, req)
	if err != nil || res.Refusal == nil || res.Refusal.Code != CodeConfig || res.Refusal.Phase != PhaseDerive {
		t.Fatalf("no derive: %+v, %v", res, err)
	}
	req = seedRequest()
	req.Lease = &LeasePart{Owner: "tok"}
	res, err = Step(context.Background(), tw, req)
	if err != nil || res.Refusal == nil || res.Refusal.Code != CodeConfig || res.Refusal.Phase != PhaseParts {
		t.Fatalf("no lease part: %+v, %v", res, err)
	}
	if string(image(t, tw, m, log)) != string(img) {
		t.Fatal("a CONFIG refusal changed the twin")
	}
}

// TestTwinPrepareRefusesAForeignKey: a command of X.plan, J or a part on a
// key that is not the sprint's own (a table's, the log's, a receipt, the
// epoch marker, a member, another deployment's) is refused REQUEST in
// prepare, and the twin is as it was: Layer 1's plan was never committed.
func TestTwinPrepareRefusesAForeignKey(t *testing.T) {
	t.Parallel()
	for _, key := range []string{testPrefix + "sprint:log@0", testPrefix + "sprint:done@0", testPrefix + "sprint:epoch",
		testPrefix + "table:work:rows", testPrefix + "member:work:p1", "other:sprint:x"} {
		phases := passX()
		phases.XCmds = func(*State, TablePlan, LogPlan) []Cmd { return []Cmd{Command("HSET", key, kindHash, "f", "v")} }
		tw, m, log := newTestTwin(t, phases)
		before := capture(t, tw, m, log)
		res, err := Step(context.Background(), tw, seedRequest())
		if err != nil || res.Err != nil || res.Refusal == nil || res.Refusal.Phase != PhasePrepare || res.Refusal.Code != CodeRequest {
			t.Fatalf("a write of %s: %+v, %v; want REQUEST from prepare", key, res, err)
		}
		unchanged(t, "a write of "+key, before, capture(t, tw, m, log))
		tw.phases = passX()
		mustStep(t, tw, seedRequest())
	}
}

// TestTwinEqualsLua10000: the twin and the Lua give the same replies and the
// same images over 10,000 random steps. It needs the store.
func TestTwinEqualsLua10000(t *testing.T) {
	t.Parallel()
	t.Skip("G0: needs the store (Layer 1 revision 4 pinned, Layer 2 accepted again) and the sprint profile loaded in a container")
}

// TestTwinTablePlanCountsMatchTheReply: tset.MemPlan carries no counts, so the
// twin counts what X.plan's table plan says (changed, guarded, changed per
// entry) from the plan and the step; the reply Mem.Commit gives afterwards
// says the same, for a step with a rows entry, a move, a guard, a remove and
// a create of two cards.
func TestTwinTablePlanCountsMatchTheReply(t *testing.T) {
	t.Parallel()
	var seen TablePlan
	phases := passX()
	phases.XCmds = func(_ *State, tp TablePlan, _ LogPlan) []Cmd { seen = tp; return nil }
	tw, m, _ := newTestTwin(t, phases)
	mustStep(t, tw, seedRequest())
	mustStep(t, tw, &Request{Epoch: "0", Meta: Meta{Verb: "add", Actor: "coordinator"}, Body: Body{Entries: []tset.Entry{
		{Kind: "create", Table: sprint.Work, To: "s1:waiting", IDs: []string{"p3"}, Scores: []string{"3"}, About: []string{"p3"}}}}})
	snap, err := m.Snapshot(testPrefix)
	if err != nil {
		t.Fatal(err)
	}
	rev := snap.Epochs["0"].Tables[sprint.Work].Records["p2"].Revision
	req := &Request{Epoch: "0", Meta: Meta{Rule: "test", Tick: true}, Body: Body{Entries: []tset.Entry{
		{Kind: "rows", Table: sprint.Work, Add: []string{"s2"}},
		{Kind: "move", Table: sprint.Work, From: "s1:waiting", To: "s1:ready", IDs: []string{"p1"}, About: []string{"p1"}},
		{Kind: "guard", Table: sprint.Work, From: "s1:waiting", IDs: []string{"p2"}, Revs: []tset.Decimal{rev}},
		{Kind: "remove", Table: sprint.Work, From: "s1:waiting", IDs: []string{"p3"}, About: []string{"p3"}},
		{Kind: "create", Table: sprint.Work, To: "s2:waiting", IDs: []string{"p4", "p5"}, Scores: []string{"1", "2"}, About: []string{"p4", "p5"}},
	}}}
	got := mustStep(t, tw, req)
	if seen.Changed != got.Reply.Changed || seen.Guarded != got.Reply.Guarded || !reflect.DeepEqual(seen.ChangedPerEntry, got.Reply.ChangedPerEntry) {
		t.Fatalf("the table plan counted changed %d, guarded %d, per entry %v; the reply says %d, %d, %v",
			seen.Changed, seen.Guarded, seen.ChangedPerEntry, got.Reply.Changed, got.Reply.Guarded, got.Reply.ChangedPerEntry)
	}
	if seen.Changed != 4 || seen.Guarded != 1 || !reflect.DeepEqual(seen.ChangedPerEntry, []int{0, 1, 0, 1, 2}) {
		t.Fatalf("counts %d, %d, %v; want 4 changed, 1 guarded, [0 1 0 1 2]", seen.Changed, seen.Guarded, seen.ChangedPerEntry)
	}
}

// TestTwinAdvanceNeedsAnOpAndLogsAtTheNewEpoch: an advance without an op and
// an intent is refused REQUEST before anything runs (Layer 1's static rule of
// S15's amendment); with both, the twin plans the log at the epoch the step
// writes, the successor, which the plan does not state and the twin works out
// before the commit, and the reply's epoch after is that epoch.
func TestTwinAdvanceNeedsAnOpAndLogsAtTheNewEpoch(t *testing.T) {
	t.Parallel()
	tw, m, log := newTestTwin(t, passX())
	mustStep(t, tw, seedRequest())
	advance := func() *Request {
		return &Request{Epoch: "0", Meta: Meta{Verb: "clear", Actor: "coordinator"}, Body: Body{Entries: []tset.Entry{
			{Kind: "advance", AdvanceFrom: "0"}}}}
	}
	before := capture(t, tw, m, log)
	_, err := Step(context.Background(), tw, advance())
	var ref *Refusal
	if !errors.As(err, &ref) || ref.Code != CodeRequest {
		t.Fatalf("an advance without an op: %v; want REQUEST before anything runs", err)
	}
	unchanged(t, "an advance without an op", before, capture(t, tw, m, log))

	req := advance()
	req.Body.Op = &Op{ID: "clear-1", Intent: "clear"}
	got := mustStep(t, tw, req)
	if got.Reply.EpochAfter != "1" || len(log.Lines(testPrefix, "1")) != 1 || len(log.Lines(testPrefix, "0")) != 2 {
		t.Fatalf("advance: epoch after %s, lines at 1: %d, at 0: %d; want 1, the advance line at the new epoch, and the seed's two",
			got.Reply.EpochAfter, len(log.Lines(testPrefix, "1")), len(log.Lines(testPrefix, "0")))
	}
}
