package store

// The differential test's harness: seeded random sequences of verbs, ticks
// and outside facts against the engine on the in-memory store, each applied
// to the reference model (internal/sprint/refmodel) with the choices the
// engine made, and Abstract(engine) compared with the model's state after
// every step. A step one refuses and the other acts on is a difference too.

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
)

// dAction is one step of a sequence: a verb, a tick or an outside fact, with
// every argument concrete, so a sequence replays exactly.
type dAction struct {
	Kind          string
	Stream        string
	IDs           []string
	Needs         []string
	Sentinel      bool
	Before, After string
	Member        string
	Reader        string
	Card          string
	Gen           int
	OK            bool
	Batch         int
	Fact          string // green, conflict, cross, red, rejected
	Other         string // the other card of a cross fact
	Did           string
	Op            string // a fleet verb: up, down, level
	Type          string // an ack's judgment type, as the model names it
	Subject       string // an ack's subject
	Run           int    // a ci run
	// CutAt cuts the step before its CutAt-th table write (1, its first:
	// nothing of it applied; repaired past the grace); Leave also compares
	// the state while it is cut, before the repair.
	CutAt int
	Leave bool
}

func (a dAction) id() string {
	if len(a.IDs) > 0 {
		return a.IDs[0]
	}
	return ""
}

// String is the action as the verb line that makes it, and how it is cut.
func (a dAction) String() string {
	switch {
	case a.CutAt > 0 && a.Leave:
		return fmt.Sprintf("%s   [cut before its write %d, looked at, then repair]", a.verb(), a.CutAt)
	case a.CutAt > 0:
		return fmt.Sprintf("%s   [cut before its write %d, then repair]", a.verb(), a.CutAt)
	}
	return a.verb()
}

func (a dAction) verb() string {
	ids := strings.Join(a.IDs, " ")
	switch a.Kind {
	case "add":
		s := "add --stream " + a.Stream
		if a.Sentinel {
			s += " --sentinel"
		}
		s += " " + ids
		if len(a.Needs) > 0 {
			s += " --needs " + strings.Join(a.Needs, ",")
		}
		if a.Before != "" {
			s += " --before " + a.Before
		}
		if a.After != "" {
			s += " --after " + a.After
		}
		return s
	case "tick":
		return "tick"
	case "start", "stop":
		return a.Kind
	case "take":
		return fmt.Sprintf("take --as %s %s@%d", a.Member, a.Card, a.Gen)
	case "finish":
		v := "--ok"
		if !a.OK {
			v = "--failed"
		}
		return fmt.Sprintf("finish --as %s %s@%d %s", a.Member, a.Card, a.Gen, v)
	case "begin":
		return fmt.Sprintf("read --as %s --begin %s", a.Reader, a.Card)
	case "read":
		v := "ok"
		if !a.OK {
			v = "broken"
		}
		return fmt.Sprintf("read --as %s %s %s", a.Reader, a.Card, v)
	case "merge":
		s := fmt.Sprintf("merge --stream %s --batch %d", a.Stream, a.Batch)
		switch a.Fact {
		case "conflict":
			s += " --conflict " + a.id()
		case "cross":
			s += " --cross " + a.id() + "=" + a.Other
		case "red":
			s += " --red"
		case "rejected":
			s += " --rejected"
		}
		return s
	case "resume":
		if a.Did == "" {
			return "resume --stream " + a.Stream
		}
		return "resume --stream " + a.Stream + " --did " + a.Did
	case "fleet":
		if a.Op == "level" {
			return "fleet level"
		}
		return "fleet " + a.Op + " " + a.Member
	case "ci":
		v := "--green"
		if !a.OK {
			v = "--red"
		}
		return fmt.Sprintf("ci %s %s --run %d", ids, v, a.Run)
	case "ack":
		return fmt.Sprintf("ack <%s on %s> --reason looked", a.Type, a.Subject)
	case "another":
		return "ask --another " + ids
	case "clear":
		return "clear"
	case "rework":
		return "rework " + ids + " --fix f"
	case "drop", "return":
		return a.Kind + " " + ids + " --reason r"
	case "release":
		return "release " + ids + " --reason r"
	}
	return a.Kind + " " + ids
}

// dFinding is one difference between the engine and the model, found after
// an action.
type dFinding struct {
	Seq    []dAction // the sequence, the action last
	Kind   string    // state, refusal, choice
	Diffs  []refmodel.Difference
	Detail string
}

