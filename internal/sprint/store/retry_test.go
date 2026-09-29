package store

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// sleeps records the waits a store asks for, and sleeps none of them.
type sleeps struct {
	mu sync.Mutex
	d  []time.Duration
}

func (s *sleeps) sleep(d time.Duration) { s.mu.Lock(); s.d = append(s.d, d); s.mu.Unlock() }

func (s *sleeps) total() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	var t time.Duration
	for _, d := range s.d {
		t += d
	}
	return t
}

// churn is another writer that moves a table between every read set of a
// load: the table's revision the shape saw is never the one its members show.
type churn struct {
	Backend
	m     *Mem
	table string
}

func (c churn) ReadSet(ctx context.Context, table string, ids []string) (ntable.ReadSetResult, error) {
	if table == c.table {
		if err := c.m.RowsAdd(ctx, table, []string{"churn"}); err != nil {
			return ntable.ReadSetResult{}, err
		}
	}
	return c.Backend.ReadSet(ctx, table, ids)
}

// A table that keeps changing wears out the load: a jittered wait between
// reads, LoadTries reads at most, and the error names the table, the reads and
// that nothing was changed.
func TestABusyTableWaitsWithJitterAndNamesItself(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	var sl sleeps
	st := *h.st
	st.B = churn{Backend: h.m, m: h.m, table: "t-fleet"}
	st.Sleep = sl.sleep
	st.Rand = func(n int64) int64 { return n / 2 }
	_, err := st.Load(h.ctx, tables(sprint.Fleet), nil)
	if err == nil {
		t.Fatal("a table that never stops changing loaded")
	}
	for _, want := range []string{"the tables are busy", "table t-fleet kept changing", "12 reads", "nothing was changed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not say %q", err, want)
		}
	}
	if len(sl.d) != LoadTries-1 {
		t.Fatalf("waits: %d, want %d", len(sl.d), LoadTries-1)
	}
	for i, d := range sl.d {
		step := min(backoffBase<<i, backoffCap)
		if d != step/2 {
			t.Fatalf("wait %d: %s, want half of the step %s", i, d, step)
		}
	}
}

// The waits stop at RetryBudget asleep, before the tries run out, when every
// wait is drawn at its longest.
func TestABusyTableStopsAtTheBudget(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	var sl sleeps
	st := *h.st
	st.B = churn{Backend: h.m, m: h.m, table: "t-work"}
	st.Sleep = sl.sleep
	st.Rand = func(n int64) int64 { return n - 1 }
	_, err := st.Load(h.ctx, tables(sprint.Work), nil)
	if err == nil || !strings.Contains(err.Error(), "table t-work kept changing") {
		t.Fatalf("load: %v", err)
	}
	if tot := sl.total(); tot > RetryBudget || tot < RetryBudget-backoffCap {
		t.Fatalf("asleep %s, want at most %s and within a step of it", tot, RetryBudget)
	}
	if len(sl.d) >= LoadTries-1 {
		t.Fatalf("%d waits: the budget did not stop them", len(sl.d))
	}
}

// fenceMover is another writer that takes and releases the fence between
// every two reads of it.
type fenceMover struct {
	Backend
	mu  *sync.Mutex
	gen *uint64
}

func (f fenceMover) ReadFence(ctx context.Context) (Fence, error) {
	fe, err := f.Backend.ReadFence(ctx)
	f.mu.Lock()
	*f.gen++
	fe.Gen += *f.gen
	f.mu.Unlock()
	return fe, err
}

// A fence that never stands still: jittered waits, FenceTries reads, and an
// error that says nothing was changed.
func TestABusyFenceWaitsWithJitter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	var sl sleeps
	var gen uint64
	st := *h.st
	st.B = fenceMover{Backend: h.m, mu: &sync.Mutex{}, gen: &gen}
	st.Sleep = sl.sleep
	st.Rand = func(n int64) int64 { return n / 4 }
	_, err := st.Run(h.ctx, FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	if err == nil || !strings.Contains(err.Error(), "the sprint is busy") || !strings.Contains(err.Error(), "nothing was changed") ||
		!strings.Contains(err.Error(), "12 reads") {
		t.Fatalf("run: %v", err)
	}
	if len(sl.d) != FenceTries-1 || sl.d[0] != backoffBase/4 || sl.d[3] != 8*backoffBase/4 {
		t.Fatalf("waits: %v", sl.d)
	}
	if h.snap().MemberCtl("m1") != nil {
		t.Fatal("a busy step changed the fleet")
	}
}

// A store with no NewID, Sleep or Rand runs a step: the defaults stand in.
func TestAZeroStoreHasWorkingDefaults(t *testing.T) {
	t.Parallel()
	m := NewMem()
	st := &Store{B: m, Names: sprint.Names{Prefix: "z-"}, Now: time.Now}
	if err := st.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := st.Run(context.Background(), FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	if err != nil || res.Op == "" {
		t.Fatalf("run: %+v %v", res, err)
	}
	r := st.retry()
	st.Rand = func(int64) int64 { return 0 } // time.Sleep(0): the default sleeps, it does not spin on nil
	if !r.next(2) || !r.next(2) || r.next(2) {
		t.Fatal("two tries, then none")
	}
}

// Two writers started fresh, as two processes or one restarted, generate
// different operation ids, so neither step is taken for a replay of the
// other: both members come up.
func TestFreshStoresGenerateDifferentOperationIDs(t *testing.T) {
	t.Parallel()
	m := NewMem()
	names := sprint.Names{Prefix: "p-"}
	fresh := func() *Store {
		return &Store{B: m, Names: names, Actor: "a", Now: func() time.Time { return t0 }, Sleep: func(time.Duration) {}}
	}
	a, b := fresh(), fresh()
	if err := a.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	ra, err := a.Run(context.Background(), FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	if err != nil {
		t.Fatal(err)
	}
	rb, err := b.Run(context.Background(), FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	if err != nil {
		t.Fatal(err)
	}
	if ra.Op == rb.Op {
		t.Fatalf("both stores used operation id %s", ra.Op)
	}
	if a.newID() == b.newID() || NewID() == NewID() {
		t.Fatal("two fresh ids are the same")
	}
	s, err := a.Load(context.Background(), tables(sprint.Fleet), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"m1", "m2"} {
		if s.MemberCtl(id) == nil || s.MemberCtl(id).F("status") != sprint.Up {
			t.Fatalf("%s is not up: the second step was swallowed", id)
		}
	}
}
