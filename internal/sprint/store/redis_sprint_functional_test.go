//go:build functional

package store

// Functional tests of the store binding against a real Redis of the test's own
// (testutil.Start) with the table layer's functions loaded (fn.Load): the
// paths the binding has, and the Mem backend's tests cover, that no functional
// test covered: a whole sprint driven by ticks, a multi-table step cut and
// repaired, a late writer after a clear, the exact teardown, writers racing on
// the fence, the caller's operation ids, the machine's records, the
// coordinator's records, and the reminders.
//
// These run only in the functional tier (make test-functional-container).
// The clock is the test's own (the harness's now), started at the real time
// of the run: nothing here sleeps for a duration the test names.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveClient is a client of a Redis the test started, with the table layer's
// functions loaded.
func liveClient(t *testing.T) *redis.Client {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	require.NoError(t, fn.Load(context.Background(), c))
	return c
}

// liveHarnessOn is the harness over a sprint of the prefix on the client's
// Redis, with a clock the test moves (h.tick), initialised, readers added.
func liveHarnessOn(t *testing.T, c *redis.Client, prefix string) *harness {
	t.Helper()
	h := &harness{t: t, ctx: context.Background(), now: time.Now().UTC().Truncate(time.Second), live: []string{"m1", "m2"}}
	now := func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now }
	names := sprint.Names{Prefix: prefix}
	h.st = &Store{B: &Redis{C: c, Names: names, Now: now}, Names: names, Actor: "tester", Now: now}
	require.NoError(t, h.st.Init(h.ctx))
	require.NoError(t, h.st.B.RowsAdd(h.ctx, names.Table(sprint.Readers), []string{"reader-a", "reader-b", "reader-c"}))
	require.NoError(t, h.st.B.SetCoordinator(h.ctx, h.st.Actor))
	h.beat()
	return h
}

func liveHarness(t *testing.T) (*harness, *redis.Client) {
	t.Helper()
	c := liveClient(t)
	return liveHarnessOn(t, c, "f-"), c
}

// liveOpen is the open judgments of a type at the sprint's epoch.
func liveOpen(h *harness, typ string) []sprint.Open {
	h.t.Helper()
	p, err := h.st.Pinned(h.ctx)
	require.NoError(h.t, err)
	open, err := p.B.OpenNotes(h.ctx)
	require.NoError(h.t, err)
	var out []sprint.Open
	for _, o := range open {
		if o.Note.Type == typ {
			out = append(out, o)
		}
	}
	return out
}

// liveNotes is every notification of the sprint's epoch, oldest first.
func liveNotes(h *harness) []sprint.Note {
	h.t.Helper()
	p, err := h.st.Pinned(h.ctx)
	require.NoError(h.t, err)
	notes, _, err := p.B.NotesSince(h.ctx, "", 100000)
	require.NoError(h.t, err)
	return notes
}

func liveWritten(h *harness, typ string) int {
	n := 0
	for _, x := range liveNotes(h) {
		if x.Type == typ && x.Kind != sprint.Decided && x.Kind != sprint.Acknowledged {
			n++
		}
	}
	return n
}

// liveKeys is every key of the Redis, by SCAN, sorted.
func liveKeys(t *testing.T, c *redis.Client) []string {
	t.Helper()
	var keys []string
	var cur uint64
	for {
		ks, next, err := c.Scan(context.Background(), cur, "*", 500).Result()
		require.NoError(t, err)
		keys = append(keys, ks...)
		if cur = next; cur == 0 {
			break
		}
	}
	sort.Strings(keys)
	return keys
}

