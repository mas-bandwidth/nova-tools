package sprintfn

import (
	"context"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The tests of the derive phase run the twin of IT12 with the phase bound to
// it (Twin.UseIntents), X that guards nothing, and a stand-in for J that
// records the requests it is given and makes no note: what J does with a
// request is IT15's, and what derive asks of it is the phase's.

// jRecorder stands for J: it records every request it is given.
type jRecorder struct {
	mu    sync.Mutex
	reqs  []NoteReq
	calls int
}

func (j *jRecorder) decide(_ *State, in []NoteReq, _ *Before) ([]tset.Note, JPlan, *Refusal) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.calls++
	j.reqs = append(j.reqs, in...)
	return nil, JPlan{}, nil
}

// take returns the requests recorded since the last take.
func (j *jRecorder) take() []NoteReq {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := j.reqs
	j.reqs, j.calls = nil, 0
	return out
}

// seedCard is a card of the work table the test puts in place.
type seedCard struct {
	id, row, col              string
	needs, open, waived, kind string
}

// keyWriter carries the commands a test writes to the sprint's keys through a
// sprint part, the one way a twin's keys are written.
type keyWriter struct {
	mu   sync.Mutex
	cmds []Cmd
}

func (k *keyWriter) take() []Cmd {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := k.cmds
	k.cmds = nil
	return out
}

// intentRig is a twin with X that passes, the derive phase bound, a J that
// records, stream s1 in the work table, and helpers to seed cards and keys.
type intentRig struct {
	tw   *Twin
	m    *tset.Mem
	log  *LogStub
	j    *jRecorder
	keys *keyWriter
	next int // the next score of a seeded card
}

func newIntentRig(t *testing.T) *intentRig { return newIntentRigWith(t, true) }

// newIntentRigWith is a rig whose twin has the derive phase bound to it, or,
// unbound, the phase of 8.0's signature (Derive) alone.
func newIntentRigWith(t *testing.T, bound bool) *intentRig {
	t.Helper()
	j := &jRecorder{}
	ph := passX()
	ph.JDecide = j.decide
	ph.JCmds = func(*State, JPlan, LogPlan) []Cmd { return nil }
	ph.Derive = Derive
	tw, m, log := newTestTwin(t, ph)
	if bound {
		tw.UseIntents()
	}
	kw := &keyWriter{}
	if err := tw.parts.Register(PartSprint, PartFuncs{
		PreFunc:  func(*State, *Request, *Before) (any, *Refusal) { return nil, nil },
		CmdsFunc: func(*State, any, LogPlan) ([]Cmd, *Refusal) { return kw.take(), nil },
	}); err != nil {
		t.Fatal(err)
	}
	r := &intentRig{tw: tw, m: m, log: log, j: j, keys: kw, next: 1}
	r.mustApply(t, &Request{Epoch: "0", Meta: Meta{Verb: "add"}, Body: Body{Entries: []tset.Entry{
		{Kind: "rows", Table: sprint.Work, Add: []string{"s1"}}}}})
	return r
}

func (r *intentRig) step(t *testing.T, req *Request) Result {
	t.Helper()
	res, err := Step(context.Background(), r.tw, req)
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	return res
}

func (r *intentRig) mustApply(t *testing.T, req *Request) *StepReply {
	t.Helper()
	res := r.step(t, req)
	if res.Refusal != nil || res.Err != nil || res.Step == nil {
		t.Fatalf("step result: refusal %v, err %v", res.Refusal, res.Err)
	}
	return res.Step
}

func (r *intentRig) mustRefuse(t *testing.T, req *Request) *Refusal {
	t.Helper()
	res := r.step(t, req)
	if res.Refusal == nil {
		t.Fatalf("step applied (%+v, err %v); want a refusal", res.Step, res.Err)
	}
	return res.Refusal
}

// seed creates the cards, one create entry a cell.
func (r *intentRig) seed(t *testing.T, cards ...seedCard) {
	t.Helper()
	type cell struct{ row, col string }
	var order []cell
	by := map[cell][]seedCard{}
	for _, c := range cards {
		if c.row == "" {
			c.row = "s1"
		}
		k := cell{c.row, c.col}
		if by[k] == nil {
			order = append(order, k)
		}
		by[k] = append(by[k], c)
	}
	var entries []tset.Entry
	for _, k := range order {
		e := tset.Entry{Kind: "create", Table: sprint.Work, To: k.row + ":" + k.col, Set: map[string]string{"kind": "work"}}
		for _, c := range by[k] {
			each := map[string]string{}
			for name, v := range map[string]string{"needs": c.needs, "open": c.open, "waived": c.waived, "kind": c.kind} {
				if v != "" {
					each[name] = v
				}
			}
			e.IDs = append(e.IDs, c.id)
			e.Scores = append(e.Scores, strconv.Itoa(r.next))
			e.Each = append(e.Each, each)
			e.About = append(e.About, c.id)
			r.next++
		}
		entries = append(entries, e)
	}
	r.mustApply(t, &Request{Epoch: "0", Meta: Meta{Verb: "add"}, Body: Body{Entries: entries}})
}

// addRow adds a stream's row to the work table.
func (r *intentRig) addRow(t *testing.T, rows ...string) {
	t.Helper()
	r.mustApply(t, &Request{Epoch: "0", Meta: Meta{Verb: "add"}, Body: Body{Entries: []tset.Entry{
		{Kind: "rows", Table: sprint.Work, Add: rows}}}})
}

// drop removes cards from the cell they are in, keeping their records.
func (r *intentRig) drop(t *testing.T, from string, ids ...string) {
	t.Helper()
	r.mustApply(t, &Request{Epoch: "0", Meta: Meta{Verb: "drop"}, Body: Body{Entries: []tset.Entry{
		{Kind: "remove", Table: sprint.Work, From: from, IDs: ids, About: ids}}}})
}

// writeKeys writes commands to the sprint's keys.
func (r *intentRig) writeKeys(t *testing.T, cmds ...Cmd) {
	t.Helper()
	r.keys.mu.Lock()
	r.keys.cmds = cmds
	r.keys.mu.Unlock()
	r.mustApply(t, &Request{Epoch: "0", Meta: Meta{Verb: "init"}, Sprint: &SprintPart{}})
}

// seen is what a read of a card shows.
type seen struct {
	exists bool
	place  string
	fields map[string]string
}

func (r *intentRig) card(t *testing.T, id string) seen {
	t.Helper()
	res, err := Read(context.Background(), r.tw, &ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{{Kind: "ids", Table: sprint.Work,
		IDs: []string{id}, Fields: []string{"needs", "open", "waived"}}}})
	if err != nil || res.Read == nil {
		t.Fatalf("read %s: %+v, %v", id, res, err)
	}
	rec := res.Read.Tset[0].Records[0]
	out := seen{exists: rec.Exists, fields: map[string]string{}}
	if rec.Place != nil {
		out.place = rec.Place.Row + ":" + rec.Place.Col
	}
	for name, f := range rec.Fields {
		if f.Present {
			out.fields[name] = f.Value
		}
	}
	return out
}

// key is a sprint key under the test prefix: the name carries its own epoch
// suffix, "wait:n@0", where the key has one.
func key(name string) string { return testPrefix + "sprint:" + name }

