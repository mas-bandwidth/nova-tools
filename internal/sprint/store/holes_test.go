package store

// The five holes the model of the dirty-driven tick found (tla/DirtyTick.tla,
// tla/README-DirtyTick.md, "Findings"), each closed by a test that fails
// without the rule it names:
//
//	G1   a placement that does not read the fleet's status never ends the tick
//	G2   room read from a fleet table that is not up to date over-fills a machine
//	G3   a step but the pump writes the work table
//	W13  an update queues an entry each time it runs, whether or not it changed a row
//	W12  the tick's own entries to the work queue do not start the next tick
//
// The tests run on the Mem twin (holes_functional_test.go runs the same
// scenarios on a store). A tick is seen from the store's side: holeRec is a
// backend that notes every write a part of a tick applies and which part it
// was, and holeTick is the tick's table updates with every part's planner
// wrapped to say when it plans and on what state.

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// holeBackend is what the recorder wraps: a store that keeps the machine's
// records and blocks on its log (the Mem twin, and Redis).
type holeBackend interface {
	Backend
	KV
	LogWaiter
}

// holeWrite is one batch applied to a table, and the part of the tick whose
// step applied it.
type holeWrite struct {
	Part    string // table/part, "work/drain" for the pump's drain, "outside" between ticks
	Table   string // the logical table
	Members []ntable.BatchMemberEntry
}

// holeRec is the store's backend with every apply and every added row noted.
type holeRec struct {
	holeBackend
	mu     sync.Mutex
	cur    string
	writes []holeWrite
	rows   []holeWrite
}

func holeTable(table string) string {
	for _, t := range All {
		if strings.HasSuffix(table, t) {
			return t
		}
	}
	return table
}

func (r *holeRec) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	r.mu.Lock()
	r.writes = append(r.writes, holeWrite{r.cur, holeTable(m.Table), slices.Clone(m.Members)})
	r.mu.Unlock()
	return r.holeBackend.Apply(ctx, m)
}

func (r *holeRec) RowsAdd(ctx context.Context, table string, rows []string) error {
	r.mu.Lock()
	r.rows = append(r.rows, holeWrite{Part: r.cur, Table: holeTable(table)})
	r.mu.Unlock()
	return r.holeBackend.RowsAdd(ctx, table, rows)
}

func (r *holeRec) set(cur string) { r.mu.Lock(); r.cur = cur; r.mu.Unlock() }

// take is the writes noted so far, and forgets them.
func (r *holeRec) take() []holeWrite {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.writes
	r.writes = nil
	return out
}

// pumpParts are the parts of the work table's update, the pump: the only
// steps that write the work table while the machine runs.
var pumpParts = []string{"work/drain", "work/resolve", "work/deal", "work/accept"}

// holeStep is one planning of a part of the tick: its name and what it planned.
type holeStep struct {
	Part string
	Plan sprint.Plan
	// WorkChanges is the plan's changes of the work table that change a row.
	WorkChanges int
}

// holeTick is one harness's tick, watched.
type holeTick struct {
	h   *harness
	rec *holeRec
	// seen is every planning of the tick in progress, in order.
	seen []holeStep
	// onPlan is called before every part plans, on the state it plans on:
	// the state after every step before it in the tick.
	onPlan func(part string, s *sprint.Snapshot)
}

func newHoleTick(h *harness) *holeTick {
	x := &holeTick{h: h, rec: &holeRec{holeBackend: h.st.B.(holeBackend), cur: "outside"}}
	h.st.B = x.rec
	updates := make([]sprint.TableUpdate, len(sprint.TickTables))
	for i, u := range sprint.TickTables {
		parts := make([]sprint.TickPartDef, len(u.Parts))
		for j, p := range u.Parts {
			name := u.Table + "/" + p.Name
			if p.Fn == nil {
				parts[j] = p
				continue
			}
			fn := p.Fn
			parts[j] = sprint.TickPartDef{Name: p.Name, Fn: func(s *sprint.Snapshot, r sprint.TickReq) (sprint.Plan, int) {
				x.rec.set(name)
				if x.onPlan != nil {
					x.onPlan(name, s)
				}
				plan, due := fn(s, r)
				n := 0
				for _, un := range plan.Units {
					for _, ch := range un.Changes {
						if ch.Table == sprint.Work && changesRow(ch.Entry) {
							n++
						}
					}
				}
				x.seen = append(x.seen, holeStep{name, plan, n})
				return plan, due
			}}
		}
		updates[i] = sprint.TableUpdate{Table: u.Table, Parts: parts}
	}
	h.st.Updates = updates
	return x
}