// Sig is the finding without its ids: the action's verb, what differs and
// how, for grouping.
func (f dFinding) Sig() string {
	act := f.Seq[len(f.Seq)-1].Kind
	switch f.Kind {
	case "state":
		var sigs []string
		seen := map[string]bool{}
		for _, d := range f.Diffs {
			s := d.Sig()
			switch d.Field {
			case "needs", "waived", "pair", "member", "score", "gen", "head", "attempt", "need", "primary", "reader":
			default:
				s += "=" + d.Engine + "/" + d.Model
			}
			if !seen[s] {
				seen[s] = true
				sigs = append(sigs, s)
			}
		}
		sort.Strings(sigs)
		return act + ": " + strings.Join(sigs, " ")
	case "refusal":
		sig := act + ": refusal " + f.Detail[:strings.IndexByte(f.Detail+":", ':')]
		seen := map[string]bool{}
		for _, d := range f.Diffs {
			if !seen[d.Sig()] {
				seen[d.Sig()] = true
				sig += " " + d.Sig()
			}
		}
		return sig
	}
	return act + ": " + f.Kind
}

func (f dFinding) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n  the sequence so far (as verbs):\n", f.Sig())
	for i, a := range f.Seq {
		fmt.Fprintf(&b, "    %2d. nova-sprint %s\n", i+1, a)
	}
	fmt.Fprintf(&b, "  the action: nova-sprint %s\n", f.Seq[len(f.Seq)-1])
	if f.Detail != "" {
		fmt.Fprintf(&b, "  %s\n", f.Detail)
	}
	for _, d := range f.Diffs {
		fmt.Fprintf(&b, "  field %s %s %s: engine %q, model %q\n", d.Table, d.ID, d.Field, d.Engine, d.Model)
	}
	return b.String()
}

const dCoordinator = "tester"

var dReaders = []string{"r1", "r2", "r3"}
var dMembers = []string{"m1", "m2", "m3"}
var dStreams = []string{"s1", "s2", "s3"}

type dHarness struct {
	t     testing.TB
	ctx   context.Context
	m     *Mem
	st    *Store
	mu    sync.Mutex
	now   time.Time
	model refmodel.State
	seen  map[string]map[string]bool // logical table -> card ids to read, placed or not
	epoch uint64
	seq   []dAction
	// resync, when set, takes the engine's state as the model's after a
	// difference, so one sequence can find more than one.
	resync   bool
	findings []dFinding
	// preNote and preSubjects are the notification an ack names and every
	// subject it is open on, read before the ack.
	preNote     string
	preSubjects []string
	preNeeds    []string
	// Acted counts, by action, the steps the engine carried out (its state
	// changed), and Cuts the steps cut short.
	Acted, Tried map[string]int
	Cuts         int
	// cutTable is the table the last action was cut before ("" when it was
	// not cut).
	cutTable string
	mid      *refmodel.State // the state while cut, before repair (Leave)
}

func newDHarness(t testing.TB) *dHarness {
	h := &dHarness{t: t, ctx: context.Background(), m: NewMem(), now: t0, resync: true}
	n := 0
	h.st = &Store{B: h.m, Names: sprint.Names{Prefix: "d-"}, Actor: dCoordinator,
		Now:   func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now },
		NewID: func() string { h.mu.Lock(); defer h.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}, Rand: func(int64) int64 { return 0 }, Grace: 200 * time.Millisecond}
	if err := h.st.Init(h.ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.m.RowsAdd(h.ctx, "d-readers", dReaders); err != nil {
		t.Fatal(err)
	}
	if err := h.m.SetCoordinator(h.ctx, dCoordinator); err != nil {
		t.Fatal(err)
	}
	h.model = refmodel.New(dReaders, nil, dCoordinator)
	h.Acted, h.Tried = map[string]int{}, map[string]int{}
	h.seen = map[string]map[string]bool{}
	return h
}

// observe is Abstract of what the engine's store holds: the four tables with
// the records of every card either side has held this epoch, the open
// judgments, the fence and the machine.
func (h *dHarness) observe() refmodel.State {
	ids := map[string][]string{}
	add := func(table, id string) {
		if h.seen[table] == nil {
			h.seen[table] = map[string]bool{}
		}
		h.seen[table][id] = true
	}
	for id := range h.model.Primaries {
		add(sprint.Work, id)
	}
	for id := range h.model.Work {
		add(sprint.Fleet, id)
	}
	for id := range h.model.Reads {
		add(sprint.Readers, id)
	}
	for id := range h.model.Merge {
		add(sprint.Merge, id)
	}
	for table, set := range h.seen {
		for id := range set {
			ids[table] = append(ids[table], id)
		}
		sort.Strings(ids[table])
	}
	s, err := h.st.Load(h.ctx, All, func(s *sprint.Snapshot) map[string][]string {
		out := map[string][]string{}
		for table, want := range ids {
			for _, id := range want {
				if s.T(table).Placed(id) == nil {
					out[table] = append(out[table], id)
				}
			}
		}
		return out
	})
	if err != nil {
		h.t.Fatalf("load: %v", err)
	}
	for _, name := range All {
		for id := range s.T(name).Cards {
			add(name, id)
		}
	}
	h.epoch = s.Epoch
	o := refmodel.Observed{Snap: s, Machine: refmodel.Stopped}
	if p := h.pending(); p != nil {
		o.Pending = p.Verb
	}
	mach, _, err := h.st.Machine(h.ctx)
	if err != nil {
		h.t.Fatalf("machine: %v", err)
	}
	if mach.Running() {
		o.Machine = refmodel.Running
	}
	a := refmodel.Abstract(o)
	a.Coordinator = dCoordinator
	return a
}