func zadd(name string, pairs ...string) Cmd {
	return Command("ZADD", key(name), kindZSet, pairs...)
}

func hset(name string, kv ...string) Cmd {
	return Command("HSET", key(name), kindHash, kv...)
}

// members is the members of a sorted set of the twin's keys, in order.
func (r *intentRig) members(name string) []string {
	kv, ok := r.tw.SprintKeys()[key(name)]
	if !ok {
		return nil
	}
	var out []string
	for m := range kv.ZSet {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

func (r *intentRig) score(name, member string) (float64, bool) {
	s, ok := r.tw.SprintKeys()[key(name)].ZSet[member]
	return s, ok
}

func (r *intentRig) image(t *testing.T) string { return string(image(t, r.tw, r.m, r.log)) }

// intentStep is a step of the coordinator carrying intents and entries. IT12's
// S.before asks for the record of every intent's Card, and a needmet or a
// needgone names no single card (8.0 gives Card to the waiter of a waitfor and
// a waive): Layer 1 refuses the empty id REQUEST, before derive runs. So the
// step names Need as Card for them, which no phase reads; an open question.
func intentStep(entries []tset.Entry, intents ...Intent) *Request {
	for i, in := range intents {
		if (in.Kind == IntentNeedMet || in.Kind == IntentNeedGone) && in.Card == "" {
			intents[i].Card = in.Need
		}
	}
	return &Request{Epoch: "0", Meta: Meta{Verb: "add", Actor: "coordinator"},
		Body: Body{Entries: entries, Intents: intents}}
}

// admit is an add of a card that waits for needs, as the builder plans it: a
// create entry in waiting with the needs and the open it read, and the waitfor.
func admit(id, needs, open string) ([]tset.Entry, Intent) {
	e := tset.Entry{Kind: "create", Table: sprint.Work, To: "s1:waiting", IDs: []string{id}, Scores: []string{"100"},
		Set: map[string]string{"kind": "work", "needs": needs, "open": open}, About: []string{id}}
	return []tset.Entry{e}, Intent{Kind: IntentWaitFor, Card: id, Needs: sprint.Split(needs)}
}

func reqKinds(reqs []NoteReq) []string {
	var out []string
	for _, n := range reqs {
		out = append(out, n.Op+" "+n.Type+" "+n.Cause+" "+strings.Join(n.Subjects, ","))
	}
	return out
}

const (
	missingJ = "a primary is blocked on something missing"
	droppedJ = "a primary is blocked on something dropped"
)

// TestNeedmetTwoNeedsOneTick: two needs of one waiter landing in one tick
// lower its open by two, in one entry, and take it out of both wait sets
// (1.3.3: "two needmet of one waiter in one step lower it by two in one
// entry"). One entry names the card, so layer 1 does not refuse TWICE, and J
// is asked to close the judgment of each need. A second run of the same step
// finds the card in neither set and writes nothing (1.3.3, Idempotence).
func TestNeedmetTwoNeedsOneTick(t *testing.T) {
	t.Parallel()
	r := newIntentRig(t)
	r.seed(t,
		seedCard{id: "n1", col: "landed"}, seedCard{id: "n2", col: "landed"},
		seedCard{id: "w", col: "waiting", needs: "n1,n2", open: "2"})
	r.writeKeys(t, zadd("wait:n1@0", "0", "w"), zadd("wait:n2@0", "0", "w"))

	req := intentStep(nil,
		Intent{Kind: IntentNeedMet, Need: "n1", Waiters: []string{"w"}},
		Intent{Kind: IntentNeedMet, Need: "n2", Waiters: []string{"w"}})
	reply := r.mustApply(t, req)
	if reply.Reply.Changed != 1 || !reflect.DeepEqual(reply.Reply.ChangedPerEntry, []int{1}) || reply.Reply.Lines != 1 {
		t.Fatalf("changed %d in entries %v, %d lines; want one card in one entry and one line",
			reply.Reply.Changed, reply.Reply.ChangedPerEntry, reply.Reply.Lines)
	}
	if got := r.card(t, "w"); got.fields["open"] != "0" || got.place != "s1:waiting" {
		t.Fatalf("w after two needs landed: %+v; want open 0, still waiting", got)
	}
	if r.members("wait:n1@0") != nil || r.members("wait:n2@0") != nil {
		t.Fatalf("wait sets after: %v %v; want both empty", r.members("wait:n1@0"), r.members("wait:n2@0"))
	}
	want := []string{"close " + missingJ + " n1 w", "close " + missingJ + " n2 w"}
	if got := reqKinds(r.j.take()); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests to J %v, want %v", got, want)
	}

	before := r.image(t)
	again := r.mustApply(t, req)
	if again.Reply.Changed != 0 || again.Reply.Lines != 0 || r.image(t) != before {
		t.Fatalf("the same step run twice changed %d cards and wrote %d lines, or the image moved", again.Reply.Changed, again.Reply.Lines)
	}
	if got := r.j.take(); len(got) != 0 {
		t.Fatalf("the second run asked J for %v; it found no waiter", got)
	}
}

// TestNeedgoneHeadMoves: a need removed takes each card still waiting for it
// out of its wait set, so the head of the set moves (W15: a needgone that
// leaves the waiter in the set starves the set's head), opens "blocked on
// something dropped" through J, and leaves the card's open as it was: the
// judgment counts in it (I2). Nothing in the table changes.
func TestNeedgoneHeadMoves(t *testing.T) {
	t.Parallel()
	r := newIntentRig(t)
	r.seed(t,
		seedCard{id: "d", col: "ready"},
		seedCard{id: "w1", col: "waiting", needs: "d", open: "1"},
		seedCard{id: "w2", col: "waiting", needs: "d", open: "1"},
		seedCard{id: "w3", col: "waiting", needs: "d", open: "1"})
	r.drop(t, "s1:ready", "d")
	r.writeKeys(t, zadd("wait:d@0", "0", "w1", "0", "w2", "0", "w3"))

	reply := r.mustApply(t, intentStep(nil, Intent{Kind: IntentNeedGone, Need: "d", Waiters: []string{"w1", "w2"}}))
	if reply.Reply.Changed != 0 {
		t.Fatalf("a needgone changed %d cards; it changes none", reply.Reply.Changed)
	}
	if got := r.members("wait:d@0"); !reflect.DeepEqual(got, []string{"w3"}) {
		t.Fatalf("wait:d after: %v; want the head moved on to w3", got)
	}
	for _, id := range []string{"w1", "w2", "w3"} {
		if got := r.card(t, id); got.fields["open"] != "1" {
			t.Fatalf("%s open %q after the need was removed; want 1 (the judgment counts in it)", id, got.fields["open"])
		}
	}
	want := []string{"open " + droppedJ + " d w1,w2"}
	if got := reqKinds(r.j.take()); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests to J %v, want %v", got, want)
	}
	r.mustApply(t, intentStep(nil, Intent{Kind: IntentNeedGone, Need: "d", Waiters: []string{"w3"}}))
	if r.members("wait:d@0") != nil {
		t.Fatalf("wait:d still holds %v after its last waiter was taken out", r.members("wait:d@0"))
	}
}

