package sprintfn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
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

// orderPart is a part that records its pre and its commands in the tracer and
// appends its name to the list key t:sprint:order, so the order the commands
// were committed in shows in the list.
func orderPart(tr *tracer, name string) Part {
	return PartFuncs{
		PreFunc: func(*State, *Request, *Before) (any, *Refusal) {
			tr.add("pre:" + name)
			return map[string]string{"part": name}, nil
		},
		CmdsFunc: func(st *State, plan any, lp LogPlan) ([]Cmd, *Refusal) {
			tr.add("cmds:" + name)
			return []Cmd{Command("RPUSH", st.Prefix+"sprint:order", kindList, name)}, nil
		},
	}
}

// TestComposePartsRunInFixedOrder: parts in one step run in 1.0's order
// (lease, pop, ingest, beat, clock, sprint), whatever order they were
// registered in: their pre calls in that order, then their commands, and the
// committed list is X's, then J's, then each part's (A1 across parts), in that
// order.
func TestComposePartsRunInFixedOrder(t *testing.T) {
	t.Parallel()
	tr := &tracer{}
	phases := Phases{
		XPre:    func(*State, *Request, *Before) *Refusal { return nil },
		JDecide: func(*State, []NoteReq, *Before) ([]tset.Note, JPlan, *Refusal) { return nil, JPlan{}, nil },
		XCmds: func(st *State, tp TablePlan, lp LogPlan) []Cmd {
			tr.add("cmds:X")
			return []Cmd{Command("RPUSH", st.Prefix+"sprint:order", kindList, "X")}
		},
		JCmds: func(st *State, jp JPlan, lp LogPlan) []Cmd {
			tr.add("cmds:J")
			return []Cmd{Command("RPUSH", st.Prefix+"sprint:order", kindList, "J")}
		},
	}
	tw, _, _ := newTestTwin(t, phases)
	// Registered in the reverse of 1.0's order, so a twin that ran the parts in
	// the order it found them would be caught.
	for i := len(PartOrder) - 1; i >= 0; i-- {
		if err := tw.parts.Register(PartOrder[i], orderPart(tr, PartOrder[i])); err != nil {
			t.Fatal(err)
		}
	}
	req := seedRequest()
	req.Body.Notes = []NoteReq{{Op: "open", Type: "blocked", Cause: "c", Subjects: []string{"p1"}}}
	req.Sprint = &SprintPart{Coordinator: "c1"}
	req.Clock = &ClockPart{Verb: ClockInit}
	req.Beat = &BeatPart{Members: []BeatMember{{Member: "m1"}}}
	req.Ingest = &IngestPart{From: "0", To: "0"}
	req.Pop = &PopPart{Limit: 10}
	req.Lease = &LeasePart{Owner: "tok", HoldMS: 5000}
	reply := mustStep(t, tw, req)

	want := []string{"pre:lease", "pre:pop", "pre:ingest", "pre:beat", "pre:clock", "pre:sprint",
		"cmds:X", "cmds:J", "cmds:lease", "cmds:pop", "cmds:ingest", "cmds:beat", "cmds:clock", "cmds:sprint"}
	if got := tr.list(); !sameStrings(got, want) {
		t.Fatalf("calls\n got %v\nwant %v", got, want)
	}
	order := tw.SprintKeys()[testPrefix+"sprint:order"].List
	if !sameStrings(order, []string{"X", "J", "lease", "pop", "ingest", "beat", "clock", "sprint"}) {
		t.Fatalf("committed order %v; want X, J, then the parts in 1.0's order", order)
	}
	for _, name := range PartOrder {
		if string(reply.Parts[name]) != `{"part":"`+name+`"}` {
			t.Fatalf("part %s's reply is %s", name, reply.Parts[name])
		}
	}
}

// noWriteCase is one refusal of TestComposeNoWriteBeforeCommit: what it sets
// on a writing twin, the request it sends, and the code and phase it expects.
type noWriteCase struct {
	name, phase, code string
	set               func(tw *Twin, log *LogStub)
	req               func() *Request
}