// tick is one tick of the machine. Between ticks the recorder says "outside";
// in one, the drain is the work part no planner names.
func (x *holeTick) tick() TickResult {
	x.h.t.Helper()
	x.seen = nil
	x.rec.set("work/drain")
	res := x.h.machine()
	x.rec.set("outside")
	return res
}

// workWrites is the writes of the work table a tick's parts applied, by part.
func workWrites(ws []holeWrite) map[string]int {
	out := map[string]int{}
	for _, w := range ws {
		if w.Table == sprint.Work {
			out[w.Part] += len(w.Members)
		}
	}
	return out
}

// queueLen is the work table's queue.
func (h *harness) queueLen() int {
	h.t.Helper()
	q, err := h.st.B.QueueRead(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	return len(q)
}

// notesOf is how many notifications of a type the inbox holds.
func (h *harness) notesOf(typ string) int {
	h.t.Helper()
	notes, _, err := h.st.B.NotesSince(h.ctx, "", 100000)
	if err != nil {
		h.t.Fatal(err)
	}
	n := 0
	for _, x := range notes {
		if x.Type == typ && x.Kind != sprint.Decided && x.Kind != sprint.Acknowledged {
			n++
		}
	}
	return n
}

// tableRevs is the four tables' revisions as stored.
func (h *harness) tableRevs() [4]uint64 {
	h.t.Helper()
	s := h.table()
	return [4]uint64{s.Work.Revision, s.Readers.Revision, s.Merge.Revision, s.Fleet.Revision}
}

// play is one round of the outside world after a tick: the members' workers
// take and finish every card, the readers report every read ok, the
// coordinator accepts and the merger lands every stream, and the clock moves.
func (h *harness) play(members []string, streams ...string) {
	h.t.Helper()
	for _, m := range members {
		h.work(m)
	}
	h.readAll()
	for _, st := range streams {
		h.landAll(st)
	}
	h.tick(time.Second)
}

// heldOK says no up member holds more work cards, ready and working, than its
// width; "" when none does.
func heldOK(s *sprint.Snapshot) string {
	for _, m := range s.UpMembers() {
		if n := heldBy(s, m); n > s.Width(m) {
			return fmt.Sprintf("%s holds %d work cards, over its width %d", m, n, s.Width(m))
		}
	}
	return ""
}

// landed is the primaries landed in the three streams.
func landed(s *sprint.Snapshot) int {
	return s.Work.Count("s1", sprint.Landed) + s.Work.Count("s2", sprint.Landed) + s.Work.Count("s3", sprint.Landed)
}

// holesUp is two members of width 2 up and three streams of n primaries, the
// machine running: the fleet of the holes' scenarios, small enough that its
// room is used up at once.
func holesUp(h *harness, n int) {
	h.t.Helper()
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 2}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2", Width: 2}))
	for _, st := range []string{"s1", "s2", "s3"} {
		h.must(AddStep(sprint.AddReq{Stream: st, Count: n}))
	}
	h.startMachine()
}

// liveMembers is the machines that beat from now on.
func (h *harness) liveMembers(m ...string) {
	h.mu.Lock()
	h.live = m
	h.mu.Unlock()
	h.beat()
}

// holesRun plays 3n primaries to landed, one tick and one round of the
// outside world at a time. With lapse, m1's machine falls silent in the
// second round with its cards dealt and untaken, while m2's worker finishes
// its own; the clock passes the beat deadline, the next tick finds m1 down,
// and m1 beats again in the fifth round. after is called after every tick with
// the writes it applied.
func holesRun(x *holeTick, n int, lapse bool, after func(round int, res TickResult, ws []holeWrite)) {
	h := x.h
	t := h.t
	t.Helper()
	for round := 1; round <= 120; round++ {
		res := x.tick()
		ws := x.rec.take()
		if after != nil {
			after(round, res, ws)
		}
		if lapse && round == 2 {
			h.liveMembers("m2")
			h.work("m2")
			h.readAll()
			h.tick(sprint.BeatDeadline + time.Second)
			continue
		}
		if lapse && round == 5 {
			h.liveMembers("m1", "m2")
		}
		h.play([]string{"m1", "m2"}, "s1", "s2", "s3")
		if landed(h.snap()) == 3*n {
			return
		}
	}
	t.Fatalf("not landed after 120 rounds")
}