// TestNeedmetSkipsQuarantined: a quarantined card is left out of every
// derivation (1.3.1, I1): it stays in the wait set it was in, its open is not
// lowered, and J is not asked about it; a needgone leaves it in the set too.
func TestNeedmetSkipsQuarantined(t *testing.T) {
	t.Parallel()
	r := newIntentRig(t)
	r.seed(t,
		seedCard{id: "n", col: "landed"}, seedCard{id: "g", col: "ready"},
		seedCard{id: "w1", col: "waiting", needs: "n", open: "1"},
		seedCard{id: "w2", col: "waiting", needs: "n", open: "1"},
		seedCard{id: "g1", col: "waiting", needs: "g", open: "1"},
		seedCard{id: "g2", col: "waiting", needs: "g", open: "1"})
	r.drop(t, "s1:ready", "g")
	r.writeKeys(t, zadd("wait:n@0", "0", "w1", "0", "w2"), zadd("wait:g@0", "0", "g1", "0", "g2"),
		hset("quarantine@0", "w1", "DRIFT", "g1", "DRIFT"))

	r.mustApply(t, intentStep(nil,
		Intent{Kind: IntentNeedMet, Need: "n", Waiters: []string{"w1", "w2"}},
		Intent{Kind: IntentNeedGone, Need: "g", Waiters: []string{"g1", "g2"}}))
	if got := r.members("wait:n@0"); !reflect.DeepEqual(got, []string{"w1"}) {
		t.Fatalf("wait:n after: %v; want only the quarantined w1 left", got)
	}
	if got := r.members("wait:g@0"); !reflect.DeepEqual(got, []string{"g1"}) {
		t.Fatalf("wait:g after: %v; want only the quarantined g1 left", got)
	}
	if got := r.card(t, "w1"); got.fields["open"] != "1" {
		t.Fatalf("quarantined w1 open %q, want 1", got.fields["open"])
	}
	if got := r.card(t, "w2"); got.fields["open"] != "0" {
		t.Fatalf("w2 open %q, want 0", got.fields["open"])
	}
	want := []string{"close " + missingJ + " n w2", "open " + droppedJ + " g g2"}
	if got := reqKinds(r.j.take()); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests to J %v, want %v", got, want)
	}
}

// TestWaitforMissingJoinsWaitAndMissing: an admitted card whose need has no
// record enters wait:<n> and, once, missing at R, and J is asked to open
// "blocked on something missing"; the second admission of the same need does
// not move its score (the store has no ZADD NX, so the phase reads the score
// and decides). Every state of a need is checked in one admission: open on the
// table, no record, removed, landed, and created by the same step.
func TestWaitforMissingJoinsWaitAndMissing(t *testing.T) {
	t.Parallel()
	r := newIntentRig(t)
	r.writeKeys(t, hset("clock", "stopped_ms", "1000", "stopped_since_ms", ""))
	first := testTime.UnixMilli() - 1000

	entries, in := admit("w1", "ghost", "1")
	r.mustApply(t, intentStep(entries, in))
	if got := r.members("wait:ghost@0"); !reflect.DeepEqual(got, []string{"w1"}) {
		t.Fatalf("wait:ghost: %v, want w1", got)
	}
	if s, ok := r.score("missing@0", "ghost"); !ok || int64(s) != first {
		t.Fatalf("missing ghost scored %v (%v), want R = %d", s, ok, first)
	}
	if s := r.tw.SprintKeys()[key("wait:ghost@0")].ZSet["w1"]; s != 0 {
		t.Fatalf("w1 scored %v in wait:ghost; a member has the one score %q", s, deriveWaitScore)
	}
	if got := r.card(t, "w1"); got.fields["open"] != "1" || got.place != "s1:waiting" {
		t.Fatalf("w1: %+v; want open 1 in waiting", got)
	}
	want := []string{"open " + missingJ + " ghost w1"}
	if got := reqKinds(r.j.take()); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests to J %v, want %v", got, want)
	}

	// Later, another card names the same ghost: missing keeps its first R.
	r.tw.SetClock(func() time.Time { return testTime.Add(5 * time.Second) })
	entries, in = admit("w2", "ghost", "1")
	entries[0].Scores = []string{"101"}
	r.mustApply(t, intentStep(entries, in))
	if got := r.members("wait:ghost@0"); !reflect.DeepEqual(got, []string{"w1", "w2"}) {
		t.Fatalf("wait:ghost: %v, want w1 and w2", got)
	}
	if s, _ := r.score("missing@0", "ghost"); int64(s) != first {
		t.Fatalf("missing ghost rescored to %v, want it kept at %d", s, first)
	}
	r.j.take()

	// Every state of a need in one admission.
	r.seed(t, seedCard{id: "busy", col: "ready"}, seedCard{id: "done", col: "landed"}, seedCard{id: "gone", col: "ready"})
	r.drop(t, "s1:ready", "gone")
	entries, in = admit("w3", "busy,ghost2,gone,done,mate", "4")
	entries = append(entries, tset.Entry{Kind: "create", Table: sprint.Work, To: "s1:ready", IDs: []string{"mate"},
		Scores: []string{"102"}, About: []string{"mate"}})
	r.mustApply(t, intentStep(entries, in))
	for need, inSet := range map[string]bool{"busy": true, "ghost2": true, "gone": false, "done": false, "mate": true} {
		got := r.members("wait:" + need + "@0")
		if inSet != (len(got) == 1 && got[0] == "w3") {
			t.Errorf("wait:%s holds %v; w3 waits for it: %v", need, got, inSet)
		}
	}
	if _, ok := r.score("missing@0", "ghost2"); !ok {
		t.Errorf("ghost2 has no record and is not in missing")
	}
	for _, need := range []string{"busy", "gone", "done", "mate"} {
		if _, ok := r.score("missing@0", need); ok {
			t.Errorf("%s has a record (or is created by the step) and is in missing", need)
		}
	}
	want = []string{"open " + missingJ + " ghost2 w3", "open " + droppedJ + " gone w3"}
	if got := reqKinds(r.j.take()); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests to J %v, want %v", got, want)
	}

	// Two admissions in one step enter a shared ghost in missing once.
	e1, i1 := admit("w4", "ghost3", "1")
	e2, i2 := admit("w5", "ghost3", "1")
	e2[0].Scores = []string{"103"}
	r.mustApply(t, intentStep(append(e1, e2...), i1, i2))
	if got := r.members("wait:ghost3@0"); !reflect.DeepEqual(got, []string{"w4", "w5"}) {
		t.Fatalf("wait:ghost3: %v, want w4 and w5", got)
	}
	want = []string{"open " + missingJ + " ghost3 w4,w5"}
	if got := reqKinds(r.j.take()); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests to J %v, want one note naming both waiters: %v", got, want)
	}
}