// xCmds sets X's commands.
func xCmds(cmds ...Cmd) func(tw *Twin, log *LogStub) {
	return func(tw *Twin, _ *LogStub) {
		tw.phases.XCmds = func(*State, TablePlan, LogPlan) []Cmd { return cmds }
	}
}

// beatPart registers a beat part with the functions given.
func beatPart(pre func(*State, *Request, *Before) (any, *Refusal), cmds func(*State, any, LogPlan) ([]Cmd, *Refusal)) func(tw *Twin, log *LogStub) {
	return func(tw *Twin, _ *LogStub) {
		if err := tw.parts.Register(PartBeat, PartFuncs{PreFunc: pre, CmdsFunc: cmds}); err != nil {
			panic(err)
		}
	}
}

// writersWith is a move of p1 with every writer active, changed by edit.
func writersWith(edit func(*Request)) func() *Request {
	return func() *Request {
		req := withWriters(moveRequest("waiting", "ready"))
		if edit != nil {
			edit(req)
		}
		return req
	}
}

func noWriteCases() []noWriteCase {
	none := func(*Twin, *LogStub) {}
	passPre := func(*State, *Request, *Before) (any, *Refusal) { return "plan", nil }
	noCmds := func(*State, any, LogPlan) ([]Cmd, *Refusal) { return nil, nil }
	withBeat := writersWith(func(r *Request) { r.Beat = &BeatPart{Members: []BeatMember{{Member: "m1"}}} })
	many := make([]Cmd, tset.MaxPlannedCommands+1)
	for i := range many {
		many[i] = Command("HSET", testPrefix+"sprint:k"+strconv.Itoa(i), kindHash, "f", "v")
	}
	argv := []string{"f", "v"}
	for len(argv)+2 <= maxArgv {
		argv = append(argv, "f", "v")
	}
	pieces := make([]string, maxPieces+1)
	for i := range pieces {
		pieces[i] = "f" + strconv.Itoa(i)
	}
	big := strings.Repeat("v", tset.MaxPlannedArgvBytes)
	return []noWriteCase{
		// open
		{"a member entry without about", PhaseOpen, CodeRequest, none, writersWith(func(r *Request) { r.Body.Entries[0].About = nil })},
		{"an epoch ahead", PhaseOpen, CodeEpochAhead, none, writersWith(func(r *Request) { r.Epoch = "1" })},
		// the pre stage
		{"too many records to read", PhaseBefore, CodeLimit, func(tw *Twin, _ *LogStub) {
			ids := make([]string, beforeRecordsMax+1)
			for i := range ids {
				ids[i] = "q" + strconv.Itoa(i)
			}
			tw.phases.Before = func(*State, *Request) []BeforeAsk { return []BeforeAsk{{Table: sprint.Work, IDs: ids}} }
		}, writersWith(nil)},
		{"X.pre", PhaseXPre, CodeXGuard, func(tw *Twin, _ *LogStub) {
			tw.phases.XPre = func(*State, *Request, *Before) *Refusal { return refuse("", CodeXGuard, RefusalDetail{}) }
		}, writersWith(nil)},
		{"derive", PhaseDerive, CodeCounter, func(tw *Twin, _ *LogStub) {
			tw.phases.Derive = func(*State, []Intent, *Before) ([]tset.Entry, []NoteReq, *Refusal) {
				return nil, nil, refuse("", CodeCounter, RefusalDetail{})
			}
		}, writersWith(nil)},
		{"J", PhaseJ, CodeDropping, func(tw *Twin, _ *LogStub) {
			tw.phases.JDecide = func(*State, []NoteReq, *Before) ([]tset.Note, JPlan, *Refusal) {
				return nil, JPlan{}, refuse("", CodeDropping, RefusalDetail{})
			}
		}, writersWith(nil)},
		{"a part no file registered", PhaseParts, CodeConfig, none, writersWith(func(r *Request) { r.Pop = &PopPart{Limit: 1} })},
		{"a part's pre", PhaseParts, CodeStaleGen, beatPart(func(*State, *Request, *Before) (any, *Refusal) {
			return nil, refuse("", CodeStaleGen, RefusalDetail{})
		}, noCmds), withBeat},
		// plan
		{"Layer 1: a move from the wrong cell", PhasePlan, "PLACE", none, writersWith(func(r *Request) { r.Body.Entries[0].From = "s1:ready" })},
		{"Layer 1: a create of a card that exists", PhasePlan, "EXISTS", none, writersWith(func(r *Request) {
			r.Body.Entries[0] = tset.Entry{Kind: "create", Table: sprint.Work, To: "s1:waiting", IDs: []string{"p1"}, Scores: []string{"9"}, About: []string{"p1"}}
		})},
		{"Layer 1: a guard at a past revision", PhasePlan, "REVISION", none, writersWith(func(r *Request) {
			r.Body.Entries[0] = tset.Entry{Kind: "guard", Table: sprint.Work, From: "s1:waiting", IDs: []string{"p1"}, Revs: []tset.Decimal{"7"}}
		})},
		{"a derived entry without about", PhasePlan, CodeRequest, func(tw *Twin, _ *LogStub) {
			tw.phases.Derive = func(*State, []Intent, *Before) ([]tset.Entry, []NoteReq, *Refusal) {
				return []tset.Entry{{Kind: "move", Table: sprint.Work, From: "s1:waiting", To: "s1:ready", IDs: []string{"p2"}}}, nil, nil
			}
		}, writersWith(nil)},
		{"too many notes", PhasePlan, CodeLimit, func(tw *Twin, _ *LogStub) {
			tw.phases.JDecide = func(*State, []NoteReq, *Before) ([]tset.Note, JPlan, *Refusal) {
				notes := make([]tset.Note, tset.MaxNotes+1)
				for i := range notes {
					notes[i] = tset.Note{Line: tset.NoteLine{Kind: "note", Meta: json.RawMessage(`{}`)}, About: []string{}}
				}
				return notes, JPlan{}, nil
			}
		}, writersWith(nil)},
		{"a note that is not a note line", PhasePlan, CodeRequest, func(tw *Twin, _ *LogStub) {
			tw.phases.JDecide = func(*State, []NoteReq, *Before) ([]tset.Note, JPlan, *Refusal) {
				return []tset.Note{{Line: tset.NoteLine{Kind: "move", Meta: json.RawMessage(`{}`)}, About: []string{"p1"}}}, JPlan{}, nil
			}
		}, writersWith(nil)},
		// log
		{"the log's LIMIT", PhaseLog, CodeLimit, func(tw *Twin, log *LogStub) {
			tw.log = &refusingLog{LogStub: log, ref: refuse(PhaseLog, CodeLimit, RefusalDetail{})}
		}, writersWith(nil)},
		{"the log's OVERFLOW", PhaseLog, "OVERFLOW", func(tw *Twin, log *LogStub) {
			tw.log = &refusingLog{LogStub: log, ref: refuse(PhaseLog, "OVERFLOW", RefusalDetail{})}
		}, writersWith(nil)},
		// X.plan
		{"no X commands", PhaseXPlan, CodeConfig, func(tw *Twin, _ *LogStub) { tw.phases.XCmds = nil }, writersWith(nil)},
		{"no J commands", PhaseXPlan, CodeConfig, func(tw *Twin, _ *LogStub) { tw.phases.JCmds = nil }, writersWith(nil)},
		{"a part's commands", PhaseXPlan, CodeLimit, beatPart(passPre, func(*State, any, LogPlan) ([]Cmd, *Refusal) {
			return nil, refuse("", CodeLimit, RefusalDetail{})
		}), withBeat},
		{"a part's decision that does not encode", PhaseXPlan, CodeRequest, beatPart(func(*State, *Request, *Before) (any, *Refusal) {
			return func() {}, nil
		}, noCmds), withBeat},
		// prepare
		{"a key of the log", PhasePrepare, CodeRequest, xCmds(Command("HSET", testPrefix+"sprint:log@0", kindHash, "f", "v")), writersWith(nil)},
		{"a command outside the registry", PhasePrepare, CodeRequest, xCmds(Command("DEL", testPrefix+"sprint:k", kindHash)), writersWith(nil)},
		{"an access that is not the key", PhasePrepare, CodeRequest, xCmds(Cmd{Argv: []string{"HSET", testPrefix + "sprint:k", "f", "v"},
			Access: []Access{{Key: testPrefix + "sprint:other", Kind: kindHash, Mode: "write"}}}), writersWith(nil)},
		{"a key of another type", PhasePrepare, CodeWrongType, xCmds(Command("ZADD", testPrefix+"sprint:lease", kindZSet, "1", "m")), writersWith(nil)},
		{"too many commands", PhasePrepare, CodeLimit, xCmds(many...), writersWith(nil)},
		{"too many argv bytes", PhasePrepare, CodeLimit, xCmds(Command("HSET", testPrefix+"sprint:big", kindHash, "f", big)), writersWith(nil)},
		{"too many argv values", PhasePrepare, CodeLimit, xCmds(Command("HSET", testPrefix+"sprint:k", kindHash, argv...)), writersWith(nil)},
		{"too many pieces", PhasePrepare, CodeLimit, xCmds(Command("HDEL", testPrefix+"sprint:k", kindHash, pieces...)), writersWith(nil)},
	}
}