// A whole small sprint to landed, driven by ticks: two streams, a dependency
// inside the stream, a dependency across streams, a sentinel that stops the
// stream until the coordinator releases it, two members, three readers; the
// machine's records, the inbox, the cursor and the stream's progress as they
// are on the real store.
func TestRedisASprintToLandedByTicks(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	require.NoError(t, h.st.B.SetCoordinator(h.ctx, "tester"))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"a"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"b"}, Needs: []string{"a"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"after"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"y"}, Needs: []string{"b"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"z"}}))
	h.clean("setup")
	line := h.st.MachineLine(h.ctx)
	require.Equal(t, "machine: STOPPED", line, "a new sprint: %q", line)
	// STOPPED: the tick moves nothing.
	if res := h.machine(); len(res.Moved()) != 0 || res.State != Stopped {
		require.Fail(t, fmt.Sprintf("a stopped tick: %+v", res))
	}
	if h.state("a") != sprint.Waiting && h.state("a") != sprint.Ready {
		require.Fail(t, fmt.Sprintf("a stopped tick moved a: %s", h.state("a")))
	}
	h.startMachine()

	round := func() {
		h.machine()
		h.work("m1")
		h.work("m2")
		h.machine()
		h.readAll()
		for _, st := range []string{"s1", "s2"} {
			// a landed stream has nothing to merge: the merge is refused, by the lifecycle
			if h.snap().StreamCtl(st).F("state") != sprint.StreamLanded {
				h.landAll(st)
			}
		}
	}
	reached := func() bool { return len(liveOpen(h, sprint.NSentinelReached)) == 1 }
	for i := 0; i < 12; i++ {
		round()
		h.clean(fmt.Sprintf("round %d", i))
		if h.state("y") == sprint.Landed && h.state("z") == sprint.Landed && reached() {
			break
		}
		h.tick(time.Second)
	}
	h.machine()
	for _, id := range []string{"a", "b", "y", "z"} {
		require.Equal(t, sprint.Landed, h.state(id), "%s is %s before the sentinel", id, h.state(id))
	}
	if h.state("stop") != sprint.Waiting || h.state("after") != sprint.Waiting || !reached() {
		require.Fail(t, fmt.Sprintf("at the sentinel: stop %s after %s reached %d", h.state("stop"), h.state("after"), len(liveOpen(h, sprint.NSentinelReached))))
	}
	// The tick does nothing twice: a further tick moves and writes nothing.
	before := liveWritten(h, sprint.NSentinelReached)
	h.quiet("at the sentinel")
	require.Equal(t, before, liveWritten(h, sprint.NSentinelReached), "a second tick wrote the sentinel's judgment again")
	// The coordinator releases it; the tick moves what waited behind it.
	h.must(ReleaseStep(sprint.ReleaseReq{IDs: []string{"stop"}, Reason: "a, b, y and z are green", Coordinator: "tester", Who: "tester"}))
	for i := 0; i < 8 && h.state("after") != sprint.Landed; i++ {
		round()
		h.clean(fmt.Sprintf("after the release, round %d", i))
		h.tick(time.Second)
	}
	s := h.snap()
	for _, id := range []string{"a", "b", "stop", "after", "y", "z"} {
		require.Equal(t, sprint.Landed, s.StateOf(id), "%s is %s at the end", id, s.StateOf(id))
	}
	for _, st := range []string{"s1", "s2"} {
		require.Equal(t, string(sprint.StreamLanded), s.StreamCtl(st).F("state"), "stream %s is %s", st, s.StreamCtl(st).F("state"))
	}
	require.False(t, reached(), "the sentinel's judgment is still open after its release")
	h.clean("landed")
	// The machine's records: state, heartbeat with its ticks.
	m, hb, err := h.st.Machine(h.ctx)
	if err != nil || !m.Running() || hb.Ticks == 0 || hb.Error != "" {
		require.Fail(t, fmt.Sprintf("the machine's records: %+v %+v %v", m, hb, err))
	}
	line = h.st.MachineLine(h.ctx)
	require.Equal(t, "machine: running", line, "machine line: %q", line)
	// The inbox stream and its cursor.
	notes, ids, err := h.st.B.NotesSince(h.ctx, "", 100000)
	if err != nil || len(notes) < 4 || len(ids) != len(notes) {
		require.Fail(t, fmt.Sprintf("notifications: %d %v", len(notes), err))
	}
	seen := map[string]bool{}
	for _, n := range notes {
		seen[n.Type] = true
	}
	for _, typ := range []string{sprint.NSentinelReached, sprint.NStreamLanded, sprint.NMachineStarted} {
		require.True(t, seen[typ], "no %q notification among %v", typ, seen)
	}
	require.NoError(t, h.st.B.SetCursor(h.ctx, ids[len(ids)-1]))
	if cur, err := h.st.B.Cursor(h.ctx); err != nil || cur != ids[len(ids)-1] {
		require.Fail(t, fmt.Sprintf("cursor %q %v", cur, err))
	}
	if rest, _, err := h.st.B.NotesSince(h.ctx, ids[len(ids)-1], 100); err != nil || len(rest) != 0 {
		require.Fail(t, fmt.Sprintf("notifications after the cursor: %d %v", len(rest), err))
	}
	v, err := h.st.Inbox(h.ctx, time.Hour, time.Hour, 100)
	if err != nil || v.Cursor != ids[len(ids)-1] || len(v.Recent) != 0 {
		require.Fail(t, fmt.Sprintf("inbox after the cursor: %+v %v", v, err))
	}
	// Progress: each stream has a clock.
	clocks, err := h.st.StreamClocks(h.ctx)
	require.NoError(t, err, "stream clocks: %+v %v", clocks, err)
	require.GreaterOrEqual(t, len(clocks), 2, "stream clocks: %+v %v", clocks, err)
	for _, c := range clocks {
		require.False(t, c.Progress.IsZero(), "stream %s has no progress clock", c.Stream)
	}
	// Stop and start are idempotent and recorded once.
	notesBefore := len(liveNotes(h))
	h.stopMachine()
	h.stopMachine()
	line = h.st.MachineLine(h.ctx)
	require.Equal(t, "machine: STOPPED", line, "stopped: %q", line)
	n := len(liveNotes(h))
	require.Equal(t, notesBefore+1, n, "stopping twice wrote %d notifications", n-notesBefore)
}

// outsideWriteLive changes a primary's brief on the work table, as a writer
// outside the sprint's fence, just before the step's work manifest applies.
func (h *harness) outsideWriteLive(id string) *racer {
	table := h.st.Names.Table(sprint.Work)
	return &racer{Backend: h.st.B, at: "apply " + table, do: func() {
		s := h.snap()
		p := s.Work.Card(id)
		_, err := h.st.B.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: table, Epoch: fmt.Sprint(s.Work.Epoch), ExpectedTableRevision: fmt.Sprint(s.Work.Revision),
			OperationID: "outside-" + id, Actor: "outside", Members: []ntable.BatchMemberEntry{{ID: p.ID, Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: p.Row, Col: p.Col}}, Set: map[string]string{"brief": "outside"}}}})
		assert.NoError(h.t, err)
	}}
}

// cutStartLive starts s1-1 and s1-2 in one step (the fleet table, then the
// work table) whose work manifest finds s1-1 changed by a writer outside the
// fence: the step is cut, pending.
func (h *harness) cutStartLive(callerOp string) Step {
	h.t.Helper()
	st := *h.st
	st.B = h.outsideWriteLive("s1-1")
	step := DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}})
	step.CallerOp = callerOp
	_, err := st.Run(h.ctx, step)
	var cut *CutError
	f, ferr := h.st.B.ReadFence(h.ctx)
	if !errors.As(err, &cut) || ferr != nil || f.Pending == nil {
		require.Fail(h.t, fmt.Sprintf("the step was not cut: %v (fence %+v %v)", err, f, ferr))
	}
	return step
}