// TestWaitforOpenMustMatchTheNeeds: an admission's create entry carries the
// open the builder read, and derive checks it against the real state at apply
// (the narrower reading of 1.3.3's fold into the create entry, which a
// preplan that may only append cannot make): a need that landed, or came to
// exist, since the read makes the step XGUARD and writes nothing, and a create
// entry that does not carry what the phase needs is REQUEST.
func TestWaitforOpenMustMatchTheNeeds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		entries func() ([]tset.Entry, Intent)
		code    string
		in      string
	}{
		{"a need landed since the read", func() ([]tset.Entry, Intent) { return admit("w", "done,ghost", "2") }, CodeXGuard, "create says open 2, the needs say 1"},
		{"a need no record has gained one", func() ([]tset.Entry, Intent) { return admit("w", "busy", "0") }, CodeXGuard, "create says open 0, the needs say 1"},
		{"an open that is not a number", func() ([]tset.Entry, Intent) { return admit("w", "ghost", "many") }, CodeRequest, `"many", not a whole number`},
		{"a need the create does not name", func() ([]tset.Entry, Intent) {
			e, in := admit("w", "ghost", "1")
			in.Needs = []string{"ghost", "busy"}
			return e, in
		}, CodeRequest, "does not name it in needs"},
		{"created in ready", func() ([]tset.Entry, Intent) {
			e, in := admit("w", "ghost", "1")
			e[0].To = "s1:ready"
			return e, in
		}, CodeRequest, "a card with needs is created in waiting"},
		{"no create entry", func() ([]tset.Entry, Intent) {
			_, in := admit("w", "ghost", "1")
			return nil, in
		}, CodeRequest, "no create entry of the step makes it"},
		{"created by an entry of another table", func() ([]tset.Entry, Intent) {
			e, in := admit("w", "ghost", "1")
			e[0].Table = sprint.Fleet
			e[0].To = "s1:ready"
			return e, in
		}, CodeRequest, "no create entry of the step makes it"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newIntentRig(t)
			r.seed(t, seedCard{id: "done", col: "landed"}, seedCard{id: "busy", col: "ready"})
			r.writeKeys(t, hset("clock", "stopped_ms", "0"))
			entries, in := c.entries()
			before := r.image(t)
			ref := r.mustRefuse(t, intentStep(entries, in))
			if ref.Code != c.code || ref.Phase != PhaseDerive || !strings.Contains(ref.Message, c.in) || !strings.HasSuffix(ref.Message, "; nothing was changed") {
				t.Fatalf("refused %s in %s: %q; want %s naming %q", ref.Code, ref.Phase, ref.Message, c.code, c.in)
			}
			if r.image(t) != before {
				t.Fatalf("a refused step changed the twin")
			}
			if got := r.j.take(); len(got) != 0 {
				t.Fatalf("a refused derive went on to ask J: %v", got)
			}
		})
	}
}

// TestWaitforRefusesCycleAtApply: `add X --needs Y` and `add Y --needs X`
// planned on one read (neither card existed, so each saw its need missing) and
// applied one after the other: the first is admitted, the second is refused
// XGUARD, naming the cycle, and writes nothing (1.3.3: the store runs one call
// at a time, so the walk at apply sees every need an earlier call added, and
// the graph of needs is acyclic by construction, K4). The walk follows the
// needs of open waiting cards through more than one hop, and of the cards the
// step itself admits.
func TestWaitforRefusesCycleAtApply(t *testing.T) {
	t.Parallel()

	t.Run("two adds planned on one read", func(t *testing.T) {
		t.Parallel()
		r := newIntentRig(t)
		r.writeKeys(t, hset("clock", "stopped_ms", "0"))
		ex, ix := admit("X", "Y", "1")
		ey, iy := admit("Y", "X", "1")
		r.mustApply(t, intentStep(ex, ix))
		before := r.image(t)
		r.j.take()
		ref := r.mustRefuse(t, intentStep(ey, iy))
		if ref.Code != CodeXGuard || ref.Phase != PhaseDerive || ref.Message != "X needs Y; a needs cycle; nothing was changed" {
			t.Fatalf("second add refused %s in %s: %q; want XGUARD naming the cycle", ref.Code, ref.Phase, ref.Message)
		}
		if !reflect.DeepEqual(ref.Detail.IDs, []string{"X", "Y"}) {
			t.Fatalf("the cycle's ids %v, want [X Y]", ref.Detail.IDs)
		}
		if r.image(t) != before {
			t.Fatalf("the refused add changed the twin")
		}
		if got := r.card(t, "Y"); got.exists {
			t.Fatalf("Y exists after its add was refused: %+v", got)
		}
		if got := r.j.take(); len(got) != 0 {
			t.Fatalf("the refused add asked J: %v", got)
		}
	})

	t.Run("through existing cards", func(t *testing.T) {
		t.Parallel()
		r := newIntentRig(t)
		r.writeKeys(t, hset("clock", "stopped_ms", "0"))
		r.seed(t,
			seedCard{id: "a", col: "waiting", needs: "b", open: "1"},
			seedCard{id: "b", col: "waiting", needs: "c", open: "1"},
			seedCard{id: "c", col: "waiting", needs: "ghost", open: "1"})
		r.writeKeys(t, zadd("wait:b@0", "0", "a"), zadd("wait:c@0", "0", "b"), zadd("wait:ghost@0", "0", "c"), zadd("missing@0", "1", "ghost"))
		entries, in := admit("ghost", "a", "1")
		before := r.image(t)
		ref := r.mustRefuse(t, intentStep(entries, in))
		if ref.Code != CodeXGuard || ref.Message != "a needs ghost through b, c; a needs cycle; nothing was changed" {
			t.Fatalf("refused %s: %q", ref.Code, ref.Message)
		}
		if !reflect.DeepEqual(ref.Detail.IDs, []string{"a", "b", "c", "ghost"}) || r.image(t) != before {
			t.Fatalf("ids %v, or the twin changed", ref.Detail.IDs)
		}
	})

	t.Run("a card that needs itself", func(t *testing.T) {
		t.Parallel()
		r := newIntentRig(t)
		entries, in := admit("me", "me", "1")
		ref := r.mustRefuse(t, intentStep(entries, in))
		if ref.Code != CodeXGuard || ref.Message != "me needs me; a needs cycle; nothing was changed" {
			t.Fatalf("refused %s: %q", ref.Code, ref.Message)
		}
	})

	t.Run("between the cards of one step", func(t *testing.T) {
		t.Parallel()
		r := newIntentRig(t)
		ex, ix := admit("X", "Y", "1")
		ey, iy := admit("Y", "X", "1")
		ey[0].Scores = []string{"101"}
		before := r.image(t)
		ref := r.mustRefuse(t, intentStep(append(ex, ey...), ix, iy))
		if ref.Code != CodeXGuard || ref.Message != "Y needs X; a needs cycle; nothing was changed" || r.image(t) != before {
			t.Fatalf("refused %s: %q (or the twin changed)", ref.Code, ref.Message)
		}
	})

	t.Run("a long chain names the first cards and then an ellipsis", func(t *testing.T) {
		t.Parallel()
		r := newIntentRig(t)
		r.writeKeys(t, hset("clock", "stopped_ms", "0"))
		var cards []seedCard
		const n = 12
		for i := 0; i < n; i++ {
			next := "c" + strconv.Itoa(i+1)
			cards = append(cards, seedCard{id: "c" + strconv.Itoa(i), col: "waiting", needs: next, open: "1"})
		}
		r.seed(t, cards...)
		// c12 has no record: the last card names a need that does not exist; admit it with a need on c0.
		entries, in := admit("c12", "c0", "1")
		ref := r.mustRefuse(t, intentStep(entries, in))
		want := "c0 needs c12 through c1, c2, c3, c4, c5, c6, c7, c8, ...; a needs cycle; nothing was changed"
		if ref.Code != CodeXGuard || ref.Message != want {
			t.Fatalf("refused %s: %q\nwant %q", ref.Code, ref.Message, want)
		}
		if len(ref.Detail.IDs) != n+1 {
			t.Fatalf("the chain's ids: %d, want all %d", len(ref.Detail.IDs), n+1)
		}
	})

	t.Run("no cycle", func(t *testing.T) {
		t.Parallel()
		r := newIntentRig(t)
		r.writeKeys(t, hset("clock", "stopped_ms", "0"))
		// a diamond: a needs b and c, each of which needs the landed d; and a card that waits on a ready card.
		r.seed(t, seedCard{id: "d", col: "landed"}, seedCard{id: "run", col: "ready"},
			seedCard{id: "b", col: "waiting", needs: "d", open: "0"},
			seedCard{id: "c", col: "waiting", needs: "d,run", open: "1"})
		r.writeKeys(t, zadd("wait:run@0", "0", "c"))
		entries, in := admit("a", "b,c", "2")
		r.mustApply(t, intentStep(entries, in))
		if got := r.members("wait:b@0"); !reflect.DeepEqual(got, []string{"a"}) {
			t.Fatalf("wait:b %v, want a", got)
		}
	})

	t.Run("the walk stops at a card that is not waiting", func(t *testing.T) {
		t.Parallel()
		r := newIntentRig(t)
		r.writeKeys(t, hset("clock", "stopped_ms", "0"))
		// r is ready and names X: only an open waiting card is gone through, so this is no cycle.
		r.seed(t, seedCard{id: "r", col: "ready", needs: "X", open: "1"})
		entries, in := admit("X", "r", "1")
		r.mustApply(t, intentStep(entries, in))
		if got := r.members("wait:r@0"); !reflect.DeepEqual(got, []string{"X"}) {
			t.Fatalf("wait:r %v, want X", got)
		}
	})
}