// TestComposeNoWriteBeforeCommit (E6, on the twin): every refusal code of every
// phase, open through prepare, with every writer active (X, derive, J, a lease
// part and a note request), leaves the whole image equal: the Mem's state, the
// sprint's keys and the log, compared as E6's image of a store is compared
// (testredis.Diff over one entry a key) and as bytes. No refusal reaches
// commit, which does not refuse: Mem.Commit refuses only a plan used twice or
// a state moved since the plan, and the twin's lock rules both out. After
// each refusal the twin still stands for the store. STALE, which needs a Mem
// at a later epoch, is the last case. The store's half is
// TestComposeNoWriteBeforeCommitOnTheStore, skipped until G0.
func TestComposeNoWriteBeforeCommit(t *testing.T) {
	t.Parallel()
	phases := map[string]bool{}
	for _, c := range noWriteCases() {
		phases[c.phase] = true
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tw, m, log, tr := writingTwin(t)
			c.set(tw, log)
			before := capture(t, tw, m, log)
			res, err := Step(context.Background(), tw, c.req())
			if err != nil || res.Err != nil || res.Refusal == nil || res.Refusal.Code != c.code || res.Refusal.Phase != c.phase {
				t.Fatalf("result %+v, err %v; want %s from %s", res, err, c.code, c.phase)
			}
			unchanged(t, c.name, before, capture(t, tw, m, log))
			if ranAny(tr.list(), []string{PhaseCommit}) || tw.broken != nil {
				t.Fatalf("a refusal in %s reached commit (%v) or broke the twin (%v)", c.phase, tr.list(), tw.broken)
			}
		})
	}
	for _, p := range PhaseOrder {
		if p != PhaseCommit && !phases[p] {
			t.Errorf("no refusal of phase %s is tested", p)
		}
	}

	t.Run("an epoch behind", func(t *testing.T) {
		t.Parallel()
		m := newTestMem(t)
		// Fault injection: the Mem's active epoch set, not stepped to.
		if err := m.SetActiveEpoch(testPrefix, "1"); err != nil {
			t.Fatal(err)
		}
		log := NewLogStub()
		tw := NewTwin(m, log, testNames)
		tw.parts, tw.phases = NewPartRegistry(), passX()
		before := capture(t, tw, m, log)
		res, err := Step(context.Background(), tw, seedRequest())
		if err != nil || res.Refusal == nil || res.Refusal.Code != CodeStale || res.Refusal.Phase != PhaseOpen {
			t.Fatalf("result %+v, err %v; want STALE from open", res, err)
		}
		unchanged(t, "a step an epoch behind", before, capture(t, tw, m, log))
	})
}

