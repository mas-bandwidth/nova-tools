package store

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
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
	require.Error(t, err, "a table that never stops changing loaded")
	for _, want := range []string{"the tables are busy", "table t-fleet kept changing", "12 reads", "nothing was changed"} {
		require.ErrorContains(t, err, want, "error %q does not say %q", err, want)
	}
	require.Len(t, sl.d, LoadTries-1, "waits: %d, want %d", len(sl.d), LoadTries-1)
	for i, d := range sl.d {
		step := min(backoffBase<<i, backoffCap)
		require.Equal(t, step/2, d, "wait %d: %s, want half of the step %s", i, d, step)
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
	require.ErrorContains(t, err, "table t-work kept changing", "load: %v", err)
	if tot := sl.total(); tot > RetryBudget || tot < RetryBudget-backoffCap {
		t.Fatalf("asleep %s, want at most %s and within a step of it", tot, RetryBudget)
	}
	require.Less(t, len(sl.d), LoadTries-1, "%d waits: the budget did not stop them", len(sl.d))
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
	require.Nil(t, h.snap().MemberCtl("m1"), "a busy step changed the fleet")
}

// A store with no NewID, Sleep or Rand runs a step: the defaults stand in.
func TestAZeroStoreHasWorkingDefaults(t *testing.T) {
	t.Parallel()
	m := NewMem()
	st := &Store{B: m, Names: sprint.Names{Prefix: "z-"}, Now: time.Now}
	require.NoError(t, st.Init(context.Background()))
	res, err := st.Run(context.Background(), FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	require.NoError(t, err, "run: %+v %v", res, err)
	require.NotEmpty(t, res.Op, "run: %+v %v", res, err)
	r := st.retry(context.Background())
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
	require.NoError(t, a.Init(context.Background()))
	ra, err := a.Run(context.Background(), FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	require.NoError(t, err)
	rb, err := b.Run(context.Background(), FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	require.NoError(t, err)
	require.NotEqual(t, rb.Op, ra.Op, "both stores used operation id %s", ra.Op)
	require.NotEqual(t, b.newID(), a.newID(), "two fresh ids are the same")
	require.NotEqual(t, NewID(), NewID(), "two fresh ids are the same")
	s, err := a.Load(context.Background(), tables(sprint.Fleet), nil)
	require.NoError(t, err)
	for _, id := range []string{"m1", "m2"} {
		require.NotNil(t, s.MemberCtl(id), "%s is not up: the second step was swallowed", id)
		require.Equal(t, string(sprint.Up), s.MemberCtl(id).F("status"), "%s is not up: the second step was swallowed", id)
	}
}

// One budget per step: the retry loops of a step (the reads, the fence, the
// plans) sleep from one budget, so the step sleeps at most RetryBudget in all.
func TestOneBudgetPerStep(t *testing.T) {
	t.Parallel()
	st := &Store{Sleep: func(time.Duration) {}, Rand: func(n int64) int64 { return n - 1 }}
	ctx := withBudget(context.Background())
	a, b := st.retry(ctx), st.retry(ctx)
	for a.next(1000) {
	}
	require.LessOrEqual(t, a.slept(), RetryBudget, "the first loop slept %s", a.slept())
	require.GreaterOrEqual(t, a.slept(), time.Duration(RetryBudget-backoffCap), "the first loop slept %s", a.slept())
	before := a.slept()
	for b.next(1000) {
	}
	if b.slept() > RetryBudget || b.slept() < before || a.slept() != b.slept() {
		t.Fatalf("two loops of one step: %s then %s, budget %s", before, b.slept(), RetryBudget)
	}
	if other := st.retry(context.Background()); !other.next(2) || !other.next(2) {
		t.Fatalf("another step has a budget of its own")
	}
}

// neverAcquire is a store where another writer takes the fence first,
// every time.
type neverAcquire struct{ Backend }

func (neverAcquire) Acquire(context.Context, uint64, OpRecord) (bool, error) { return false, nil }

// A step that loses every attempt to other writers says so, and counts no
// notes: it applied nothing.
func TestAStepThatLostEveryAttemptSaysSo(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	st := *h.st
	st.B = neverAcquire{h.m}
	res, err := st.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 2}}))
	if err != nil || !res.Lost || res.Notes != 0 || res.Op != "" || len(res.Moved) != 0 || len(res.Refused) == 0 {
		t.Fatalf("a step that lost every attempt: %+v %v", res, err)
	}
	require.Equal(t, sprint.Ready, h.state("s1-1"), "the lost step moved s1-1: %s", h.state("s1-1"))
}