// chainCards is n open waiting cards, each needing the next, the last naming
// nothing: a chain the walk reads one record a card.
func chainCards(n int) []seedCard {
	cards := make([]seedCard, n)
	for i := range cards {
		needs := ""
		if i+1 < n {
			needs = "c" + strconv.Itoa(i+1)
		}
		cards[i] = seedCard{id: "c" + strconv.Itoa(i), col: "waiting", needs: needs, open: "1"}
	}
	return cards
}

// TestWaitforWalkBound: a chain of 2,001 open waiting cards is refused XGUARD
// naming its head, and a chain of 2,000, the most records a step's walk may
// read (1.3.3), is admitted; the bound is the step's and not each intent's:
// two admissions over one chain read its records once.
func TestWaitforWalkBound(t *testing.T) {
	t.Parallel()
	if WalkRecordsMax != 2000 {
		t.Fatalf("the walk bound is %d; 1.3.3 says 2,000", WalkRecordsMax)
	}

	t.Run("a chain of 2,001 is refused naming its head", func(t *testing.T) {
		t.Parallel()
		r := newIntentRig(t)
		chain := chainCards(WalkRecordsMax + 1)
		r.seed(t, chain[:WalkRecordsMax]...)
		r.seed(t, chain[WalkRecordsMax:]...)
		entries, in := admit("w", "c0", "1")
		before := r.image(t)
		ref := r.mustRefuse(t, intentStep(entries, in))
		want := "the needs walk from c0 went past 2000 records; a needs cycle could not be ruled out; nothing was changed"
		if ref.Code != CodeXGuard || ref.Phase != PhaseDerive || ref.Message != want {
			t.Fatalf("refused %s in %s: %q\nwant %q", ref.Code, ref.Phase, ref.Message, want)
		}
		if !reflect.DeepEqual(ref.Detail.IDs, []string{"c0"}) || ref.Detail.Budget != "walk_records" ||
			ref.Detail.Limit == nil || *ref.Detail.Limit != 2000 || ref.Detail.Actual == nil || *ref.Detail.Actual != 2001 {
			t.Fatalf("detail %+v; want the head c0 and the bound 2000 against 2001", ref.Detail)
		}
		if r.image(t) != before {
			t.Fatalf("the refused step changed the twin")
		}
	})

	t.Run("a chain of 2,000 is admitted", func(t *testing.T) {
		t.Parallel()
		r := newIntentRig(t)
		r.writeKeys(t, hset("clock", "stopped_ms", "0"))
		r.seed(t, chainCards(WalkRecordsMax)...)
		entries, in := admit("w", "c0", "1")
		r.mustApply(t, intentStep(entries, in))
		if got := r.members("wait:c0@0"); !reflect.DeepEqual(got, []string{"w"}) {
			t.Fatalf("wait:c0 %v, want w", got)
		}
	})

	t.Run("the bound is the step's", func(t *testing.T) {
		t.Parallel()
		r := newIntentRig(t)
		r.writeKeys(t, hset("clock", "stopped_ms", "0"))
		// Two chains of 1,001 and 1,000 cards: 2,001 records in one step; 2,000 when they share a tail.
		long := chainCards(1001)
		var other []seedCard
		for i := 0; i < 1000; i++ {
			needs := "o" + strconv.Itoa(i+1)
			if i == 999 {
				needs = ""
			}
			other = append(other, seedCard{id: "o" + strconv.Itoa(i), col: "waiting", needs: needs, open: "1"})
		}
		r.seed(t, long...)
		r.seed(t, other...)
		e1, i1 := admit("w1", "c0", "1")
		e2, i2 := admit("w2", "o0", "1")
		e2[0].Scores = []string{"101"}
		before := r.image(t)
		ref := r.mustRefuse(t, intentStep(append(e1, e2...), i1, i2))
		if ref.Code != CodeXGuard || !strings.Contains(ref.Message, "the needs walk from o0 went past 2000 records") || r.image(t) != before {
			t.Fatalf("refused %s: %q", ref.Code, ref.Message)
		}
		// The same chain walked for two waiters is read once.
		e1, i1 = admit("w1", "c0", "1")
		e3, i3 := admit("w3", "c0", "1")
		e3[0].Scores = []string{"103"}
		r.mustApply(t, intentStep(append(e1, e3...), i1, i3))
		if got := r.members("wait:c0@0"); !reflect.DeepEqual(got, []string{"w1", "w3"}) {
			t.Fatalf("wait:c0 %v, want both waiters", got)
		}
	})
}