// ---- G1 ----------------------------------------------------------------------

// G1. A placement reads the fleet table's status in its own plan: a member the
// table has down or held gets no work card, whatever its machine beats, and
// with none up nothing is placed and the coordinator is told once; the tick
// ends. The model's G1 is a placement that does not read the status and
// trades with the fleet for ever (tla/README-DirtyTick.md).
func TestG1AWorkPlacementReadsTheMembersStatusFromTheFleetTableInThePlan(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		held, want []string
	}{
		{[]string{"m2"}, []string{"m1"}},
		{[]string{"m1"}, []string{"m2"}},
		{nil, []string{"m1", "m2"}},
		{[]string{"m1", "m2"}, nil},
	} {
		h := newHarness(t)
		h.setup(6)
		for _, m := range tc.held {
			h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: m}))
		}
		h.startMachine()
		x := newHoleTick(h)
		res := x.tick()
		by := dealtBy(res)
		dealt := 0
		for m, n := range by {
			dealt += n
			if !slices.Contains(tc.want, m) {
				t.Fatalf("held %v: %d cards dealt to %s, which the fleet table has down", tc.held, n, m)
			}
		}
		if len(tc.want) > 0 && dealt != 6 {
			t.Fatalf("held %v: %d cards dealt, want 6 (%v)", tc.held, dealt, by)
		}
		if len(tc.want) == 0 && (dealt != 0 || h.notesOf(sprint.NNoMember) != 1 || len(h.openOf(sprint.NNoMember)) != 1) {
			t.Fatalf("no member up: %d dealt, %d judgments written", dealt, h.notesOf(sprint.NNoMember))
		}
		if len(res.Order) > 12 {
			t.Fatalf("held %v: the tick took %d updates: %v", tc.held, len(res.Order), res.Order)
		}
		// the tick after is quiet whichever way: nothing placed, nothing written
		res = x.tick()
		if len(res.Moved()) > 0 || res.Notes() > 0 || h.notesOf(sprint.NNoMember) > 1 {
			t.Fatalf("held %v: the second tick moved %v", tc.held, res.Moved())
		}
	}
}

// G1. The placement is read from the fleet table as the plan finds it, every
// time: flip a member's status in the table between two plans of the same
// part and the second plan follows it.
func TestG1AWorkPlacementFollowsTheFleetTableBetweenPlans(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(4)
	h.must(ResolveStep(sprint.ResolveReq{Sel: sprint.Sel{Limit: 100}}))
	ctl := func(m, status string) {
		h.poke(sprint.Fleet, ntable.BatchMemberEntry{ID: sprint.CtlID(m), Set: map[string]string{"status": status}})
	}
	members := func() map[string]int {
		out := map[string]int{}
		p, _ := sprint.TickDeal(h.table(), sprint.TickReq{})
		for _, u := range p.Units {
			for _, ch := range u.Changes {
				if ch.Table == sprint.Fleet && ch.Entry.Create != nil {
					out[ch.Entry.Create.Row]++
				}
			}
		}
		return out
	}
	if got := members(); got["m1"] == 0 || got["m2"] == 0 || len(got) != 2 {
		t.Fatalf("both up: %v", got)
	}
	ctl("m1", sprint.Down)
	if got := members(); got["m1"] != 0 || got["m2"] != 4 {
		t.Fatalf("m1 down in the table: %v", got)
	}
	ctl("m1", sprint.Up)
	ctl("m2", sprint.Down)
	if got := members(); got["m2"] != 0 || got["m1"] != 4 {
		t.Fatalf("m2 down in the table: %v", got)
	}
	ctl("m1", sprint.Down)
	if got := members(); len(got) != 0 {
		t.Fatalf("none up in the table: %v", got)
	}
}