func liveSkipNotes(h *harness) []sprint.Note {
	var out []sprint.Note
	for _, n := range liveNotes(h) {
		if n.Type == NRepairSkipped {
			out = append(out, n)
		}
	}
	return out
}

// A step over two tables, cut after the first and before the last by a writer
// outside the fence: repair applies what still holds, skips what does not,
// leaves the newer state, writes one judgment, releases the fence, and a
// replay of the caller's operation id returns the skips.
func TestRedisAMultiTableStepCutIsRepairedSkippingWhatMoved(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	h.setup(2)
	step := h.cutStartLive("caller-9")
	// While the fence is held, another mutating verb finishes it on its way; here
	// the repair verb does.
	h.tick(time.Hour)
	rr, err := h.st.Repair(h.ctx)
	if err != nil || len(rr) != 1 || rr[0].Done != RepairSkipped || len(rr[0].Skipped) != 1 {
		require.Fail(t, fmt.Sprintf("repair: %+v %v", rr, err))
	}
	if f, err := h.st.B.ReadFence(h.ctx); err != nil || f.Pending != nil {
		require.Fail(t, fmt.Sprintf("the fence is still held: %+v %v", f, err))
	}
	s := h.snap()
	if c := s.Work.Card("s1-1"); c.Col != sprint.Ready || c.F("brief") != "outside" {
		require.Fail(t, fmt.Sprintf("s1-1 overwritten: %s brief=%s", c.Col, c.F("brief")))
	}
	c := s.Work.Card("s1-2")
	require.Equal(t, sprint.Working, c.Col, "s1-2, whose expectation held, is %s", c.Col)
	ns := liveSkipNotes(h)
	if len(ns) != 1 || ns[0].Kind != sprint.Judgment || len(ns[0].Primaries) != 1 || ns[0].Primaries[0] != "s1-1" {
		require.Fail(t, fmt.Sprintf("skip judgments: %+v", ns))
	}
	for _, want := range []string{"card s1-1", "f-work", "expected"} {
		if !strings.Contains(ns[0].What, want) && !strings.Contains(rr[0].Skipped[0], want) {
			require.Fail(t, fmt.Sprintf("the skip does not say %q: %s | %v", want, ns[0].What, rr[0].Skipped))
		}
	}
	found := false
	for _, o := range liveOpen(h, NRepairSkipped) {
		found = found || (o.Note.ID == ns[0].ID && o.Subject() == "s1-1")
	}
	require.True(t, found, "the skip judgment is not open on s1-1")
	rep, _, err := h.st.Check(h.ctx, 3)
	require.NoError(t, err, "check after the repair: pending %q %v", rep.Pending, err)
	require.Empty(t, rep.Pending, "check after the repair: pending %q %v", rep.Pending, err)
	// a second repair has nothing to do and writes nothing
	if rr, err := h.st.Repair(h.ctx); err != nil || len(rr) != 0 {
		require.Fail(t, fmt.Sprintf("a second repair: %+v %v", rr, err))
	}
	// the replay of the caller's operation id returns the recorded result
	res, err := h.st.Run(h.ctx, step)
	if err != nil || !res.Replay || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "s1-1") {
		require.Fail(t, fmt.Sprintf("replay: %+v %v", res, err))
	}
	require.Len(t, liveSkipNotes(h), 1, "%d skip judgments after the replay", len(liveSkipNotes(h)))
}

// failsWork is a store that refuses every send of the work table's manifest
// before it reaches Redis, as a broken connection does, while its switch is on.
type failsWork struct {
	Backend
	table string
	mu    sync.Mutex
	on    bool
}

func (f *failsWork) set(on bool) { f.mu.Lock(); f.on = on; f.mu.Unlock() }

func (f *failsWork) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	f.mu.Lock()
	on := f.on
	f.mu.Unlock()
	if on && m.Table == f.table {
		return ntable.Receipt{}, errors.New("connection refused")
	}
	return f.Backend.Apply(ctx, m)
}

// A step cut by a store that stops answering after its first manifest: the
// operation stays in the fence with the fleet table applied and the work table
// not; check names it; repair applies the rest and everything is as the step
// planned; nothing applies twice.
func TestRedisACutStepIsRepairedWhole(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	h.setup(2)
	cut := &failsWork{Backend: h.st.B, table: h.st.Names.Table(sprint.Work), on: true}
	st := *h.st
	st.B = cut
	st.Sleep = func(time.Duration) {}
	step := DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}})
	step.CallerOp = "caller-cut"
	res, err := st.Run(h.ctx, step)
	var cutErr *CutError
	if err == nil || !(errors.As(err, &cutErr) || errors.Is(err, ErrUnknown)) || res.Pending == "" {
		require.Fail(t, fmt.Sprintf("the step was not cut: %+v %v", res, err))
	}
	f, err := h.st.B.ReadFence(h.ctx)
	if err != nil || f.Pending == nil || len(f.Pending.Manifests) != 2 {
		require.Fail(t, fmt.Sprintf("the fence: %+v %v", f, err))
	}
	// the fleet table applied, the work table did not
	s := h.snap()
	require.Equal(t, sprint.Ready, s.Work.Card("s1-1").Col, "the work table moved: s1-1 is %s", s.Work.Card("s1-1").Col)
	rep, _, err := h.st.Check(h.ctx, 3)
	require.NoError(t, err, "check does not name the pending operation: %+v %v", rep, err)
	require.NotEmpty(t, rep.Pending, "check does not name the pending operation: %+v %v", rep, err)
	cut.set(false)
	rr, err := h.st.Repair(h.ctx)
	if err != nil || len(rr) != 1 || rr[0].Done != RepairFinished {
		require.Fail(t, fmt.Sprintf("repair: %+v %v", rr, err))
	}
	for _, id := range []string{"s1-1", "s1-2"} {
		require.Equal(t, sprint.Working, h.state(id), "%s is %s after the repair", id, h.state(id))
	}
	h.clean("repaired")
	// The replay of the caller's operation id: the recorded result, no new work.
	rev := h.snap().Work.Revision
	rep2, err := h.st.Run(h.ctx, step)
	if err != nil || !rep2.Replay || h.snap().Work.Revision != rev {
		require.Fail(t, fmt.Sprintf("replay: %+v %v", rep2, err))
	}
	n := len(h.snap().Fleet.Of("s1-1"))
	require.Equal(t, 1, n, "s1-1 has %d work cards", n)
}