// TestWaiveMissingRefusedOnceCreated: an ack waives a need of a waiting card
// whose judgment is open (1.3.3). A missing need leaves wait:<n> with the
// card, and missing when it was the last waiter; a missing need that has a
// record now is XGUARD and writes nothing: a live prerequisite is never
// waived. A dropped need is waived while its judgment is open, and a need
// whose judgment closed since the read is left alone.
func TestWaiveMissingRefusedOnceCreated(t *testing.T) {
	t.Parallel()

	// setup: w waits for ghost (missing), for d (dropped) and for run (open);
	// another card waits for ghost too.
	setup := func(t *testing.T) *intentRig {
		r := newIntentRig(t)
		r.seed(t, seedCard{id: "d", col: "ready"}, seedCard{id: "run", col: "ready"},
			seedCard{id: "w", col: "waiting", needs: "ghost,d,run", open: "3"},
			seedCard{id: "v", col: "waiting", needs: "ghost", open: "1"})
		r.drop(t, "s1:ready", "d")
		r.writeKeys(t, zadd("wait:ghost@0", "0", "w", "0", "v"), zadd("wait:run@0", "0", "w"), zadd("missing@0", "5", "ghost"),
			hset("jopen:w@0", missingJ+"|ghost", "n1", droppedJ+"|d", "n2"),
			hset("jopen:v@0", missingJ+"|ghost", "n3"))
		return r
	}

	t.Run("refused once the need has a record", func(t *testing.T) {
		t.Parallel()
		r := setup(t)
		r.seed(t, seedCard{id: "ghost", col: "ready"}) // the need has been created since the ack was planned
		before := r.image(t)
		ref := r.mustRefuse(t, intentStep(nil, Intent{Kind: IntentWaive, Card: "w", Needs: []string{"ghost"}}))
		want := "ghost exists now; w waits for ghost to land; drop w or wait; nothing was changed"
		if ref.Code != CodeXGuard || ref.Phase != PhaseDerive || ref.Message != want {
			t.Fatalf("refused %s in %s: %q\nwant %q", ref.Code, ref.Phase, ref.Message, want)
		}
		if !reflect.DeepEqual(ref.Detail.IDs, []string{"ghost"}) || r.image(t) != before {
			t.Fatalf("ids %v, or the twin changed", ref.Detail.IDs)
		}
		if got := r.card(t, "w"); got.fields["open"] != "3" || got.fields["waived"] != "" {
			t.Fatalf("w changed: %+v", got)
		}
	})

	t.Run("a missing need and a dropped one", func(t *testing.T) {
		t.Parallel()
		r := setup(t)
		r.mustApply(t, intentStep(nil, Intent{Kind: IntentWaive, Card: "w", Needs: []string{"ghost", "d"}}))
		if got := r.card(t, "w"); got.fields["open"] != "1" || got.fields["waived"] != "ghost,d" {
			t.Fatalf("w: %+v; want open 1 (run is still open) and ghost and d waived", got)
		}
		if got := r.members("wait:ghost@0"); !reflect.DeepEqual(got, []string{"v"}) {
			t.Fatalf("wait:ghost %v, want v alone", got)
		}
		if _, ok := r.score("missing@0", "ghost"); !ok {
			t.Fatalf("ghost left missing while v still waits for it")
		}
		want := []string{"close " + missingJ + " ghost w", "close " + droppedJ + " d w"}
		if got := reqKinds(r.j.take()); !reflect.DeepEqual(got, want) {
			t.Fatalf("requests to J %v, want %v", got, want)
		}
		// The last waiter's waiver takes the need out of missing.
		r.mustApply(t, intentStep(nil, Intent{Kind: IntentWaive, Card: "v", Needs: []string{"ghost"}}))
		if r.members("wait:ghost@0") != nil || r.members("missing@0") != nil {
			t.Fatalf("after the last waiver: wait:ghost %v, missing %v; want both gone", r.members("wait:ghost@0"), r.members("missing@0"))
		}
		if got := r.card(t, "v"); got.fields["open"] != "0" || got.fields["waived"] != "ghost" {
			t.Fatalf("v: %+v", got)
		}
		// Waiving again finds no open judgment: nothing is written.
		r.j.take()
		r.writeKeys(t, Command("HDEL", key("jopen:w@0"), kindHash, missingJ+"|ghost", droppedJ+"|d"))
		before := r.image(t)
		again := r.mustApply(t, intentStep(nil, Intent{Kind: IntentWaive, Card: "w", Needs: []string{"ghost", "d"}}))
		if again.Reply.Changed != 0 || r.image(t) != before || len(r.j.take()) != 0 {
			t.Fatalf("a waiver with no judgment open wrote something")
		}
	})

	t.Run("a need whose judgment closed since the read", func(t *testing.T) {
		t.Parallel()
		r := setup(t)
		before := r.image(t)
		// run has no judgment open on w: it is waiting for run to land, and only the judgments are waived.
		reply := r.mustApply(t, intentStep(nil, Intent{Kind: IntentWaive, Card: "w", Needs: []string{"run"}}))
		if reply.Reply.Changed != 0 || r.image(t) != before {
			t.Fatalf("a waiver of a need with no judgment open wrote something")
		}
	})
}

// TestIntentsFoldOneEntryPerCard: every intent on one card folds into one
// entry, since layer 1 refuses a card named twice (TWICE): two needmet and a
// waive of one waiter are one entry, cards in one cell share an entry, and
// cards in two cells are two.
func TestIntentsFoldOneEntryPerCard(t *testing.T) {
	t.Parallel()
	r := newIntentRig(t)
	r.addRow(t, "s2")
	r.seed(t,
		seedCard{id: "a", col: "landed"}, seedCard{id: "b", col: "landed"}, seedCard{id: "c", col: "ready"},
		seedCard{id: "w", col: "waiting", needs: "a,b,c", open: "3"},
		seedCard{id: "x", col: "waiting", needs: "a", open: "1"},
		seedCard{id: "y", row: "s2", col: "waiting", needs: "a", open: "1"})
	r.drop(t, "s1:ready", "c")
	r.writeKeys(t, zadd("wait:a@0", "0", "w", "0", "x", "0", "y"), zadd("wait:b@0", "0", "w"),
		hset("jopen:w@0", droppedJ+"|c", "n1"))

	reply := r.mustApply(t, intentStep(nil,
		Intent{Kind: IntentNeedMet, Need: "a", Waiters: []string{"w", "x", "y"}},
		Intent{Kind: IntentNeedMet, Need: "b", Waiters: []string{"w"}},
		Intent{Kind: IntentWaive, Card: "w", Needs: []string{"c"}}))
	if reply.Reply.Changed != 3 || !reflect.DeepEqual(reply.Reply.ChangedPerEntry, []int{2, 1}) {
		t.Fatalf("changed %d in entries %v; want w and x in one entry (s1:waiting) and y in another", reply.Reply.Changed, reply.Reply.ChangedPerEntry)
	}
	if got := r.card(t, "w"); got.fields["open"] != "0" || got.fields["waived"] != "c" {
		t.Fatalf("w: %+v; want open 0 and c waived", got)
	}
	for _, id := range []string{"x", "y"} {
		if got := r.card(t, id); got.fields["open"] != "0" {
			t.Fatalf("%s open %q, want 0", id, got.fields["open"])
		}
	}
}