// G1. A read is placed on a reader, and no fleet row enters it: the ask's plan
// over the same state is the same with the fleet table empty, and with every
// member held the reads are still asked in a tick that ends. A read that
// waited on a host's status would trade with the fleet (the model's G1, in
// its readers).
func TestG1AReadsPlacementNeverDependsOnAFleetRow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	x := newHoleTick(h)
	x.tick()
	h.work("m1")
	h.work("m2")
	s := h.snap()
	if n := len(s.Work.Column(sprint.Review)); n != 2 {
		t.Fatalf("%d in review, want 2", n)
	}
	want, _ := sprint.TickAsk(s, sprint.TickReq{})
	if len(want.Units) != 2 {
		t.Fatalf("the ask plans %d units, want 2", len(want.Units))
	}
	empty := *s
	empty.Fleet = sprint.NewTable(s.Fleet.Name)
	got, _ := sprint.TickAsk(&empty, sprint.TickReq{})
	if !reflect.DeepEqual(got.Units, want.Units) || !reflect.DeepEqual(got.Notes, want.Notes) {
		t.Fatalf("the ask's plan depends on the fleet table:\n got %+v\nwant %+v", got.Units, want.Units)
	}
	h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m2"}))
	res := x.tick()
	if n := len(h.table().Readers.Column(sprint.Asked)); n != 4 || len(res.Order) > 12 {
		t.Fatalf("with no member up %d reads are asked, want 4, in %v", n, res.Order)
	}
}

// G1. A member that falls silent after the pump placed cards on it (the table
// still had it up) does not keep the tick going: every tick ends within the
// model's bound of 12 updates, the fleet takes the member down, and the next
// tick places nothing on it.
func TestG1ALapseMidTickEndsTheTickAndTheNextPlacesNothingOnTheMember(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	holesUp(h, 8)
	x := newHoleTick(h)
	holesRun(x, 8, true, func(round int, res TickResult, _ []holeWrite) {
		if len(res.Order) > 12 {
			t.Fatalf("round %d: the tick took %d updates: %v", round, len(res.Order), res.Order)
		}
		if round == 3 {
			if st := h.table().MemberCtl("m1").F("status"); st != sprint.Down {
				t.Fatalf("m1 is %s after the tick that found it silent", st)
			}
		}
		if round == 4 {
			if n := dealtBy(res)["m1"]; n != 0 {
				t.Fatalf("round 4 dealt %d cards to m1, down in the table", n)
			}
		}
	})
}

// ---- G2 ----------------------------------------------------------------------

// G2. The deal writes the fleet in the pump's own step: by the time the next
// update plans, every primary the pump moved to working has its work card in
// the fleet table (the fleet holds exactly the cards the working primaries
// hold), at every step of the tick; and no step after the pump's deal creates a
// work card on a member. The room a later update reads is the room the pump
// left.
func TestG2TheDealWritesTheFleetInThePumpsOwnStep(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	holesUp(h, 8)
	x := newHoleTick(h)
	checked := 0
	x.onPlan = func(part string, s *sprint.Snapshot) {
		if strings.HasPrefix(part, "work/") {
			return
		}
		held := 0
		for _, m := range s.Fleet.Rows() {
			held += heldBy(s, m)
		}
		if working := len(s.Work.Column(sprint.Working)); held != working {
			t.Fatalf("before %s the fleet holds %d work cards and %d primaries are working: the deal's fleet write is not in the pump's step", part, held, working)
		}
		checked++
	}
	created := 0
	holesRun(x, 8, false, func(_ int, _ TickResult, ws []holeWrite) {
		for _, w := range ws {
			if w.Table != sprint.Fleet {
				continue
			}
			for _, e := range w.Members {
				if e.Create != nil && w.Part == "work/deal" {
					created++
				}
				if e.Create != nil && w.Part != "work/deal" {
					t.Fatalf("%s created work card %s: only the pump's deal places new work on a member", w.Part, e.ID)
				}
			}
		}
	})
	if created < 24 || checked < 10 {
		t.Fatalf("the watch saw %d work cards created by the deal and checked %d steps: it saw too little", created, checked)
	}
}