// TestComposeNoWriteBeforeCommitOnTheStore: the same, on the store: every code
// from every phase leaves the whole-store image (testredis.Image) equal.
func TestComposeNoWriteBeforeCommitOnTheStore(t *testing.T) {
	t.Parallel()
	t.Skip("G0: needs the store (Layer 1 revision 4 pinned, Layer 2 accepted again) and the sprint profile loaded in a container")
}

// TestComposeFCALLOnlyOnTheStore: the suite, on the store, writes only through
// FCALL ns_sprint_step, teardown excepted (E7), with testredis.OnlyFCALL on the
// client the code under test is given.
func TestComposeFCALLOnlyOnTheStore(t *testing.T) {
	t.Parallel()
	t.Skip("G0: needs the store (Layer 1 revision 4 pinned, Layer 2 accepted again) and the sprint profile loaded in a container")
}

// answerHook stands in for the store under a client that NewRedis owns: it
// answers every FCALL and FCALL_RO of a pipeline itself and never calls on
// (so nothing is sent), refuses every single command, and refuses to dial, so
// no connection is ever made. It records each command it is given.
type answerHook struct {
	mu        sync.Mutex
	pipelines int
	dials     int
	cmds      [][]any
}

func (h *answerHook) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.dials++
		return nil, errors.New("a unit test has no store to dial")
	}
}

