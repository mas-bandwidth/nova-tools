package sprintfn

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// TestTwinRefusesLikeLayerOne: a step Layer 1 refuses comes back from the
// twin with Layer 1's own code, detail and message, as a bare Mem given the
// same step refuses it, and leaves the twin byte-equal: the Mem's exported
// state, the sprint's keys and the log. A refusal from a phase of the pre
// stage leaves it byte-equal too.
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
		{"a create of a card that exists", tset.Entry{Kind: "create", Table: sprint.Work, To: "s1:waiting",
			IDs: []string{"p2"}, Scores: []string{"9"}, About: []string{"p2"}}},
		{"a create into a row that does not exist", tset.Entry{Kind: "create", Table: sprint.Work, To: "s9:waiting",
			IDs: []string{"p9"}, Scores: []string{"9"}, About: []string{"p9"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tw, m, log := newTestTwin(t, passX())
			mustStep(t, tw, seedRequest())

			bare := newTestMem(t)
			seed, ref := encodeStep(testPrefix, seedRequest())
			if ref != nil {
				t.Fatal(ref)
			}
			if _, err := bare.Step(context.Background(), seed.step); err != nil {
				t.Fatal(err)
			}
			req := &Request{Epoch: "0", Meta: Meta{Rule: "test", Tick: true}, Body: Body{Entries: []tset.Entry{c.entry}}}
			enc, ref := encodeStep(testPrefix, req)
			if ref != nil {
				t.Fatal(ref)
			}
			_, err := bare.Step(context.Background(), enc.step)
			var want *tset.Refusal
			if !errors.As(err, &want) {
				t.Fatalf("the bare Mem did not refuse: %v", err)
			}

			img := image(t, tw, m, log)
			res, err := Step(context.Background(), tw, req)
			if err != nil || res.Refusal == nil {
				t.Fatalf("twin: result %+v, err %v; want a refusal", res, err)
			}
			if res.Refusal.Code != want.Code || !reflect.DeepEqual(res.Refusal.Detail.RefusalDetail, want.Detail) ||
				res.Refusal.Message != want.Message || res.Refusal.Phase != PhasePlan {
				t.Fatalf("twin refused %+v in %s; Layer 1 refused %+v", res.Refusal, res.Refusal.Phase, want)
			}
			if got := image(t, tw, m, log); string(got) != string(img) {
				t.Fatalf("a refused step changed the twin:\nbefore %s\nafter  %s", img, got)
			}
		})
	}

	t.Run("a refusal of the pre stage", func(t *testing.T) {
		t.Parallel()
		phases := passX()
		phases.XPre = func(*State, *Request, *Before) *Refusal { return refuse("", CodeXGuard, RefusalDetail{}) }
		tw, m, log := newTestTwin(t, passX())
		mustStep(t, tw, seedRequest())
		tw.phases = phases
		img := image(t, tw, m, log)
		res, err := Step(context.Background(), tw, moveRequest("waiting", "ready"))
		if err != nil || res.Refusal == nil || res.Refusal.Code != CodeXGuard || res.Refusal.Phase != PhaseXPre {
			t.Fatalf("result %+v, err %v; want XGUARD from X.pre", res, err)
		}
		if got := image(t, tw, m, log); string(got) != string(img) {
			t.Fatal("a refusal of X.pre changed the twin")
		}
	})
}

// TestTwinDivergesAfterPlanRefusal: tset.Mem has no plan-only call (errata
// E7.1), so a phase that refuses after plan finds the tables already
// written. The twin does not report that as a refusal ("nothing was
// changed" would be false): it returns a DivergedError, and every later call
// returns it too.
func TestTwinDivergesAfterPlanRefusal(t *testing.T) {
	t.Parallel()
	tw, _, _ := newTestTwin(t, passX())
	mustStep(t, tw, seedRequest())
	refusing := PartFuncs{
		PreFunc: func(*State, *Request, *Before) (any, *Refusal) { return "plan", nil },
		CmdsFunc: func(*State, any, LogPlan) ([]Cmd, *Refusal) {
			return nil, refuse("", CodeLimit, RefusalDetail{})
		},
	}
	if err := tw.parts.Register(PartBeat, refusing); err != nil {
		t.Fatal(err)
	}
	req := moveRequest("waiting", "ready")
	req.Beat = &BeatPart{Members: []BeatMember{{Member: "m1"}}}
	res, err := Step(context.Background(), tw, req)
	var diverged *DivergedError
	if err != nil || !errors.As(res.Err, &diverged) || diverged.Phase != PhaseXPlan || diverged.Refusal.Code != CodeLimit {
		t.Fatalf("result %+v, err %v; want a DivergedError from X.plan", res, err)
	}
	next, err := Read(context.Background(), tw, &ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{{Kind: "rows", Table: sprint.Work}}})
	if err != nil || !errors.As(next.Err, &diverged) {
		t.Fatalf("the call after divergence: %+v, %v", next, err)
	}
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
// key that is not the sprint's own (a table's, the log's, a receipt) is
// refused in prepare; tset.Mem has already applied the table plan, so the
// twin diverges rather than claim nothing changed.
func TestTwinPrepareRefusesAForeignKey(t *testing.T) {
	t.Parallel()
	for _, key := range []string{testPrefix + "sprint:log@0", testPrefix + "sprint:done@0", testPrefix + "sprint:epoch",
		testPrefix + "table:work:rows", testPrefix + "member:work:p1", "other:sprint:x"} {
		phases := passX()
		phases.XCmds = func(*State, TablePlan, LogPlan) []Cmd { return []Cmd{Command("HSET", key, kindHash, "f", "v")} }
		tw, _, _ := newTestTwin(t, phases)
		res, err := Step(context.Background(), tw, seedRequest())
		var diverged *DivergedError
		if err != nil || !errors.As(res.Err, &diverged) || diverged.Phase != PhasePrepare || diverged.Refusal.Code != CodeRequest {
			t.Fatalf("a write of %s: %+v, %v", key, res, err)
		}
	}
}

// TestTwinEqualsLua10000: the twin and the Lua give the same replies and the
// same images over 10,000 random steps. It needs the store.
func TestTwinEqualsLua10000(t *testing.T) {
	t.Parallel()
	t.Skip("G0: needs the store (Layer 1 revision 4 pinned, Layer 2 accepted again), the sprint profile loaded in a container, and items E6 and E7")
}
