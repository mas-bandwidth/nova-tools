package sprintfn

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// tracedPhases are phases that record each call in tr and do a little real
// work: X.pre and derive read what the pre stage read, derive turns an intent
// into a field change of its card, J makes one note, and X.plan and J write
// sprint keys that carry the log's seqs.
func tracedPhases(tr *tracer) Phases {
	return Phases{
		Before: func(st *State, req *Request) []BeforeAsk {
			tr.add("hook:before")
			return []BeforeAsk{{Table: sprint.Work, IDs: []string{"p2"}, Fields: []string{"kind"}}}
		},
		XPre: func(st *State, req *Request, obs *Before) *Refusal {
			tr.add("hook:X.pre")
			if r, ok := obs.Record(sprint.Work, "p2"); !ok || r.Exists && r.Fields["kind"].Value != "work" {
				return refuse("", CodeXGuard, RefusalDetail{})
			}
			return nil
		},
		Derive: func(st *State, in []Intent, obs *Before) ([]tset.Entry, []NoteReq, *Refusal) {
			tr.add("hook:derive")
			r, _ := obs.Record(sprint.Work, in[0].Card)
			entry := tset.Entry{Kind: "move", Table: sprint.Work, From: r.Place.Row + ":" + r.Place.Col,
				IDs: []string{in[0].Card}, Revs: []tset.Decimal{r.Revision}, Set: map[string]string{"open": "0"},
				About: []string{in[0].Card}}
			return []tset.Entry{entry}, nil, nil
		},
		JDecide: func(st *State, in []NoteReq, obs *Before) ([]tset.Note, JPlan, *Refusal) {
			tr.add("hook:J")
			note := tset.Note{Line: tset.NoteLine{Kind: "note", Meta: json.RawMessage(`{"type":"` + in[0].Type + `"}`)}, About: in[0].Subjects}
			return []tset.Note{note}, JPlan{Notes: []JNote{{Index: 0, Req: in[0]}}}, nil
		},
		XCmds: func(st *State, tp TablePlan, lp LogPlan) []Cmd {
			tr.add("hook:X.cmds")
			return []Cmd{Command("HSET", st.Prefix+"sprint:tick@0", kindHash, "cur", string(lp.LastSeq))}
		},
		JCmds: func(st *State, jp JPlan, lp LogPlan) []Cmd {
			tr.add("hook:J.cmds")
			seq := lp.NoteSeqs[jp.Notes[0].Index]
			return []Cmd{Command("HSET", st.Prefix+"sprint:jopen:p1@0", kindHash, jp.Notes[0].Req.Type+"|c", "n"+string(seq))}
		},
	}
}

// tracedLease is a lease part that records its calls.
func tracedLease(tr *tracer) Part {
	return PartFuncs{
		PreFunc: func(st *State, req *Request, obs *Before) (any, *Refusal) {
			tr.add("hook:lease.pre")
			return map[string]string{"owner": req.Lease.Owner, "held": "self"}, nil
		},
		CmdsFunc: func(st *State, plan any, lp LogPlan) ([]Cmd, *Refusal) {
			tr.add("hook:lease.cmds")
			return []Cmd{Command("HSET", st.Prefix+"sprint:lease", kindHash, "owner", plan.(map[string]string)["owner"])}, nil
		},
	}
}

// TestComposePhaseOrder: a traced step on the twin enters the phases of 1.0
// in their one order, calls each phase's function inside its phase, and the
// applied step shows each phase's work: the derived entry and J's note are
// logged, X.plan and J wrote their keys with the seqs the log assigned, and
// the lease part's decision is in the reply.
func TestComposePhaseOrder(t *testing.T) {
	t.Parallel()
	tr := &tracer{}
	tw, m, log := newTestTwin(t, tracedPhases(tr))
	if err := tw.parts.Register(PartLease, tracedLease(tr)); err != nil {
		t.Fatal(err)
	}
	mustStep(t, tw, seedRequest())
	tr.got = nil
	tw.trace = tr.add

	req := moveRequest("waiting", "ready")
	req.Body.Intents = []Intent{{Kind: "needmet", Card: "p2", Need: "n0", Waiters: []string{"p2"}}}
	req.Body.Notes = []NoteReq{{Op: "open", Type: "blocked", Cause: "c", Subjects: []string{"p1"}}}
	req.Lease = &LeasePart{Owner: "token-1", HoldMS: 5000}
	reply := mustStep(t, tw, req)

	want := []string{PhaseOpen, PhaseBefore, "hook:before", PhaseXPre, "hook:X.pre", PhaseDerive, "hook:derive",
		PhaseJ, "hook:J", PhaseParts, "hook:lease.pre", PhasePlan, PhaseLog, PhaseXPlan, "hook:X.cmds",
		"hook:J.cmds", "hook:lease.cmds", PhasePrepare, PhaseCommit}
	if got := tr.list(); !sameStrings(got, want) {
		t.Fatalf("trace\n got %v\nwant %v", got, want)
	}
	var phases []string
	for _, s := range tr.list() {
		if len(s) < 5 || s[:5] != "hook:" {
			phases = append(phases, s)
		}
	}
	if !sameStrings(phases, PhaseOrder) {
		t.Fatalf("phases %v, want 1.0's %v", phases, PhaseOrder)
	}

	// The seed wrote lines 1 (rows) and 2 (create); this step 3 (the move), 4
	// (the derived field change) and 5 (J's note).
	if reply.Reply.FirstSeq != "3" || reply.Reply.LastSeq != "5" || reply.Reply.Lines != 3 {
		t.Fatalf("reply seqs %s..%s lines %d, want 3..5 and 3", reply.Reply.FirstSeq, reply.Reply.LastSeq, reply.Reply.Lines)
	}
	keys := tw.SprintKeys()
	if keys[testPrefix+"sprint:tick@0"].Hash["cur"] != "5" || keys[testPrefix+"sprint:jopen:p1@0"].Hash["blocked|c"] != "n5" ||
		keys[testPrefix+"sprint:lease"].Hash["owner"] != "token-1" {
		t.Fatalf("sprint keys after the step: %+v", keys)
	}
	if string(reply.Parts[PartLease]) != `{"held":"self","owner":"token-1"}` {
		t.Fatalf("lease part's reply %s", reply.Parts[PartLease])
	}
	snap, err := m.Snapshot(testPrefix)
	if err != nil {
		t.Fatal(err)
	}
	work := snap.Epochs["0"].Tables[sprint.Work]
	if work.Records["p1"].Column != "ready" || work.Records["p2"].Fields["open"] != "0" {
		t.Fatalf("tables after the step: p1 %+v, p2 %+v", work.Records["p1"], work.Records["p2"])
	}
	if lines := log.Lines(testPrefix, "0"); len(lines) != 5 {
		t.Fatalf("log has %d lines, want 5", len(lines))
	}
	if h := log.History(testPrefix, "0", "p1"); len(h) != 3 || h[2] != "5" {
		t.Fatalf("p1's history %v, want its create, move and note", h)
	}
}