// G2. No machine is over its width at any step of any tick: the width is
// checked on the state every update plans on, after every settle step, and
// after the tick.
func TestG2NoMachineIsOverItsWidthAtAnyStepOfATick(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	holesUp(h, 8)
	x := newHoleTick(h)
	steps := 0
	x.onPlan = func(part string, s *sprint.Snapshot) {
		steps++
		if why := heldOK(s); why != "" {
			t.Fatalf("before %s: %s", part, why)
		}
	}
	holesRun(x, 8, false, func(round int, _ TickResult, _ []holeWrite) {
		if why := heldOK(h.table()); why != "" {
			t.Fatalf("round %d after the tick: %s", round, why)
		}
	})
	if steps < 40 {
		t.Fatalf("%d steps checked", steps)
	}
}

// G2. A machine that falls silent with work on it, while the others are at
// their width: the pump never deals to a machine at or over its width, in the
// tick after the lapse or any other (a deal creates at most the room the fleet
// table showed the pump, per machine). The cards of a down member go to the
// others past their width, as the fleet's down rule has always placed them
// (every_row_test.go), so a machine may hold more than its width after a
// lapse until its work finishes; that is the down rule's, and this test pins
// that the pump adds nothing to it.
func TestG2TheDealNeverGivesAMachineMoreThanItsRoomAfterALapse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	holesUp(h, 8)
	x := newHoleTick(h)
	held := map[string]int{}
	x.onPlan = func(part string, s *sprint.Snapshot) {
		if part != "work/deal" {
			return
		}
		for _, m := range s.UpMembers() {
			held[m] = heldBy(s, m)
		}
	}
	over, dealt := 0, 0
	holesRun(x, 8, true, func(round int, res TickResult, ws []holeWrite) {
		made := map[string]int{}
		for _, w := range ws {
			for _, e := range w.Members {
				if w.Table == sprint.Fleet && w.Part == "work/deal" && e.Create != nil {
					made[e.Create.Row]++
				}
			}
		}
		for m, n := range made {
			dealt += n
			if room := max(0, 2-held[m]); n > room {
				t.Fatalf("round %d: the deal gave %s %d cards, its room was %d (it held %d of width 2)", round, m, n, room, held[m])
			}
		}
		for _, m := range []string{"m1", "m2"} {
			over = max(over, heldBy(h.table(), m)-2)
		}
		clear(held)
	})
	if dealt < 24 {
		t.Fatalf("the watch saw %d cards dealt", dealt)
	}
	t.Logf("the most a machine held over its width after a tick with a lapse: %d (the down rule's)", over)
}

// ---- G3 ----------------------------------------------------------------------

// G3. No step of a tick but the pump's writes the work table: walking every
// step of a whole sprint's ticks (with a machine lapsing, the readers asked,
// the cards accepted), a batch applied to the work table is always a part of
// the pump, never another's; the changes of the work table the other steps
// plan are the log's queue, and the next pump drains them.
func TestG3OnlyThePumpWritesTheWorkTableAndTheQueueHoldsTheRest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	holesUp(h, 8)
	x := newHoleTick(h)
	// the stored work table's revision before the first step after the pump
	// is what every step after it finds
	var after uint64
	x.onPlan = func(part string, s *sprint.Snapshot) {
		if strings.HasPrefix(part, "work/") {
			return
		}
		rev := h.table().Work.Revision
		if after == 0 {
			after = rev
		} else if rev != after {
			t.Fatalf("the stored work table moved after the pump, before %s: revision %d, the pump left %d", part, rev, after)
		}
	}
	planned := map[string]int{}
	pumped := map[string]int{}
	holesRun(x, 8, true, func(round int, res TickResult, ws []holeWrite) {
		after = 0
		for part, n := range workWrites(ws) {
			if !slices.Contains(pumpParts, part) {
				t.Fatalf("round %d: %s wrote %d entries of the work table: only the pump does (%v)", round, part, n, pumpParts)
			}
			pumped[part] += n
		}
		for _, w := range x.rec.rows {
			if w.Table == sprint.Work && w.Part != "outside" {
				t.Fatalf("round %d: %s added rows to the work table", round, w.Part)
			}
		}
		queued := 0
		for _, st := range x.seen {
			if !slices.Contains(pumpParts, st.Part) {
				planned[st.Part] += st.WorkChanges
				queued += st.WorkChanges
			}
		}
		if queued > 0 && h.queueLen() == 0 {
			t.Fatalf("round %d: updates after the pump planned changes of the work table and the queue is empty", round)
		}
	})
	if planned["readers/ask"] == 0 {
		t.Fatalf("the parts that planned changes of the work table after the pump: %v: the walk saw too little", planned)
	}
	for _, part := range []string{"work/drain", "work/deal", "work/accept"} {
		if pumped[part] == 0 {
			t.Fatalf("the pump's %s wrote nothing in the whole sprint: %v", part, pumped)
		}
	}
	t.Logf("planned after the pump, queued: %v; applied by the pump: %v", planned, pumped)
}