// pending is the operation the fence of the sprint's epoch holds.
func (h *dHarness) pending() *OpRecord {
	f, err := h.m.AtEpoch(h.epoch, false).ReadFence(h.ctx)
	if err != nil {
		h.t.Fatalf("fence: %v", err)
	}
	return f.Pending
}

// openNotes is the open judgments of the sprint's epoch.
func (h *dHarness) openNotes() []sprint.Open {
	open, err := h.m.AtEpoch(h.epoch, false).OpenNotes(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	return open
}

// engineErr is an engine step's refusal: an error, or cards refused.
func engineErr(err error, refused []sprint.Refusal) string {
	if err != nil {
		return err.Error()
	}
	if len(refused) > 0 {
		var ws []string
		for _, r := range refused {
			ws = append(ws, r.Key+": "+r.Why)
		}
		return strings.Join(ws, "; ")
	}
	return ""
}

// do runs one action on the engine and the model and compares them. It
// returns the findings of the action.
func (h *dHarness) do(a dAction) []dFinding {
	h.seq = append(h.seq, a)
	h.mu.Lock()
	h.now = h.now.Add(time.Second)
	h.mu.Unlock()
	pre := h.observe()
	h.cutTable, h.mid = "", nil
	refusedWhy, cutOK := h.engine(a, pre)
	post := h.observe()
	h.Tried[a.Kind]++
	if len(refmodel.Compare(post, pre)) > 0 {
		h.Acted[a.Kind]++
	}
	next, merr := h.modelStep(a, pre, post)
	var out []dFinding
	if h.mid != nil && merr == nil && h.cutTable == "d-work" {
		// D1: cut before its last write, the work table, the store holds
		// the step's state but for the work table (the model's Crash),
		// and the fence holds the operation. Repair (line 695) then
		// finishes it from its record.
		want := refmodel.Crash(h.model, next, h.mid.Pending)
		if d := refmodel.Compare(*h.mid, want); len(d) > 0 {
			out = append(out, dFinding{Seq: append([]dAction(nil), h.seq...), Kind: "state", Detail: "while the step is cut, before repair", Diffs: d})
			h.findings = append(h.findings, out...)
		}
	}
	return append(out, h.record(a, post, next, merr, refusedWhy, pre, cutOK)...)
}

// record compares the engine's state after the action with the model's and
// keeps the findings: the model's refusal (merr) or the engine's (refusedWhy)
// on one side only, a choice the model does not allow, a cut step that did not
// repair, and every field that differs. base is the state the action started
// from.
func (h *dHarness) record(a dAction, post, next refmodel.State, merr error, refusedWhy string, base refmodel.State, cutOK bool) []dFinding {
	var out []dFinding
	seq := append([]dAction(nil), h.seq...)
	var ce *refmodel.ChoiceError
	switch {
	case errors.As(merr, &ce):
		out = append(out, dFinding{Seq: seq, Kind: "choice", Detail: "the engine made a choice the model does not allow: " + ce.Why})
		next = h.model
	case merr != nil:
		if d := refmodel.Compare(post, base); len(d) > 0 {
			out = append(out, dFinding{Seq: seq, Kind: "refusal", Detail: "model refuses: " + merr.Error() + "; the engine acted", Diffs: d})
		}
		next = h.model
	case refusedWhy != "":
		if d := refmodel.Compare(h.model, next); len(d) > 0 {
			out = append(out, dFinding{Seq: seq, Kind: "refusal", Detail: "engine refuses: " + refusedWhy + "; the model acts"})
		}
	}
	if !cutOK {
		out = append(out, dFinding{Seq: seq, Kind: "cut", Detail: "the cut step did not repair cleanly"})
	}
	if len(out) == 0 {
		if d := refmodel.Compare(post, next); len(d) > 0 {
			f := dFinding{Seq: seq, Kind: "state", Diffs: d}
			if refusedWhy != "" {
				f.Detail = "the engine refused: " + refusedWhy
			}
			out = append(out, f)
		}
	}
	h.model = next
	if len(out) > 0 && h.resync {
		h.model = post
	}
	if a.Kind == "clear" {
		h.seen = map[string]map[string]bool{}
	}
	h.findings = append(h.findings, out...)
	return out
}

// engine runs the action on the engine; a cut action is cut before its
// work-table write (when the step writes another table first) and repaired.
func (h *dHarness) engine(a dAction, pre refmodel.State) (refused string, cutOK bool) {
	cutOK = true
	if a.CutAt > 0 {
		seen := map[string]bool{}
		cut := ""
		h.m.Fail = func(point string) error {
			if !strings.HasPrefix(point, "apply ") || !strings.HasSuffix(point, " before") {
				return nil
			}
			t := strings.TrimSuffix(strings.TrimPrefix(point, "apply "), " before")
			if !seen[t] {
				seen[t] = true
				if len(seen) == a.CutAt {
					cut = t
				}
			}
			if t == cut {
				return errors.New("cut")
			}
			return nil
		}
		defer func() {
			h.m.Fail = nil
			if h.pending() == nil {
				return
			}
			h.Cuts++
			h.cutTable = cut
			if a.Leave {
				mid := h.observe()
				h.mid = &mid
			}
			if a.CutAt == 1 {
				// its writer is gone past the grace
				h.mu.Lock()
				h.now = h.now.Add(h.st.Grace + time.Second)
				h.mu.Unlock()
			}
			if _, err := h.st.Repair(h.ctx); err != nil || h.pending() != nil {
				cutOK = false
			}
		}()
	}
	run := func(step Step) string {
		res, err := h.st.Run(h.ctx, step)
		if a.CutAt > 0 && err != nil && h.pending() != nil {
			return ""
		}
		return engineErr(err, res.Refused)
	}
	switch a.Kind {
	case "add":
		return run(AddStep(sprint.AddReq{Stream: a.Stream, IDs: a.IDs, Needs: a.Needs, Sentinel: a.Sentinel, Before: a.Before, After: a.After, Who: dCoordinator})), cutOK
	case "tick":
		_, err := h.st.Tick(h.ctx)
		return engineErr(err, nil), cutOK
	case "start", "stop":
		_, _, _, err := h.st.SetMachine(h.ctx, a.Kind == "start")
		return engineErr(err, nil), cutOK
	case "take":
		return run(TakeStep(sprint.TakeReq{As: a.Member, Sel: sprint.Sel{IDs: []string{a.Card}}, Gens: map[string]int{a.Card: a.Gen}})), cutOK
	case "finish":
		return run(FinishStep(sprint.FinishReq{As: a.Member, Sel: sprint.Sel{IDs: []string{a.Card}}, Gens: map[string]int{a.Card: a.Gen}, Failed: !a.OK, Report: "report"})), cutOK
	case "begin":
		return run(ReadStep(sprint.ReadReq{As: a.Reader, Sel: sprint.Sel{IDs: []string{a.Card}}, Begin: true})), cutOK
	case "read":
		v := "ok"
		if !a.OK {
			v = "broken"
		}
		return run(ReadStep(sprint.ReadReq{As: a.Reader, Sel: sprint.Sel{IDs: []string{a.Card}}, Verdict: v, Finding: "finding"})), cutOK
	case "accept":
		return run(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: a.IDs}})), cutOK
	case "rework":
		return run(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: a.IDs}, Fix: "f"})), cutOK
	case "drop":
		return run(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: a.IDs}, Reason: "r"})), cutOK
	case "rank":
		return run(RankStep(sprint.RankReq{IDs: a.IDs, First: true})), cutOK
	case "return":
		return run(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: a.IDs}, Reason: "r"})), cutOK
	case "merge":
		r := sprint.MergeReq{Stream: a.Stream, Batch: a.Batch}
		switch a.Fact {
		case "conflict":
			r.Conflict = a.id()
		case "cross":
			r.Cross = a.id() + "=" + a.Other
		case "red":
			r.Red = true
		case "rejected":
			r.Rejected = true
		}
		return run(MergeStep(r)), cutOK
	case "resume":
		return run(ResumeStep(sprint.ResumeReq{Stream: a.Stream, Did: a.Did})), cutOK
	case "fleet":
		return run(FleetStep(sprint.FleetReq{Op: a.Op, Member: a.Member})), cutOK
	case "ci":
		return run(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: a.IDs}, Red: !a.OK, Run: fmt.Sprint(a.Run), Source: "test"})), cutOK
	case "ack":
		note := h.openNote(a.Type, a.Subject)
		h.preNote, h.preSubjects, h.preNeeds = note, h.subjectsOf(note), h.needsOf(note)
		if note == "" {
			return "no open judgment " + a.Type + " on " + a.Subject, cutOK
		}
		return run(AckStep(sprint.AckReq{Notes: []string{note}, Reason: "looked"})), cutOK
	case "ask":
		return run(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: a.IDs}})), cutOK
	case "another":
		return run(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: a.IDs}, Another: true})), cutOK
	case "release":
		return run(ReleaseStep(sprint.ReleaseReq{IDs: a.IDs, Reason: "r", Coordinator: dCoordinator, Who: dCoordinator})), cutOK
	case "resolve":
		return run(ResolveStep(sprint.ResolveReq{Sel: sprint.Sel{IDs: a.IDs}})), cutOK
	case "clear":
		_, err := h.st.Clear(h.ctx)
		return engineErr(err, nil), cutOK
	}
	h.t.Fatalf("unknown action %q", a.Kind)
	return "", cutOK
}