// The caller's operation ids on the real store: a retry returns the recorded
// result and changes nothing; another verb or other arguments under the same id
// are a conflict; after a clear the id belongs to the epoch it was recorded at.
func TestRedisCallerOperationReplay(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	h.setup(3)
	deal := DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}})
	deal.CallerOp = "op-1"
	deal.Args = ArgsOf(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}})
	first := h.must(deal)
	require.Len(t, first.Moved, 1, "first: %+v", first)
	require.False(t, first.Replay, "first: %+v", first)
	rev := h.snap().Work.Revision
	again, err := h.st.Run(h.ctx, deal)
	if err != nil || !again.Replay || len(again.Moved) != 1 || h.snap().Work.Revision != rev {
		require.Fail(t, fmt.Sprintf("the retry: %+v %v", again, err))
	}
	if got, ok, err := h.st.B.Done(h.ctx, "op-1"); err != nil || !ok || got == "" {
		require.Fail(t, fmt.Sprintf("the record: %q %v %v", got, ok, err))
	}
	if _, ok, _ := h.st.B.Done(h.ctx, "op-none"); ok {
		require.Fail(t, "a record of an id never used")
	}
	other := AddStep(sprint.AddReq{Stream: "s1", Count: 1})
	other.CallerOp = "op-1"
	var conflict *OpConflictError
	_, err = h.st.Run(h.ctx, other)
	require.ErrorAs(t, err, &conflict, "another verb under the id: %v", err)
	args := DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}})
	args.CallerOp = "op-1"
	args.Args = ArgsOf(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}})
	if _, err := h.st.Run(h.ctx, args); !errors.As(err, &conflict) || !conflict.OtherArgs {
		require.Fail(t, fmt.Sprintf("other arguments under the id: %v", err))
	}
	require.Equal(t, sprint.Ready, h.state("s1-2"), "a conflicting step moved s1-2: %s", h.state("s1-2"))
	// after a clear
	_, err = h.st.Clear(h.ctx)
	require.NoError(t, err)
	e, ok, err := h.st.B.DoneBefore(h.ctx, "op-1", 1)
	if err != nil || !ok || e != 0 {
		require.Fail(t, fmt.Sprintf("DoneBefore: %d %v %v", e, ok, err))
	}
	res, err := h.st.Run(h.ctx, deal)
	if err != nil || len(res.Refused) != 1 || !strings.Contains(res.Refused[0].Why, "belongs to epoch 0") {
		require.Fail(t, fmt.Sprintf("the id after a clear: %+v %v", res, err))
	}
}

// liveImage is what a late writer must not change: the tables' revisions, the
// fence, the notification count, the size of the store.
func liveImage(h *harness, c *redis.Client) string {
	h.t.Helper()
	var b strings.Builder
	names := make([]string, len(All))
	for i, tb := range All {
		names[i] = h.st.Names.Table(tb)
	}
	p, err := h.st.Pinned(h.ctx)
	require.NoError(h.t, err)
	shapes, err := p.B.Shapes(h.ctx, names)
	require.NoError(h.t, err)
	for _, sh := range shapes {
		fmt.Fprintf(&b, "%s rev %d epoch %d\n", sh.Name, sh.Revision, sh.Epoch)
	}
	f, err := p.B.ReadFence(h.ctx)
	require.NoError(h.t, err)
	fmt.Fprintf(&b, "fence gen %d pending %v\n", f.Gen, f.Pending != nil)
	fmt.Fprintf(&b, "notes %d\n", len(liveNotes(h)))
	fmt.Fprintf(&b, "dbsize %d\n", c.DBSize(h.ctx).Val())
	return b.String()
}

// liveRacer runs another writer's step just before the first acquisition of the
// fence, once, and stays in place when the store pins the backend to an epoch
// (racer does not: AtEpoch returns the wrapped backend).
type liveRacer struct {
	Backend
	once *sync.Once
	do   func()
}

func (r liveRacer) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	r.once.Do(r.do)
	return r.Backend.Acquire(ctx, gen, op)
}

func (r liveRacer) AtEpoch(epoch uint64, old bool) Backend {
	return liveRacer{Backend: r.Backend.AtEpoch(epoch, old), once: r.once, do: r.do}
}