// TestFenceBypassesSprintPhases (errata E5): a fence writes only its receipt
// and no line, and runs no sprint phase; its replay returns "fenced"
// unchanged; a fence with any sprint field is REQUEST and runs nothing; a
// lease-only step with an op is ordinary, status "ok".
func TestFenceBypassesSprintPhases(t *testing.T) {
	t.Parallel()
	tr := &tracer{}
	tw, m, log := newTestTwin(t, tracedPhases(tr))
	if err := tw.parts.Register(PartLease, tracedLease(tr)); err != nil {
		t.Fatal(err)
	}
	mustStep(t, tw, seedRequest())
	tw.trace = tr.add
	tr.got = nil
	before := log.Lines(testPrefix, "0")

	fence := &Request{Epoch: "0", Fence: true, Body: Body{Op: &Op{ID: "op-1/p2", Intent: "add a b"}}}
	got := mustStep(t, tw, fence)
	if got.Reply.Status != "fenced" || got.Reply.FirstSeq != "0" || got.Reply.LastSeq != "0" || got.Reply.Lines != 0 {
		t.Fatalf("fence reply %+v", got.Reply)
	}
	if trace := tr.list(); !sameStrings(trace, []string{PhaseOpen, PhasePrepare, PhaseCommit}) {
		t.Fatalf("fence ran %v; want open, prepare, commit and no sprint phase", trace)
	}
	if after := log.Lines(testPrefix, "0"); len(after) != len(before) {
		t.Fatalf("fence wrote %d lines", len(after)-len(before))
	}
	snap, err := m.Snapshot(testPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if r := snap.Receipts["0"]["op-1/p2"]; r.Status != "fenced" {
		t.Fatalf("fence receipt %+v", r)
	}
	again := mustStep(t, tw, fence)
	if !again.Reply.Replay || again.Reply.Status != "fenced" {
		t.Fatalf("fence replay %+v", again.Reply)
	}

	tr.got = nil
	withField := &Request{Epoch: "0", Fence: true, Body: Body{Op: &Op{ID: "op-2/p1", Intent: "x"}}, Lease: &LeasePart{Owner: "tok"}}
	img := image(t, tw, m, log)
	_, err = Step(context.Background(), tw, withField)
	var ref *Refusal
	if !errors.As(err, &ref) || ref.Code != CodeRequest {
		t.Fatalf("fence with a sprint field: %v, want REQUEST", err)
	}
	if len(tr.list()) != 0 || string(image(t, tw, m, log)) != string(img) {
		t.Fatalf("a refused fence ran %v or changed the twin", tr.list())
	}

	leaseOnly := &Request{Epoch: "0", Body: Body{Op: &Op{ID: "op-3/p1", Intent: "lease"}}, Lease: &LeasePart{Owner: "tok"}}
	ok := mustStep(t, tw, leaseOnly)
	if ok.Reply.Status != "ok" || ok.Reply.Replay {
		t.Fatalf("lease-only step with an op: %+v", ok.Reply)
	}
}

// TestComposeNoWriteBeforeCommit: every refusal code from every phase leaves
// the whole-store image equal (item E6's DUMP comparison). It needs the store.
func TestComposeNoWriteBeforeCommit(t *testing.T) {
	t.Parallel()
	t.Skip("G0: needs the store (Layer 1 revision 4 pinned, Layer 2 accepted again) and item E6's whole-store image")
}

// TestComposeFCALLOnly: the suite writes only through FCALL ns_sprint_step,
// teardown excepted (item E7's command monitor). It needs the store.
func TestComposeFCALLOnly(t *testing.T) {
	t.Parallel()
	t.Skip("G0: needs the store (Layer 1 revision 4 pinned, Layer 2 accepted again) and item E7's command monitor")
}