// openNote is the id of the open judgment of the type (as the model names
// it) on the subject, "" when there is none.
func (h *dHarness) openNote(typ, subject string) string {
	for _, o := range h.openNotes() {
		if o.Note.Kind != sprint.Judgment {
			continue
		}
		j := refmodel.JudgmentType(strings.TrimSuffix(o.Note.Type, sprint.NRepeatSuffix))
		sub := o.Subject()
		if j == refmodel.JNoMember {
			sub = "fleet"
		}
		if j == typ && sub == subject {
			return o.Note.ID
		}
	}
	return ""
}

// subjectsOf is the subjects an open judgment of the type on the subject
// shares its notification with: an ack closes every subject of it.
func (h *dHarness) subjectsOf(note string) []string {
	var out []string
	for _, o := range h.openNotes() {
		if o.Note.ID == note {
			sub := o.Subject()
			if refmodel.JudgmentType(o.Note.Type) == refmodel.JNoMember {
				sub = "fleet"
			}
			out = append(out, sub)
		}
	}
	sort.Strings(out)
	return out
}

// needsOf is the dropped needs a blocked notification names.
func (h *dHarness) needsOf(note string) []string {
	for _, o := range h.openNotes() {
		if o.Note.ID == note {
			return o.Note.Needs
		}
	}
	return nil
}