// After clear, a writer holding the old epoch (a worker with its card, a
// driver with its merge step) is refused, changes nothing, and is told the
// sprint was cleared; and the same step of a writer that read at the old epoch
// and reaches the fence after the clear is refused by the store, not planned
// against the new epoch's empty tables.
func TestRedisALateWriterAfterAClearIsRefused(t *testing.T) {
	t.Parallel()
	h, c := liveHarness(t)
	h.setup(4)
	h.through("s1-1", "s1-2")
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-3"}}}))
	before := h.snap()
	_, err := h.st.Clear(h.ctx)
	require.NoError(t, err)
	h.clean("cleared")
	img := liveImage(h, c)
	held := uint64(0)
	rc := before.Readers.Of("s1-1")
	steps := map[string]Step{
		"take":   TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{IDs: []string{"s1-3.w1"}}, Gens: map[string]int{"s1-3.w1": 1}}),
		"finish": FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{"s1-3.w1"}}, Gens: map[string]int{"s1-3.w1": 1}}),
		"report": ReadStep(sprint.ReadReq{As: rc[0].F("reader"), Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc[0].ID}}}),
		"merge":  MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1}),
		"accept": AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}),
		"drop":   DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-4"}}, Reason: "x"}),
		"fleet":  FleetStep(sprint.FleetReq{Op: "down", Member: "m1"}),
	}
	for name, step := range steps {
		e := held
		step.Epoch = &e
		res, err := h.st.Run(h.ctx, step)
		if err != nil || len(res.Moved) != 0 || len(res.Refused) != 1 || !strings.Contains(res.Refused[0].Why, "cleared at") {
			assert.Fail(t, fmt.Sprintf("late %s: %+v %v", name, res, err))
		}
		if got := liveImage(h, c); got != img {
			assert.Fail(t, fmt.Sprintf("late %s changed the store:\n%s\nwas\n%s", name, got, img))
			img = got
		}
	}
	h.clean("after the late writers")
	// The old epoch reads as it was.
	old, err := h.st.At(0).Load(h.ctx, All, nil)
	if err != nil || old.StateOf("s1-1") != sprint.Review && old.StateOf("s1-1") != sprint.Merging {
		require.Fail(t, fmt.Sprintf("the old epoch: s1-1 %v %v", old.StateOf("s1-1"), err))
	}

	// A writer that read the tables at the new epoch's predecessor and reaches
	// the fence after another clear: it is refused, or planned again at the new
	// epoch; it never writes at the epoch it read.
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 2}))
	other := *h.st
	other.Actor = "clearer"
	r := liveRacer{Backend: h.st.B, once: new(sync.Once), do: func() {
		_, err := other.Clear(h.ctx)
		assert.NoError(t, err)
	}}
	one := uint64(1)
	late := *h.st
	late.B = r
	step := DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}})
	step.Epoch = &one
	res, err := late.Run(h.ctx, step)
	if err != nil || len(res.Moved) != 0 || len(res.Refused) != 1 {
		require.Fail(t, fmt.Sprintf("the late deal that read before a clear: %+v %v", res, err))
	}
	now, err := h.st.EpochNow(h.ctx)
	require.NoError(t, err, "epoch %+v %v", now, err)
	require.Equal(t, uint64(2), now.N, "epoch %+v %v", now, err)
	h.clean("after the racing clear")
	old1, err := h.st.At(1).Load(h.ctx, All, nil)
	require.NoError(t, err, "epoch 1 holds a deal made after its clear: %v %v", old1.StateOf("s1-1"), err)
	require.NotEqual(t, sprint.Working, old1.StateOf("s1-1"), "epoch 1 holds a deal made after its clear: %v %v", old1.StateOf("s1-1"), err)
}

// A clear between a step's first manifest and its last, on the real table
// layer: the new epoch is empty and consistent, the old epoch reads as one
// state, repair and the same verb again change nothing at the new epoch.
func TestRedisAClearInTheMiddleOfAStep(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	h.setup(3)
	other := *h.st
	other.Actor = "clearer"
	var cres ClearResult
	var cerr error
	w := &clearAt{Backend: h.st.B, n: 2, do: func() { cres, cerr = other.Clear(h.ctx) }}
	st := *h.st
	st.B = w
	step := DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}})
	res, err := st.Run(h.ctx, step)
	t.Logf("step: %+v err=%v", res, err)
	t.Logf("clear: %+v err=%v", cres, cerr)
	require.NoError(t, cerr, "clear: %v", cerr)
	require.GreaterOrEqual(t, w.seen, 2, "the step wrote %d manifests", w.seen)
	s := h.snap()
	require.Equal(t, uint64(1), s.Epoch, "epoch %d", s.Epoch)
	for _, c := range s.Work.Cards() {
		if c.Placed() {
			assert.Fail(t, fmt.Sprintf("the new epoch holds %s at %s", c.ID, c.Col))
		}
	}
	for _, c := range s.Fleet.Cards() {
		if c.Placed() && c.Col != sprint.Ctl {
			assert.Fail(t, fmt.Sprintf("the new epoch's fleet holds %s at %s", c.ID, c.Col))
		}
	}
	h.clean("after the clear mid-step")
	old, err := h.st.At(0).Load(h.ctx, All, nil)
	require.NoError(t, err, "old epoch: %v", err)
	t.Logf("old epoch: s1-1 %s, s1-2 %s, fleet cards %d", old.StateOf("s1-1"), old.StateOf("s1-2"), len(old.Fleet.Cards()))
	_, err = h.st.Repair(h.ctx)
	require.NoError(t, err, "repair: %v", err)
	res, err = h.st.Run(h.ctx, step)
	require.NoError(t, err, "the same verb again: %+v %v", res, err)
	require.Empty(t, res.Moved, "the same verb again: %+v %v", res, err)
	h.clean("end")
}