// TestIntentsRefusedShapeWritesNothing: an intent that is not well formed is
// REQUEST before anything is read (3, 1.0, 1.3.3), naming the intent; and on
// the twin the refused step leaves the whole-store image as it was.
func TestIntentsRefusedShapeWritesNothing(t *testing.T) {
	t.Parallel()
	needs := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "n" + strconv.Itoa(i)
		}
		return out
	}
	cases := []struct {
		name    string
		intents []Intent
		in      string
	}{
		{"an unknown kind", []Intent{{Kind: "hold", Card: "w"}}, "intent 0 (hold): is not a kind of intent"},
		{"a waitfor with no card", []Intent{{Kind: IntentWaitFor, Needs: []string{"n"}}}, "intent 0 (waitfor): names no card"},
		{"a waitfor with no needs", []Intent{{Kind: IntentWaitFor, Card: "w"}}, "names 0 needs; a card names 1 to 64"},
		{"a waitfor with 65 needs", []Intent{{Kind: IntentWaitFor, Card: "w", Needs: needs(NeedsPerCardMax + 1)}}, "names 65 needs; a card names 1 to 64"},
		{"a waitfor with an empty need", []Intent{{Kind: IntentWaitFor, Card: "w", Needs: []string{"n", ""}}}, "names a need with no id"},
		{"two waitfor of one card", []Intent{{Kind: IntentWaitFor, Card: "w", Needs: []string{"n"}}, {Kind: IntentWaitFor, Card: "w", Needs: []string{"m"}}}, "intent 1 (waitfor): admits w twice"},
		{"a waive with no needs", []Intent{{Kind: IntentWaive, Card: "w"}}, "intent 0 (waive): names 0 needs"},
		{"a needmet with no need", []Intent{{Kind: IntentNeedMet, Waiters: []string{"w"}}}, "intent 0 (needmet): names no need"},
		{"a needgone with an empty waiter", []Intent{{Kind: IntentNeedGone, Need: "n", Waiters: []string{"w", ""}}}, "names a waiter with no id"},
		{"more waiters than a chunk", []Intent{{Kind: IntentNeedMet, Need: "n", Waiters: needs(IntentWaitersMax)}, {Kind: IntentNeedGone, Need: "m", Waiters: []string{"x"}}},
			"intent 1 (needgone): the step's intents name 2001 waiters; at most 2000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := &State{Prefix: testPrefix, Epoch: "0", NowMS: "1", Names: testNames, Keys: &Keys{ks: newKeyspace()}}
			_, ref := deriveOver(st, &fixWorld{}, c.intents)
			if ref == nil || ref.Code != CodeRequest || ref.Phase != PhaseDerive || !strings.Contains(ref.Message, c.in) ||
				!strings.HasSuffix(ref.Message, "; nothing was changed") {
				t.Fatalf("refused %v; want REQUEST in derive naming %q", ref, c.in)
			}
		})
	}

	t.Run("on the twin", func(t *testing.T) {
		t.Parallel()
		r := newIntentRig(t)
		before := r.image(t)
		ref := r.mustRefuse(t, intentStep(nil, Intent{Kind: IntentWaitFor, Card: "w", Needs: needs(NeedsPerCardMax + 1)}))
		if ref.Code != CodeRequest || ref.Phase != PhaseDerive || r.image(t) != before {
			t.Fatalf("refused %s in %s, or the twin changed: %q", ref.Code, ref.Phase, ref.Message)
		}
	})
}

// TestIntentsRefuseABrokenWaiter: a card a sprint key says waits, whose
// record says otherwise, is DRIFT naming the card (1.3.5 quarantines it): the
// indexes and the records disagree, and the phase never writes on top of that.
func TestIntentsRefuseABrokenWaiter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		card seedCard
		keys func(r *intentRig) []Cmd
		step Intent
		in   string
	}{
		{"needmet on a card that is not waiting", seedCard{id: "w", col: "ready", needs: "n", open: "1"},
			func(*intentRig) []Cmd { return []Cmd{zadd("wait:n@0", "0", "w")} },
			Intent{Kind: IntentNeedMet, Need: "n", Waiters: []string{"w"}}, "w is in wait:n and is not a waiting card"},
		{"needmet on a card whose open is 0", seedCard{id: "w", col: "waiting", needs: "n", open: "0"},
			func(*intentRig) []Cmd { return []Cmd{zadd("wait:n@0", "0", "w")} },
			Intent{Kind: IntentNeedMet, Need: "n", Waiters: []string{"w"}}, "its open does not count the need"},
		{"needmet on a card whose open is not a number", seedCard{id: "w", col: "waiting", needs: "n", open: "-1"},
			func(*intentRig) []Cmd { return []Cmd{zadd("wait:n@0", "0", "w")} },
			Intent{Kind: IntentNeedMet, Need: "n", Waiters: []string{"w"}}, "its open does not count the need"},
		{"waive of a judgment on a card that is not waiting", seedCard{id: "w", col: "ready", needs: "n", open: "1"},
			func(*intentRig) []Cmd { return []Cmd{hset("jopen:w@0", droppedJ+"|n", "n1")} },
			Intent{Kind: IntentWaive, Card: "w", Needs: []string{"n"}}, "has a judgment open on n and is not a waiting card"},
		{"waive of a missing need the card is not in the set for", seedCard{id: "w", col: "waiting", needs: "n", open: "1"},
			func(*intentRig) []Cmd { return []Cmd{hset("jopen:w@0", missingJ+"|n", "n1")} },
			Intent{Kind: IntentWaive, Card: "w", Needs: []string{"n"}}, "has a judgment open on n and is not in wait:n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newIntentRig(t)
			r.seed(t, c.card)
			r.writeKeys(t, c.keys(r)...)
			before := r.image(t)
			ref := r.mustRefuse(t, intentStep(nil, c.step))
			if ref.Code != "DRIFT" || ref.Phase != PhaseDerive || !strings.Contains(ref.Message, c.in) || !reflect.DeepEqual(ref.Detail.IDs, []string{"w"}) {
				t.Fatalf("refused %s in %s: %q ids %v; want DRIFT naming w: %q", ref.Code, ref.Phase, ref.Message, ref.Detail.IDs, c.in)
			}
			if r.image(t) != before {
				t.Fatalf("a refused step changed the twin")
			}
		})
	}
}

// TestIntentsCommandsInPieces: a needmet of 2,000 waiters, the chunk, removes
// them from the wait set in commands of at most 1,000 members (L1 1.4), and
// the step applies: a single ZREM of 2,000 would be LIMIT at prepare.
func TestIntentsCommandsInPieces(t *testing.T) {
	t.Parallel()
	r := newIntentRig(t)
	r.seed(t, seedCard{id: "n", col: "landed"})
	var cards []seedCard
	var pairs, waiters []string
	for i := 0; i < IntentWaitersMax; i++ {
		id := "w" + strconv.Itoa(i)
		cards = append(cards, seedCard{id: id, col: "waiting", needs: "n", open: "1"})
		pairs = append(pairs, "0", id)
		waiters = append(waiters, id)
	}
	r.seed(t, cards...)
	for first := 0; first < len(pairs); first += 2 * maxPieces {
		r.writeKeys(t, zadd("wait:n@0", pairs[first:min(first+2*maxPieces, len(pairs))]...))
	}
	reply := r.mustApply(t, intentStep(nil, Intent{Kind: IntentNeedMet, Need: "n", Waiters: waiters}))
	if reply.Reply.Changed != IntentWaitersMax {
		t.Fatalf("changed %d cards, want %d", reply.Reply.Changed, IntentWaitersMax)
	}
	if r.members("wait:n@0") != nil {
		t.Fatalf("wait:n still holds %d waiters", len(r.members("wait:n@0")))
	}
	if got := r.card(t, "w1999"); got.fields["open"] != "0" {
		t.Fatalf("w1999 open %q, want 0", got.fields["open"])
	}
}