func (h *answerHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.cmds = append(h.cmds, cmd.Args())
		err := errors.New("the answer hook takes pipelines only")
		cmd.SetErr(err)
		return err
	}
}

func (h *answerHook) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(_ context.Context, cmds []redis.Cmder) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.pipelines++
		for _, cmd := range cmds {
			args := cmd.Args()
			h.cmds = append(h.cmds, args)
			reply := okEnvelope("1", "1", 1, `{}`)
			if name, _ := args[0].(string); name == "fcall_ro" {
				reply = countReply
				if plan, _ := args[len(args)-1].(string); strings.Contains(plan, `"mode":"page"`) {
					reply = pageReply
				}
			}
			cmd.(valueSetter).SetVal(reply)
			cmd.SetErr(nil)
		}
		return nil
	}
}

// recordingTB is a test that keeps the failures reported to it instead of
// failing, so a test can show that a hook reports one.
type recordingTB struct {
	testing.TB
	mu     sync.Mutex
	errors []string
}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

// fcallSuite is every kind of item the write path sends, each with whether it
// writes: steps that apply, a replay, a fence, steps refused at open and at
// plan, a read and a page.
func fcallSuite() ([]Item, []bool) {
	op := withWriters(moveRequest("waiting", "ready"))
	op.Body.Op = &Op{ID: "op-1/p1", Intent: "move p1"}
	count := &ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{{Kind: "count", Table: sprint.Work, Cells: []string{"s1:waiting"}}}}
	ahead := moveRequest("ready", "working")
	ahead.Epoch = "1"
	items := []Item{
		{Step: op}, {Read: count}, {Page: pagePlan()}, {Step: op},
		{Step: &Request{Epoch: "0", Fence: true, Body: Body{Op: &Op{ID: "op-2/p2", Intent: "add a b"}}}},
		{Step: moveRequest("waiting", "working")}, {Step: ahead},
		{Step: withWriters(moveRequest("ready", "working"))},
	}
	writes := []bool{true, false, false, false, true, false, false, true}
	return items, writes
}