// Teardown on the real store names its keys exactly: a second sprint on the
// same Redis, keys of the same shapes that are not the first sprint's, and
// keys of no sprint are all left as they were; the first sprint leaves no key,
// after clears and a second teardown.
func TestRedisTeardownLeavesNoKeyAndNothingElse(t *testing.T) {
	t.Parallel()
	c := liveClient(t)
	ctx := context.Background()
	left := liveKeys(t, c)
	require.Empty(t, left, "a new Redis holds %v", left)
	// the neighbour: another sprint on the same Redis
	b := liveHarnessOn(t, c, "g-")
	b.setup(2)
	b.through("s1-1")
	_, err := b.st.Clear(ctx)
	require.NoError(t, err)
	b.must(AddStep(sprint.AddReq{Stream: "s1", Count: 2}))
	b.through("s1-1")
	// keys of no sprint, and keys that look like the sprint's but are not its own
	foreign := []string{"other:key", "f-stranger", "table:f-work:stranger", "f-sprint-not:fence"}
	for _, k := range foreign {
		require.NoError(t, c.Set(ctx, k, "x", 0).Err())
	}
	untouched := liveKeys(t, c)

	a := liveHarnessOn(t, c, "f-")
	a.setup(3)
	a.through("s1-1", "s1-2")
	a.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1}))
	for i := 0; i < 2; i++ {
		_, err := a.st.Clear(ctx)
		require.NoError(t, err)
		a.must(AddStep(sprint.AddReq{Stream: "s1", Count: 2}))
		a.through("s1-1")
		require.NoError(t, a.st.B.SetCoordinator(ctx, "tester"))
	}
	a.startMachine()
	route := "file:" + filepath.Join(t.TempDir(), "goal.txt")
	a.setGoal("friend-a", "goal", route)
	a.machine()
	require.Greater(t, len(liveKeys(t, c)), len(untouched), "the sprint wrote no keys")
	n, err := a.st.Teardown(ctx)
	require.NoError(t, err)
	t.Logf("teardown deleted %d keys", n)
	after := liveKeys(t, c)
	require.Equal(t, strings.Join(untouched, "\n"), strings.Join(after, "\n"), "after teardown:\n%s\nbefore the sprint:\n%s", strings.Join(after, "\n"), strings.Join(untouched, "\n"))
	// the neighbour is whole
	b.clean("the neighbour after the teardown")
	// a second teardown deletes nothing more
	_, err = a.st.Teardown(ctx)
	require.NoError(t, err, "second teardown: %v", err)
	again := liveKeys(t, c)
	require.Equal(t, strings.Join(untouched, "\n"), strings.Join(again, "\n"), "after the second teardown:\n%s", strings.Join(again, "\n"))
	// and the neighbour's teardown leaves exactly the foreign keys
	_, err = b.st.Teardown(ctx)
	require.NoError(t, err)
	sort.Strings(foreign)
	got := liveKeys(t, c)
	require.Equal(t, strings.Join(foreign, "\n"), strings.Join(got, "\n"), "after both teardowns: %v, want %v", got, foreign)
}

// Writers racing on the fence, each with a client and a store of its own: of
// several writers dealing the same primaries at the same time each primary is
// started once, by one of them; the others are refused it; the tables are
// consistent at the end.
func TestRedisWritersRaceForTheSamePrimaries(t *testing.T) {
	t.Parallel()
	h, c := liveHarness(t)
	h.setup(15)
	const writers = 4
	for round := 0; round < 5; round++ {
		ids := []string{fmt.Sprintf("s1-%d", round*3+1), fmt.Sprintf("s1-%d", round*3+2), fmt.Sprintf("s1-%d", round*3+3)}
		type out struct {
			res Result
			err error
		}
		outs := make(chan out, writers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				wc := redis.NewClient(&redis.Options{Addr: c.Options().Addr})
				defer wc.Close()
				w := *h.st
				w.B = &Redis{C: wc, Names: h.st.Names, Now: h.st.Now}
				w.Actor = fmt.Sprintf("writer-%d", i)
				<-start
				res, err := w.Run(context.Background(), DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: ids}}))
				outs <- out{res, err}
			}(i)
		}
		close(start)
		wg.Wait()
		close(outs)
		moved := map[string]int{}
		busy := 0
		for o := range outs {
			require.NoError(t, o.err, "round %d: a writer failed: %v", round, o.err)
			for _, m := range o.res.Moved {
				moved[strings.Fields(m)[0]]++
			}
			for _, r := range o.res.Refused {
				if strings.Contains(r.Why, "kept changing") {
					busy++
				}
			}
		}
		for _, id := range ids {
			require.Equal(t, 1, moved[id], "round %d: %s was started %d times (%v)", round, id, moved[id], moved)
			require.Equal(t, sprint.Working, h.state(id), "round %d: %s is %s", round, id, h.state(id))
			n := len(h.snap().Fleet.Of(id))
			require.Equal(t, 1, n, "round %d: %s has %d work cards", round, id, n)
		}
		if busy > 0 {
			t.Logf("round %d: %d refusals said the sprint kept changing", round, busy)
		}
		h.clean(fmt.Sprintf("round %d", round))
	}
}