// modelStep applies the action to the model, with the choices the engine
// made read from its state before and after.
func (h *dHarness) modelStep(a dAction, pre, post refmodel.State) (refmodel.State, error) {
	s := h.model
	var next refmodel.State
	var err error
	switch a.Kind {
	case "add":
		scores := map[string]float64{}
		args := refmodel.AddArgs{Stream: a.Stream, IDs: a.IDs, Needs: a.Needs, Sentinel: a.Sentinel, Before: a.Before, After: a.After}
		prev := 0.0
		for k, id := range a.IDs {
			if p, ok := post.Primaries[id]; ok && pre.Primaries[id].Stream == "" {
				scores[id] = p.Score
			} else {
				scores[id] = refmodel.DefaultScore(s, args, k, prev)
			}
			prev = scores[id]
		}
		next, err = refmodel.Add(s, args, scores)
	case "tick":
		next, err = refmodel.Tick(s, tickChoices(pre, post))
	case "start", "stop":
		next, err = refmodel.SetMachine(s, a.Kind == "start")
	case "take":
		next, err = refmodel.Take(s, a.Member, a.Card, a.Gen)
	case "finish":
		next, err = refmodel.Finish(s, a.Member, a.Card, a.Gen, a.OK)
	case "begin":
		next, err = refmodel.ReadStart(s, a.Reader, a.Card)
	case "read":
		next, err = refmodel.Read(s, a.Reader, a.Card, a.OK)
	case "accept":
		next, err = refmodel.Accept(s, a.IDs)
	case "rework":
		m := ""
		if p, ok := s.Primaries[a.id()]; ok {
			if w, ok := post.Work[refmodel.WC(a.id(), p.Attempt+1)]; ok {
				m = w.Member
			} else if len(s.Up()) > 0 {
				m = firstShortest(s)
			}
		}
		next, err = refmodel.Rework(s, a.id(), m)
	case "drop":
		next, err = refmodel.Drop(s, a.id())
	case "rank":
		sc := post.Primaries[a.id()].Score
		if p, ok := post.Primaries[a.id()]; !ok || p.Score == pre.Primaries[a.id()].Score {
			sc = minScore(s, a.id()) - 1
		}
		next, err = refmodel.Rank(s, a.id(), sc)
	case "return":
		next, err = refmodel.Return(s, a.id())
	case "merge":
		switch a.Fact {
		case "green":
			next, err = refmodel.MergeGreen(s, a.Stream, a.Batch)
		case "conflict":
			next, err = refmodel.MergeStop(s, a.Stream, a.Batch, a.id(), refmodel.CConflict, "")
		case "cross":
			next, err = refmodel.MergeStop(s, a.Stream, a.Batch, a.id(), refmodel.CCross, a.Other)
		case "red", "rejected":
			next, err = refmodel.MergeRed(s, a.Stream, a.Fact == "rejected")
		}
	case "resume":
		next, err = refmodel.Resume(s, a.Stream, a.Did)
	case "fleet":
		moves := moved(pre, post)
		switch a.Op {
		case "up":
			next, err = refmodel.FleetUp(s, a.Member, moves)
		case "down":
			next, err = refmodel.FleetDown(s, a.Member, moves)
		case "level":
			next, err = refmodel.Level(s, moves)
		}
	case "ci":
		if a.OK {
			next, err = refmodel.CiGreen(s, a.id())
		} else {
			next, err = refmodel.CiRed(s, a.id())
		}
	case "ack":
		subs := []string{a.Subject}
		if !pre.Open[refmodel.Judgment{Type: a.Type, Subject: a.Subject}] {
			subs = nil
		} else if h.preNote != "" {
			subs = h.preSubjects
		}
		next, err = refmodel.Ack(s, a.Type, subs, h.preNeeds)
	case "ask":
		next, err = refmodel.Ask(s, a.id(), newReaders(pre, post, a.id()))
	case "another":
		r := ""
		if rs := newReaders(pre, post, a.id()); len(rs) == 1 {
			r = rs[0]
		} else if rs == nil {
			r = firstUnasked(s, a.id())
		}
		next, err = refmodel.AskAnother(s, a.id(), r)
	case "release":
		next, err = refmodel.Release(s, a.IDs, dCoordinator)
	case "resolve":
		next, err = refmodel.Resolve(s, a.id())
	case "clear":
		next, err = refmodel.Clear(s)
	}
	if err != nil {
		return s, err
	}
	return next, nil
}