// TestIntentsBindingRefusesWithoutIt: a twin the derive phase was not bound to
// has Derive alone, which cannot carry the commands a step decides on wait:<n>
// and missing, nor read a waitfor's create entries: it refuses such a step
// CONFIG, in the derive phase and with nothing written, where dropping the
// commands would leave the indexes and the records disagreeing. A step whose
// intents decide nothing needs no binding.
func TestIntentsBindingRefusesWithoutIt(t *testing.T) {
	t.Parallel()
	r := newIntentRigWith(t, false)
	r.seed(t, seedCard{id: "n", col: "landed"}, seedCard{id: "w", col: "waiting", needs: "n", open: "1"})
	r.writeKeys(t, zadd("wait:n@0", "0", "w"))
	entries, admitted := admit("x", "n", "0")

	before := r.image(t)
	for name, req := range map[string]*Request{
		"a waitfor":                intentStep(entries, admitted),
		"a needmet with a waiter":  intentStep(nil, Intent{Kind: IntentNeedMet, Need: "n", Waiters: []string{"w"}}),
		"a needgone with a waiter": intentStep(nil, Intent{Kind: IntentNeedGone, Need: "n", Waiters: []string{"w"}}),
	} {
		ref := r.mustRefuse(t, req)
		if ref.Code != CodeConfig || ref.Phase != PhaseDerive {
			t.Errorf("%s: refused %s in %s (%q); want CONFIG in derive", name, ref.Code, ref.Phase, ref.Message)
		}
	}
	if r.image(t) != before {
		t.Fatalf("a refused step changed the twin")
	}

	// No waiter in the set: nothing is decided, and Derive serves.
	reply := r.mustApply(t, intentStep(nil, Intent{Kind: IntentNeedMet, Need: "other", Waiters: []string{"w"}}))
	if reply.Reply.Changed != 0 || r.image(t) != before {
		t.Fatalf("a needmet that finds no waiter wrote something")
	}
	st := &State{Prefix: testPrefix, Epoch: "0", NowMS: "1", Names: testNames, Keys: &Keys{ks: newKeyspace()}}
	entries2, notes, ref := Derive(st, []Intent{{Kind: IntentNeedMet, Need: "n", Waiters: []string{"w"}}}, &Before{})
	if ref != nil || len(entries2) != 0 || len(notes) != 0 {
		t.Fatalf("Derive on an empty state: %v %v %v; want nothing decided", entries2, notes, ref)
	}
}

// TestIntentsCommandsDoNotSurviveARefusedStep: the commands derive decided
// ride its own call. A step refused after derive (here by layer 1, at plan)
// leaves none behind for the next step's X.plan.
func TestIntentsCommandsDoNotSurviveARefusedStep(t *testing.T) {
	t.Parallel()
	r := newIntentRig(t)
	r.seed(t, seedCard{id: "n", col: "landed"}, seedCard{id: "w", col: "waiting", needs: "n", open: "1"})
	r.writeKeys(t, zadd("wait:n@0", "0", "w"))

	// derive decides to take w out of wait:n, and layer 1 refuses the step (a create of a card that exists).
	bad := tset.Entry{Kind: "create", Table: sprint.Work, To: "s1:waiting", IDs: []string{"n"}, Scores: []string{"9"}, About: []string{"n"}}
	before := r.image(t)
	ref := r.mustRefuse(t, intentStep([]tset.Entry{bad}, Intent{Kind: IntentNeedMet, Need: "n", Waiters: []string{"w"}}))
	if ref.Phase != PhasePlan || ref.Code != "EXISTS" {
		t.Fatalf("refused %s in %s; want EXISTS at plan", ref.Code, ref.Phase)
	}
	if r.image(t) != before {
		t.Fatalf("the refused step changed the twin")
	}
	// The next step, with no intents, applies nothing of derive's.
	r.addRow(t, "s2")
	if got := r.members("wait:n@0"); !reflect.DeepEqual(got, []string{"w"}) {
		t.Fatalf("wait:n after the next step: %v; the refused step's command was carried over", got)
	}
}

// TestIntentsKeepTheTracedPhaseOrder: binding the derive phase changes none of
// 1.0's order: open, before, X.pre, derive, J, parts, plan, log, X.plan,
// prepare, commit, each once.
func TestIntentsKeepTheTracedPhaseOrder(t *testing.T) {
	t.Parallel()
	r := newIntentRig(t)
	r.seed(t, seedCard{id: "n", col: "landed"}, seedCard{id: "w", col: "waiting", needs: "n", open: "1"})
	r.writeKeys(t, zadd("wait:n@0", "0", "w"))
	tr := &tracer{}
	r.tw.trace = tr.add
	r.mustApply(t, intentStep(nil, Intent{Kind: IntentNeedMet, Need: "n", Waiters: []string{"w"}}))
	if got := tr.list(); !sameStrings(got, PhaseOrder) {
		t.Fatalf("phases %v, want %v", got, PhaseOrder)
	}
}

// TestTwinDeriveWorldReadsObsFirst: a record S.before already read with the
// fields the phase asks for is used as read (the same state, read once, AL1),
// and any other goes to the table twin in one read: an id obs holds without
// the field, an id obs does not hold, and an id with no record at all.
func TestTwinDeriveWorldReadsObsFirst(t *testing.T) {
	t.Parallel()
	r := newIntentRig(t)
	r.seed(t, seedCard{id: "w", col: "waiting", needs: "a", open: "1"}, seedCard{id: "x", col: "waiting", needs: "b", open: "2"})

	obs := &Before{Records: map[string]map[string]tset.MemberRecord{sprint.Work: {
		// obs says w has open 5, which the store does not: proof the record came from obs.
		"w": {ID: "w", Exists: true, Revision: "1", Place: &tset.CellPlace{Row: "s1", Col: "waiting"},
			Fields: map[string]tset.FieldValue{"open": {Present: true, Value: "5"}}},
		// x is held without the field asked: it is read.
		"x": {ID: "x", Exists: true, Revision: "1", Place: &tset.CellPlace{Row: "s1", Col: "waiting"}},
		// an absent record is complete whatever fields are asked.
		"nope": {ID: "nope"},
	}}}
	w := twinDeriveWorld{tab: r.m, st: &State{Prefix: testPrefix, Epoch: "0"}, obs: obs}
	got, ref := w.records([]string{"w", "x", "nope", "unseen"}, []string{"open"})
	if ref != nil || len(got) != 4 {
		t.Fatalf("records: %v, %v", got, ref)
	}
	if got[0].Fields["open"].Value != "5" {
		t.Errorf("w open %q; want obs's 5", got[0].Fields["open"].Value)
	}
	if got[1].Fields["open"].Value != "2" || !got[1].Exists {
		t.Errorf("x: %+v; want the store's open 2", got[1])
	}
	if got[2].Exists {
		t.Errorf("nope exists")
	}
	if got[3].Exists || got[3].ID != "unseen" {
		t.Errorf("unseen: %+v; want a record that does not exist", got[3])
	}

	// A field obs does not hold for w is read from the store.
	got, ref = w.records([]string{"w"}, []string{"open", "needs"})
	if ref != nil || got[0].Fields["needs"].Value != "a" || got[0].Fields["open"].Value != "1" {
		t.Errorf("w with another field: %+v, %v; want the store's record", got, ref)
	}
}