// Ticks racing: several loops tick the same running machine at once. Every
// ready primary is dealt once, the heartbeat is one record, and the tables are
// consistent; a tick after them moves nothing.
func TestRedisTicksRaceEachOther(t *testing.T) {
	t.Parallel()
	h, c := liveHarness(t)
	h.setup(9)
	h.startMachine()
	const loops = 3
	errs := make(chan error, loops)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < loops; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			wc := redis.NewClient(&redis.Options{Addr: c.Options().Addr})
			defer wc.Close()
			l := *h.st
			l.B = &Redis{C: wc, Names: h.st.Names, Now: h.st.Now}
			l.Actor = sprint.MachineActor
			<-start
			for k := 0; k < 3; k++ {
				if _, err := l.Tick(context.Background()); err != nil {
					errs <- err
					return
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		assert.Fail(t, fmt.Sprintf("a tick failed: %v", err))
	}
	s := h.snap()
	working := 0
	for i := 1; i <= 9; i++ {
		id := fmt.Sprintf("s1-%d", i)
		n := len(s.Fleet.Of(id))
		assert.LessOrEqual(t, n, 1, "%s has %d work cards", id, n)
		if s.StateOf(id) == sprint.Working || s.StateOf(id) == sprint.Ready {
			working++
		}
	}
	assert.Equal(t, 9, working, "%d of 9 primaries were dealt", working)
	h.clean("after the racing ticks")
	h.tick(time.Second)
	h.quiet("after the racing ticks")
}

// The coordinator's records on the real store: the coordinator, the open
// judgments one per subject, a review time set on one, and the cursor.
func TestRedisTheCoordinatorsRecords(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	who, err := h.st.B.Coordinator(h.ctx)
	if err != nil || who != h.st.Actor {
		require.Fail(t, fmt.Sprintf("the harness's coordinator: %q %v", who, err))
	}
	require.NoError(t, h.st.B.SetCoordinator(h.ctx, "tester"))
	who, err = h.st.B.Coordinator(h.ctx)
	if err != nil || who != "tester" {
		require.Fail(t, fmt.Sprintf("the coordinator: %q %v", who, err))
	}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true}))
	h.startMachine()
	h.machine()
	open := liveOpen(h, sprint.NSentinelReached)
	require.Len(t, open, 1, "open judgments: %d", len(open))
	when := h.now.Add(2 * time.Hour).UTC().Truncate(time.Second)
	require.NoError(t, h.st.SetReview(h.ctx, open[0].Note.ID, when))
	if got := liveOpen(h, sprint.NSentinelReached); len(got) != 1 || !got[0].Note.Review.Equal(when) {
		require.Fail(t, fmt.Sprintf("the review time: %+v", got))
	}
	require.Error(t, h.st.SetReview(h.ctx, "no-such-note", when), "a review time of no judgment was accepted")
	// release by another actor is refused; by the coordinator it lands
	coordinator, err := h.st.B.Coordinator(h.ctx)
	require.NoError(t, err)
	res, err := h.st.Run(h.ctx, ReleaseStep(sprint.ReleaseReq{IDs: []string{"stop"}, Reason: "x", Coordinator: coordinator, Who: "someone-else"}))
	if err != nil || len(res.Refused) == 0 || h.state("stop") == sprint.Landed {
		require.Fail(t, fmt.Sprintf("a release by another actor: %+v %v", res, err))
	}
	h.must(ReleaseStep(sprint.ReleaseReq{IDs: []string{"stop"}, Reason: "x", Coordinator: "tester", Who: "tester"}))
	require.Equal(t, sprint.Landed, h.state("stop"), "stop is %s", h.state("stop"))
	open = liveOpen(h, sprint.NSentinelReached)
	require.Empty(t, open, "the judgment is open after the release: %d", len(open))
	h.clean("released")
}

// The reminders on the real store: a person's goal is delivered by a tick of a
// RUNNING machine and again every five minutes of running; a failing route is
// judged once and closes when a delivery arrives; a dropped person's judgment
// closes; a clear keeps the people and forgets the pushes.
func TestRedisTheReminders(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	route, path := goalFile(t, "a")
	h.setGoal("friend-a", "keep the queue full", route)
	res := h.machine()
	require.Empty(t, reminded(res), "a stopped machine reminded: %v", reminded(res))
	h.startMachine()
	if got := reminded(h.machine()); len(got) != 1 || !strings.HasPrefix(got[0], "REMINDER 1 to friend-a") {
		require.Fail(t, fmt.Sprintf("the first tick after start: %v", got))
	}
	if b, err := os.ReadFile(path); err != nil || !strings.HasPrefix(string(b), "REMINDER 1 to friend-a at ") || !strings.HasSuffix(string(b), "keep the queue full\n") {
		require.Fail(t, fmt.Sprintf("file: %q %v", b, err))
	}
	h.tick(sprint.RemindEvery - time.Second)
	got := reminded(h.machine())
	require.Empty(t, got, "before five minutes: %v", got)
	h.tick(time.Second)
	if got := reminded(h.machine()); len(got) != 1 || !strings.HasPrefix(got[0], "REMINDER 2 ") {
		require.Fail(t, fmt.Sprintf("at five minutes: %v", got))
	}
	got = reminded(h.machine())
	require.Empty(t, got, "a second tick right after: %v", got)
	if g := h.goal("friend-a"); g.Count != 2 {
		require.Fail(t, fmt.Sprintf("record: %+v", g))
	}
	// a failing route: one judgment however often it fails
	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))
	h.setGoal("friend-b", "goal", "file:"+filepath.Join(blocker, "sub", "b.txt"))
	h.machine()
	for i := 0; i < 3; i++ {
		h.tick(sprint.RemindEvery)
		h.machine()
	}
	n := liveWritten(h, sprint.NRemindFailed)
	require.Equal(t, 1, n, "%d failure judgments written", n)
	open := liveOpen(h, sprint.NRemindFailed)
	require.Len(t, open, 1, "open failure judgments: %d", len(open))
	route2, path2 := goalFile(t, "b")
	_, _, err := h.st.SetGoal(h.ctx, "friend-b", nil, route2)
	require.NoError(t, err)
	require.NotEmpty(t, reminded(h.machine()), "no delivery after the route was fixed")
	if _, err := os.Stat(path2); err != nil {
		require.Fail(t, fmt.Sprintf("no file at the fixed route: %v", err))
	}
	open = liveOpen(h, sprint.NRemindFailed)
	require.Empty(t, open, "the failure stayed open after a delivery: %d", len(open))
	// a clear keeps the people and forgets the pushes
	if _, err := h.st.Clear(h.ctx); err != nil {
		require.NoError(t, err)
	}
	g, err := h.st.Goals(h.ctx)
	if err != nil || len(g.People) != 2 || g.People[0].Count != 0 || !g.People[0].Last.IsZero() {
		require.Fail(t, fmt.Sprintf("goals after a clear: %+v %v", g, err))
	}
	if ok, err := h.st.DropGoal(h.ctx, "friend-b"); err != nil || !ok {
		require.Fail(t, fmt.Sprintf("drop: %v %v", ok, err))
	}
}