func minScore(s refmodel.State, p string) float64 {
	lo := s.Primaries[p].Score
	for _, q := range s.StreamOrder(s.Primaries[p].Stream) {
		if s.Primaries[q].Score < lo {
			lo = s.Primaries[q].Score
		}
	}
	return lo
}

func firstShortest(s refmodel.State) string {
	up := s.Up()
	for _, m := range up {
		if s.ShortestIn(m, up) {
			return m
		}
	}
	return ""
}

func firstUnasked(s refmodel.State, p string) string {
	a := s.Primaries[p].Attempt
	for _, r := range s.Readers {
		if _, ok := s.Reads[refmodel.RC(p, a, r)]; !ok {
			return r
		}
	}
	return ""
}

// newReaders is the readers of the read cards of p cut by the step.
func newReaders(pre, post refmodel.State, p string) []string {
	var out []string
	for id, c := range post.Reads {
		if _, ok := pre.Reads[id]; !ok && c.Primary == p {
			out = append(out, c.Reader)
		}
	}
	sort.Strings(out)
	return out
}

// moved is the work cards the step moved from one member's ready or
// working cell to another member's ready cell: the choice of a fleet step.
func moved(pre, post refmodel.State) map[string]string {
	out := map[string]string{}
	for id, w := range post.Work {
		b, ok := pre.Work[id]
		if !ok || w.Place != refmodel.FReady || (b.Place != refmodel.FReady && b.Place != refmodel.FWorking) {
			continue
		}
		if b.Member != w.Member {
			out[id] = w.Member
		}
	}
	return out
}

// tickChoices is the choices a tick made: the member each primary's card was
// dealt to, the member each ready card was levelled to, the readers each
// primary was asked of.
func tickChoices(pre, post refmodel.State) refmodel.TickChoices {
	ch := refmodel.TickChoices{Deal: map[string]string{}, Level: map[string]string{}, Ask: map[string][]string{}}
	for id, w := range post.Work {
		b, ok := pre.Work[id]
		switch {
		case w.Place == refmodel.FReady && (!ok || b.Place == refmodel.FWithdrawn):
			ch.Deal[w.Primary] = w.Member
		case ok && w.Place == refmodel.FReady && b.Place == refmodel.FReady && b.Member != w.Member:
			ch.Level[id] = w.Member
		}
	}
	for id, c := range post.Reads {
		if _, ok := pre.Reads[id]; !ok {
			ch.Ask[c.Primary] = append(ch.Ask[c.Primary], c.Reader)
		}
	}
	for p := range ch.Ask {
		sort.Strings(ch.Ask[p])
	}
	return ch
}

// ------------------------------------------------------------------ generation

// pick draws the next action from the state the model holds (the engine's,
// after a resync), biased toward the moves that make progress.
func (h *dHarness) pick(rng *rand.Rand, n *int) dAction {
	a := h.pick1(rng, n)
	switch a.Kind {
	case "tick", "start", "stop", "clear":
	default:
		if rng.IntN(12) == 0 {
			a.CutAt = 1 + rng.IntN(3)
			if rng.IntN(20) != 0 {
				a.CutAt = 2 + rng.IntN(2)
			}
			a.Leave = rng.IntN(2) == 0
		}
	}
	return a
}