// G3. The walk above names a step that writes the work table and is not the
// pump: the merge's step marked as the pump, run as the merge update's part,
// writes the work table and is named.
func TestG3TheWalkNamesAStepThatWritesTheWorkTableAndIsNotThePump(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	x := newHoleTick(h)
	x.tick()
	h.work("m1")
	h.work("m2")
	x.tick()
	h.readAll()
	x.tick()
	x.rec.take()
	merge := MergeStep(sprint.MergeReq{Stream: "s1", Batch: 2})
	merge.Pump = true
	x.rec.set("merge/resume")
	h.must(merge)
	x.rec.set("outside")
	bad := 0
	for part, n := range workWrites(x.rec.take()) {
		if !slices.Contains(pumpParts, part) {
			bad += n
		}
	}
	if bad == 0 {
		t.Fatalf("the merge step marked as the pump wrote no entry of the work table the walk names")
	}
}

// ---- W13 ---------------------------------------------------------------------

// W13. A tick with nothing to do ends after the four first updates with every
// queue empty and no note: no table is written, no entry is queued, the tick
// says it was idle and writes no tick-end note, for as many ticks as there is
// nothing to do, however the clock moves and the machines beat. The same with
// no member up and cards waiting for one: the judgment is written once.
func TestW13ATickWithNothingToDoEndsAfterTheFourFirstUpdates(t *testing.T) {
	t.Parallel()
	for _, hold := range []bool{false, true} {
		h := newHarness(t)
		h.setup(3)
		if hold {
			h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m1"}))
			h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m2"}))
		}
		h.startMachine()
		x := newHoleTick(h)
		first := x.tick() // deals (or, with no member up, says so once)
		if want := map[bool]int{false: 0, true: 1}[hold]; first.TickEnd != want {
			t.Fatalf("hold=%v: the first tick's note count %d, want %d", hold, first.TickEnd, want)
		}
		x.rec.take()
		ends, revs := h.notesOf(sprint.NTickEnd), h.tableRevs()
		for i := 0; i < 5; i++ {
			h.tick(time.Duration(i+1) * time.Second)
			res := x.tick()
			if want := []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet, "end"}; !slices.Equal(res.Order, want) {
				t.Fatalf("hold=%v tick %d updated %v, want %v", hold, i+1, res.Order, want)
			}
			if !res.Idle || len(res.Parts) > 0 || res.TickEnd != 0 {
				t.Fatalf("hold=%v tick %d was not idle: parts %v, tick-end %d", hold, i+1, res.Parts, res.TickEnd)
			}
			if ws := x.rec.take(); len(ws) > 0 {
				t.Fatalf("hold=%v tick %d applied %d batches: %+v", hold, i+1, len(ws), ws[0])
			}
			if n := h.queueLen(); n != 0 {
				t.Fatalf("hold=%v tick %d left %d entries in the work queue", hold, i+1, n)
			}
			if got := h.notesOf(sprint.NTickEnd); got != ends {
				t.Fatalf("hold=%v tick %d wrote a tick-end note (%d, was %d)", hold, i+1, got, ends)
			}
		}
		// the work, readers and merge tables are as they were (the fleet's
		// cells follow the beats, which are not an update's writes)
		if got := h.tableRevs(); got[0] != revs[0] || got[1] != revs[1] || got[2] != revs[2] {
			t.Fatalf("hold=%v: the tables' revisions %v -> %v across five idle ticks", hold, revs, got)
		}
	}
}