// The fence on the real store, without a step: the acquisition holds only at
// the generation the caller read, only while the fence is empty, and only at
// the epoch the backend is pinned to.
func TestRedisTheFenceRefusesWhatItShould(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	ctx := h.ctx
	op := func(id string) OpRecord { return OpRecord{ID: id, Verb: "test", At: time.Now()} }
	f, err := h.st.B.ReadFence(ctx)
	require.NoError(t, err, "a new fence: %+v %v", f, err)
	require.Nil(t, f.Pending, "a new fence: %+v %v", f, err)
	if ok, err := h.st.B.Acquire(ctx, f.Gen, op("op-1")); !ok || err != nil {
		require.Fail(t, fmt.Sprintf("acquire at the generation read: %v %v", ok, err))
	}
	// pending: refused at any generation
	for _, g := range []uint64{f.Gen, f.Gen + 1} {
		if ok, err := h.st.B.Acquire(ctx, g, op("op-2")); ok || err != nil {
			require.Fail(t, fmt.Sprintf("acquire while op-1 is pending at %d: %v %v", g, ok, err))
		}
	}
	if got, err := h.st.B.ReadFence(ctx); err != nil || got.Pending == nil || got.Pending.ID != "op-1" || got.Gen != f.Gen+1 {
		require.Fail(t, fmt.Sprintf("the fence: %+v %v", got, err))
	}
	require.NoError(t, h.st.B.Release(ctx, op("op-1"), true))
	// empty again, but the generation moved: a writer that read before is refused
	if ok, err := h.st.B.Acquire(ctx, f.Gen, op("op-2")); ok || err != nil {
		require.Fail(t, fmt.Sprintf("acquire at a stale generation: %v %v", ok, err))
	}
	if ok, err := h.st.B.Acquire(ctx, f.Gen+1, op("op-2")); !ok || err != nil {
		require.Fail(t, fmt.Sprintf("acquire at the current generation: %v %v", ok, err))
	}
	require.NoError(t, h.st.B.Release(ctx, op("op-2"), false))
	// a release of an operation the fence no longer holds does nothing
	err = h.st.B.Release(ctx, op("op-2"), true)
	require.NoError(t, err, "a second release: %v", err)
	// a backend pinned to an epoch the sprint has left is refused
	_, err = h.st.Clear(ctx)
	require.NoError(t, err)
	f, err = h.st.B.ReadFence(ctx)
	require.NoError(t, err)
	if ok, err := h.st.B.AtEpoch(0, false).Acquire(ctx, f.Gen, op("op-3")); ok || err != nil {
		require.Fail(t, fmt.Sprintf("acquire at epoch 0 after a clear: %v %v", ok, err))
	}
	// the fence is one per epoch: epoch 1 has its own, empty, at generation 1
	// (clear's own line that the machine is STOPPED, written at epoch 1)
	one := h.st.B.AtEpoch(1, false)
	f1, err := one.ReadFence(ctx)
	if err != nil || f1.Pending != nil || f1.Gen != 1 {
		require.Fail(t, fmt.Sprintf("epoch 1's fence: %+v %v", f1, err))
	}
	if ok, err := one.Acquire(ctx, f1.Gen, op("op-3")); !ok || err != nil {
		require.Fail(t, fmt.Sprintf("acquire at epoch 1: %v %v", ok, err))
	}
}

// TestRedisTwinCatchesUpFromARowOrderWithoutAWholeRead pins the fix for
// nova-tools#5214: a fleet reorder (a "set" change-stream event, written
// outside the fence when a member's beat lapses and comes back under load)
// once read the fleet table whole because "set" named no records in the
// twin's catch-up verbs. The twin now advances the revision and re-reads the
// rows from the shape, so the drive's "no whole read after the first tick"
// holds.
func TestRedisTwinCatchesUpFromARowOrderWithoutAWholeRead(t *testing.T) {
	c := liveClient(t)
	names := sprint.Names{Prefix: "g-"}
	now := time.Now().UTC().Truncate(time.Second)
	st := &Store{B: &Redis{C: c, Names: names, Now: func() time.Time { return now }},
		Names: names, Actor: "tester", Now: func() time.Time { return now }}
	ctx := context.Background()
	require.NoError(t, st.Init(ctx))
	require.NoError(t, st.B.RowsAdd(ctx, names.Table(sprint.Fleet), []string{"m1", "m2"}))
	tw := st.twin()
	_, _, err := st.twinRead(ctx, tw, All, nil, nil)
	require.NoError(t, err)
	before := st.stats().reads.Load()
	require.NoError(t, st.B.(RowsOrderer).RowsOrder(ctx, names.Table(sprint.Fleet), []string{"m2", "m1"}))
	_, _, err = st.twinRead(ctx, tw, All, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, before, st.stats().reads.Load(), "a fleet reorder read the fleet whole; the twin should catch up from its change stream")
}