func (h *dHarness) pick1(rng *rand.Rand, n *int) dAction {
	s := h.model
	fresh := func() string { *n++; return fmt.Sprintf("p%d", *n) }
	placed := func(states ...string) []string {
		var out []string
		for _, id := range refmodel.Keys(s.Primaries) {
			p := s.Primaries[id]
			if p.State == refmodel.Off || p.Kind != refmodel.KindPrimary && len(states) > 0 {
				continue
			}
			if len(states) == 0 || dHas(states, p.State) {
				out = append(out, id)
			}
		}
		return out
	}
	one := func(xs []string) string {
		if len(xs) == 0 {
			return "nope"
		}
		return xs[rng.IntN(len(xs))]
	}
	for {
		switch w := rng.IntN(130); {
		case w < 20:
			return dAction{Kind: "tick"}
		case w < 30:
			a := dAction{Kind: "add", Stream: dStreams[rng.IntN(len(dStreams))], IDs: []string{fresh()}}
			if rng.IntN(4) == 0 {
				a.IDs = append(a.IDs, fresh())
			}
			all := placed()
			for k := rng.IntN(3); k > 0 && len(all) > 0; k-- {
				a.Needs = appendUniq(a.Needs, one(all))
			}
			if rng.IntN(10) == 0 {
				a.Sentinel, a.IDs, a.Needs = true, a.IDs[:1], nil
			}
			if rng.IntN(6) == 0 {
				in := s.StreamOrder(a.Stream)
				if len(in) > 0 {
					if rng.IntN(2) == 0 {
						a.Before = one(in)
					} else {
						a.After = one(in)
					}
				}
			}
			return a
		case w < 40:
			var cards []string
			for _, id := range refmodel.Keys(s.Work) {
				if s.Work[id].Place == refmodel.FReady {
					cards = append(cards, id)
				}
			}
			if len(cards) == 0 {
				continue
			}
			c := one(cards)
			g := s.Work[c].Gen
			if rng.IntN(10) == 0 {
				g--
			}
			return dAction{Kind: "take", Member: s.Work[c].Member, Card: c, Gen: g}
		case w < 52:
			var cards []string
			for _, id := range refmodel.Keys(s.Work) {
				if pl := s.Work[id].Place; pl == refmodel.FWorking || pl == refmodel.FReady && rng.IntN(8) == 0 {
					cards = append(cards, id)
				}
			}
			if len(cards) == 0 {
				continue
			}
			c := one(cards)
			g := s.Work[c].Gen
			if rng.IntN(10) == 0 {
				g--
			}
			return dAction{Kind: "finish", Member: s.Work[c].Member, Card: c, Gen: g, OK: rng.IntN(5) != 0}
		case w < 70:
			var cards []string
			for _, id := range refmodel.Keys(s.Reads) {
				if pl := s.Reads[id].Place; pl == refmodel.Asked || pl == refmodel.Reading {
					cards = append(cards, id)
				}
			}
			if len(cards) == 0 {
				continue
			}
			c := one(cards)
			if s.Reads[c].Place == refmodel.Asked && rng.IntN(3) == 0 {
				return dAction{Kind: "begin", Reader: s.Reads[c].Reader, Card: c}
			}
			return dAction{Kind: "read", Reader: s.Reads[c].Reader, Card: c, OK: rng.IntN(6) != 0}
		case w < 78:
			rev := placed(refmodel.Review)
			if len(rev) == 0 {
				continue
			}
			var good []string
			for _, p := range rev {
				if s.Acceptable(p) {
					good = append(good, p)
				}
			}
			ids := []string{one(rev)}
			if len(good) > 0 && rng.IntN(4) != 0 {
				ids = []string{one(good)}
			}
			if rng.IntN(5) == 0 {
				ids = appendUniq(ids, one(rev))
			}
			return dAction{Kind: "accept", IDs: ids}
		case w < 81:
			return dAction{Kind: "rework", IDs: []string{one(placed(refmodel.Review))}}
		case w < 83:
			var all []string
			for _, id := range refmodel.Keys(s.Primaries) {
				if s.Primaries[id].State != refmodel.Off {
					all = append(all, id)
				}
			}
			return dAction{Kind: "drop", IDs: []string{one(all)}}
		case w < 85:
			return dAction{Kind: "rank", IDs: []string{one(placed())}}
		case w < 87:
			return dAction{Kind: "return", IDs: []string{one(placed(refmodel.Merging))}}
		case w < 97:
			var st []string
			for _, x := range s.StreamNames() {
				if s.Streams[x].State == refmodel.SMerging {
					st = append(st, x)
				}
			}
			if len(st) == 0 || rng.IntN(10) == 0 {
				st = s.StreamNames()
			}
			if len(st) == 0 {
				continue
			}
			a := dAction{Kind: "merge", Stream: one(st), Batch: 1 + rng.IntN(3), Fact: "green"}
			q := s.MergeCell(a.Stream, refmodel.Queued)
			switch f := rng.IntN(20); {
			case f < 2 && len(q) > 0:
				a.Fact, a.IDs = "conflict", []string{q[rng.IntN(min(a.Batch, len(q)))]}
			case f < 4 && len(q) > 0:
				a.Fact, a.IDs = "cross", []string{q[rng.IntN(min(a.Batch, len(q)))]}
				var other []string
				for _, id := range placed() {
					if s.Primaries[id].Stream != a.Stream {
						other = append(other, id)
					}
				}
				a.Other = one(other)
			case f == 4:
				a.Fact = "red"
			case f == 5:
				a.Fact = "rejected"
			}
			return a
		case w < 100:
			var st []string
			for _, x := range s.StreamNames() {
				if s.Streams[x].State == refmodel.SStopped {
					st = append(st, x)
				}
			}
			if len(st) == 0 {
				continue
			}
			a := dAction{Kind: "resume", Stream: one(st), Did: "fixed"}
			if rng.IntN(5) == 0 {
				a.Did = ""
			}
			return a
		case w < 104:
			m := dMembers[rng.IntN(len(dMembers))]
			switch rng.IntN(5) {
			case 0:
				return dAction{Kind: "fleet", Op: "level"}
			case 1, 2:
				return dAction{Kind: "fleet", Op: "down", Member: m}
			}
			return dAction{Kind: "fleet", Op: "up", Member: m}
		case w < 106:
			*n++
			return dAction{Kind: "ci", IDs: []string{one(placed())}, OK: rng.IntN(2) == 0, Run: *n}
		case w < 111:
			var js []refmodel.Judgment
			for j := range s.Open {
				js = append(js, j)
			}
			if len(js) == 0 {
				continue
			}
			sort.Slice(js, func(i, k int) bool { return js[i].String() < js[k].String() })
			j := js[rng.IntN(len(js))]
			return dAction{Kind: "ack", Type: j.Type, Subject: j.Subject}
		case w < 112:
			return dAction{Kind: "ask", IDs: []string{one(placed(refmodel.Review))}}
		case w < 114:
			return dAction{Kind: "another", IDs: []string{one(placed(refmodel.Review))}}
		case w < 118:
			var due []string
			for _, id := range refmodel.Keys(s.Primaries) {
				if p := s.Primaries[id]; p.Kind == refmodel.KindSentinel && p.State == refmodel.Waiting {
					due = append(due, id)
				}
			}
			if len(due) == 0 {
				continue
			}
			return dAction{Kind: "release", IDs: []string{one(due)}}
		case w < 119:
			return dAction{Kind: "resolve", IDs: []string{one(placed(refmodel.Waiting))}}
		case w < 122:
			if rng.IntN(5) == 0 {
				return dAction{Kind: "stop"}
			}
			return dAction{Kind: "start"}
		case w < 123:
			if rng.IntN(4) == 0 {
				return dAction{Kind: "clear"}
			}
		default:
			continue
		}
	}
}