// W13. An update queues an entry only when it changed a row: two updates whose
// plans write a field to the value it holds (and two that only guard a card)
// dirty nothing, and the tick ends after its four first updates. An update
// that changes a row is still acted on at once, so the watch is not blind.
func TestW13AnUpdateThatChangedNothingQueuesNothing(t *testing.T) {
	t.Parallel()
	same := func(table string, guardOnly bool) sprint.TickPartFn {
		return func(s *sprint.Snapshot, _ sprint.TickReq) (sprint.Plan, int) {
			c, field := s.MemberCtl("m1"), "status"
			if table == sprint.Merge {
				c, field = s.StreamCtl("s1"), "state"
			}
			e := ntable.BatchMemberEntry{ID: c.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}}}
			if !guardOnly {
				e.Set = map[string]string{field: c.F(field)}
			}
			return sprint.Plan{Units: []sprint.Unit{{Key: c.ID, Changes: []sprint.Change{{Table: table, Entry: e}}, Moved: "again " + table}}}, 0
		}
	}
	first := []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet, "end"}
	for _, guardOnly := range []bool{false, true} {
		h := newHarness(t)
		h.setup(1)
		h.startMachine()
		h.st.Updates = []sprint.TableUpdate{
			{Table: sprint.Work}, {Table: sprint.Readers},
			{Table: sprint.Merge, Parts: []sprint.TickPartDef{{Name: "to-fleet", Fn: same(sprint.Fleet, guardOnly)}}},
			{Table: sprint.Fleet, Parts: []sprint.TickPartDef{{Name: "to-merge", Fn: same(sprint.Merge, guardOnly)}}},
		}
		res, err := h.st.Tick(h.ctx)
		if err != nil {
			t.Fatalf("guardOnly=%v: %v", guardOnly, err)
		}
		if !slices.Equal(res.Order, first) {
			t.Fatalf("guardOnly=%v: the tick updated %v, want %v", guardOnly, res.Order, first)
		}
	}
	// one that changes a row is acted on at once: the tick updates the table it wrote
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.st.Updates = []sprint.TableUpdate{
		{Table: sprint.Work}, {Table: sprint.Readers},
		{Table: sprint.Merge},
		{Table: sprint.Fleet, Parts: []sprint.TickPartDef{{Name: "to-merge", Fn: ping(sprint.Merge, 1)}}},
	}
	res := h.machine()
	if want := []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet, sprint.Merge, "end"}; !slices.Equal(res.Order, want) {
		t.Fatalf("an update that changed a row: the tick updated %v, want %v", res.Order, want)
	}
}

// ---- W12 ---------------------------------------------------------------------