// TestComposeFCALLOnly (E7, on the twin and on the store's client): the write
// path writes only through its one write call. On the twin, over a suite of
// every kind of item, the state changes on a step that applied and on nothing
// else: not a read, a page, a replay or a refusal. On the store's client, where
// a go-redis client exists, the client NewRedis owns carries
// testredis.OnlyFCALL, which fails the test on any write command other than
// FCALL, and under it answerHook, which answers in the store's place and never
// dials: the same suite, in one pipeline, is one flush of exactly one FCALL
// ns_sprint_step a step and one FCALL_RO ns_sprint_read a read or page, each
// with no key and the version first. The hook is shown to be live on a client
// built the same way: a write outside FCALL is reported and not sent. The
// store's own half is TestComposeFCALLOnlyOnTheStore, skipped until G0.
func TestComposeFCALLOnly(t *testing.T) {
	t.Parallel()
	items, writes := fcallSuite()

	t.Run("the twin", func(t *testing.T) {
		t.Parallel()
		tw, m, log, _ := writingTwin(t)
		for i, it := range items {
			before := capture(t, tw, m, log)
			results, err := tw.Pipeline(context.Background(), []Item{it})
			if err != nil || len(results) != 1 || results[0].Err != nil {
				t.Fatalf("item %d: %+v, %v", i, results, err)
			}
			after := capture(t, tw, m, log)
			changed := len(testredis.Diff(before.image, after.image)) != 0 || string(before.bytes) != string(after.bytes)
			if changed != writes[i] {
				t.Fatalf("item %d (%s): changed the twin %v, want %v: %v", i, resultKind(results[0]), changed, writes[i],
					testredis.Diff(before.image, after.image))
			}
			if writes[i] && results[0].Step == nil {
				t.Fatalf("item %d wrote and is not a step that applied: %s", i, resultKind(results[0]))
			}
		}
	})

	t.Run("the store's client", func(t *testing.T) {
		t.Parallel()
		r, err := NewRedis("sprintfn-unit-test", "", "", testNames, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Close() })
		store := &answerHook{}
		r.conn.AddHook(testredis.OnlyFCALL(t))
		r.conn.AddHook(store)
		results, err := r.Pipeline(context.Background(), items)
		if err != nil || len(results) != len(items) {
			t.Fatalf("%d results, %v", len(results), err)
		}
		if store.pipelines != 1 || store.dials != 0 || len(store.cmds) != len(items) {
			t.Fatalf("flushes %d, dials %d, commands %d; want 1, 0, %d", store.pipelines, store.dials, len(store.cmds), len(items))
		}
		for i, args := range store.cmds {
			name, fn := "fcall", fnStep
			if items[i].Step == nil {
				name, fn = "fcall_ro", fnRead
			}
			if len(args) < 4 || args[0] != name || args[1] != fn || fmt.Sprint(args[2]) != "0" || args[3] != Version {
				t.Fatalf("command %d is %v; want %s %s 0 %s ...", i, args[:min(4, len(args))], name, fn, Version)
			}
		}

		live, err := NewRedis("sprintfn-unit-test", "", "", testNames, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = live.Close() })
		rec, sink := &recordingTB{TB: t}, &answerHook{}
		live.conn.AddHook(testredis.OnlyFCALL(rec))
		live.conn.AddHook(sink)
		if err := live.conn.Do(context.Background(), "HSET", testPrefix+"sprint:k", "f", "v").Err(); err == nil {
			t.Fatal("a HSET through OnlyFCALL was not refused")
		}
		pipe := live.conn.Pipeline()
		pipe.Do(context.Background(), "FCALL", fnStep, 0, Version, "{}", "{}")
		pipe.Do(context.Background(), "DEL", testPrefix+"sprint:k")
		if _, err := pipe.Exec(context.Background()); err == nil {
			t.Fatal("a pipeline holding a DEL was not refused")
		}
		if len(rec.errors) != 2 || len(sink.cmds) != 0 || sink.dials != 0 {
			t.Fatalf("OnlyFCALL reported %v and let %d commands through; want both reported and none sent", rec.errors, len(sink.cmds))
		}
	})
}