func dHas(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func appendUniq(xs []string, x string) []string {
	if dHas(xs, x) {
		return xs
	}
	return append(xs, x)
}

// dSetup is the fixed start of every sequence: two members up, the machine
// running, and five primaries in two streams.
func dSetup() []dAction {
	return []dAction{
		{Kind: "fleet", Op: "up", Member: "m1"},
		{Kind: "fleet", Op: "up", Member: "m2"},
		{Kind: "start"},
		{Kind: "add", Stream: "s1", IDs: []string{"a1", "a2", "a3"}},
		{Kind: "add", Stream: "s2", IDs: []string{"b1", "b2"}, Needs: []string{"a1"}},
	}
}

// dGenerate is the sequence of one seed: the setup, then steps actions drawn
// against the engine as it goes.
func dGenerate(t testing.TB, seed uint64, steps int) ([]dAction, []dFinding) {
	h := newDHarness(t)
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	n := 0
	for _, a := range dSetup() {
		h.do(a)
	}
	for i := 0; i < steps; i++ {
		h.do(h.pick(rng, &n))
	}
	return h.seq, h.findings
}

// dReplay runs a sequence from a new store and returns its findings.
func dReplay(t testing.TB, seq []dAction) []dFinding {
	h := newDHarness(t)
	for _, a := range seq {
		h.do(a)
	}
	return h.findings
}

// dShrink finds a shortest sequence that still ends in a finding of the
// signature: cut after the first such finding, then drop actions (setup
// kept) one chunk at a time while it still reproduces.
func dShrink(t testing.TB, f dFinding) dFinding {
	sig := f.Sig()
	find := func(seq []dAction) (dFinding, bool) {
		for _, g := range dReplay(t, seq) {
			if g.Sig() == sig {
				return g, true
			}
		}
		return dFinding{}, false
	}
	best := f
	seq := f.Seq
	keep := len(dSetup())
	for chunk := (len(seq) - keep) / 2; chunk >= 1; chunk /= 2 {
		for i := keep; i+chunk <= len(seq)-1; {
			try := append(append([]dAction(nil), seq[:i]...), seq[i+chunk:]...)
			if g, ok := find(try); ok {
				best, seq = g, g.Seq
				continue
			}
			i += chunk
		}
	}
	for i := 0; i < keep && i < len(seq)-1; {
		try := append(append([]dAction(nil), seq[:i]...), seq[i+1:]...)
		if g, ok := find(try); ok {
			best, seq, keep = g, g.Seq, keep-1
			continue
		}
		i++
	}
	return best
}