// W12. The tick's own entries to the work queue wake the next tick: a landing
// the coordinator records late in a tick (in the merge update, after the pump
// has run) is pumped by the next tick with no outside event. The loop waits
// on the log from the last line it has seen before a tick began (run's
// pace): the lines the tick wrote are after it, the wait returns at once, and
// the tick runs again; a cursor read after the tick sees none of them and
// would strand the queue (the model's W12).
func TestW12TheTicksOwnEntriesToTheWorkQueueWakeTheNextTick(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	x := newHoleTick(h)
	x.tick()
	h.work("m1")
	h.work("m2")
	x.tick()
	h.readAll()
	x.tick() // accepted: merging, queued to merge
	if st := h.table().StateOf("s1-1"); st != sprint.Merging {
		t.Fatalf("s1-1 is %s, want merging", st)
	}
	// the landing is recorded late in the next tick, in the merge update
	fired := false
	inner := h.st.Updates[2].Parts
	h.st.Updates[2].Parts = append([]sprint.TickPartDef{{Name: "land", Fn: func(s *sprint.Snapshot, r sprint.TickReq) (sprint.Plan, int) {
		if !fired {
			fired = true
			h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 2}))
		}
		return sprint.Plan{}, 0
	}}}, inner...)
	before, err := h.st.LogTail(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	x.tick()
	if !fired || h.queueLen() == 0 {
		t.Fatalf("the landing was not recorded in the tick: fired %v, queue %d", fired, h.queueLen())
	}
	if st := h.table().StateOf("s1-1"); st != sprint.Merging {
		t.Fatalf("the stored s1-1 is %s: the landing wrote the work table itself", st)
	}
	after, err := h.st.LogTail(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	// the loop's wait, from the cursor it held before the tick
	if _, woke, err := h.st.WaitLog(h.ctx, 0, before, time.Millisecond); err != nil || !woke {
		t.Fatalf("the wait from the last line seen before the tick did not wake (woke %v, %v): the queue would wait for an outside event", woke, err)
	}
	// a wait from the tail read after the tick is the loop the model's W12 names
	if _, woke, _ := h.st.WaitLog(h.ctx, 0, after, time.Millisecond); woke {
		t.Fatalf("the log woke a wait that had seen every line: the test's cursors prove nothing")
	}
	// the next tick, with no outside event before it
	h.st.Updates[2].Parts = inner
	x.tick()
	if n := h.table().Work.Count("s1", sprint.Landed); n != 2 {
		t.Fatalf("%d landed after the next tick, want 2: the queued landing was not pumped without an outside event", n)
	}
	if n := h.queueLen(); n != 0 {
		t.Fatalf("the queue holds %d entries after the pump", n)
	}
	h.clean("landed")
}

// W12. The wake is the queue's own line: an update after the pump whose only
// write is one change of the work table (a field set on a working primary)
// writes the queue one entry and the log one line, and that line alone wakes
// the wait from before the tick; the next tick's pump applies the change.
func TestW12AQueueEntryIsALineOnTheLogThatWakesTheNextTick(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	x := newHoleTick(h)
	x.tick() // deals both; nothing queued
	if n := h.queueLen(); n != 0 {
		t.Fatalf("the queue holds %d entries after a tick that only dealt", n)
	}
	fired := false
	late := func(s *sprint.Snapshot, _ sprint.TickReq) (sprint.Plan, int) {
		c := s.Work.Column(sprint.Working)[0]
		if c.F("late") == "yes" {
			return sprint.Plan{}, 0
		}
		fired = true
		e := ntable.BatchMemberEntry{ID: c.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}},
			Set: map[string]string{"late": "yes"}}
		return sprint.Plan{Units: []sprint.Unit{{Key: c.ID, Stream: c.Row, Changes: []sprint.Change{{Table: sprint.Work, Entry: e}}, Moved: c.ID + " late"}}}, 0
	}
	h.st.Updates[2].Parts = append([]sprint.TickPartDef{{Name: "late", Fn: late}}, h.st.Updates[2].Parts...)
	before, err := h.st.LogTail(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	x.tick()
	lines, _, err := h.st.B.LogSince(h.ctx, before, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !fired || h.queueLen() != 1 || len(lines) != 1 || lines[0].Kind != sprint.LineQueued {
		t.Fatalf("fired %v, queue %d, lines since the tick began: %+v: want the one queued change and its one line", fired, h.queueLen(), lines)
	}
	if _, woke, err := h.st.WaitLog(h.ctx, 0, before, time.Millisecond); err != nil || !woke {
		t.Fatalf("the queued change's line did not wake the wait from before the tick (woke %v, %v)", woke, err)
	}
	x.tick()
	s := h.table()
	if n := h.queueLen(); n != 0 || s.Work.Column(sprint.Working)[0].F("late") != "yes" && s.Work.Column(sprint.Working)[1].F("late") != "yes" {
		t.Fatalf("after the next tick the queue holds %d and the change is not applied", n)
	}
}

// W12. The loop's own pacing over a whole sprint: after every tick, the wait
// from the cursor before it wakes while the work queue holds anything, so the
// queue is never left with nothing to start the tick that drains it.
func TestW12TheLoopIsWokenWhileTheWorkQueueHoldsAnything(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	holesUp(h, 4)
	x := newHoleTick(h)
	cursor, err := h.st.LogTail(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	woken := 0
	holesRun(x, 4, true, func(round int, res TickResult, _ []holeWrite) {
		tail, woke, err := h.st.WaitLog(h.ctx, res.Epoch, cursor, time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if q := h.queueLen(); q > 0 && !woke {
			t.Fatalf("round %d: the tick left %d entries in the work queue and the wait from the cursor before it did not wake", round, q)
		} else if q > 0 {
			woken++
		}
		cursor = tail
	})
	if woken == 0 {
		t.Fatalf("no tick left anything in the queue")
	}
}
