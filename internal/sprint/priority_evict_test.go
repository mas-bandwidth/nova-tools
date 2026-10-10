package sprint_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A blocker evicts the lowest running card, then the one running the shortest, never another
// blocker (docs/SPEC-SPRINT.md section 1, Priority; the owner, 2026-10-06). On the twin
// (store.Mem): a member at its width with a low card started ten minutes ago, a normal
// started two minutes ago and a normal started twenty minutes ago; a blocker arrives and the
// low is evicted; with no low the two-minute normal is evicted; with only blockers running
// nothing is evicted and the blocker waits.
func TestABlockerIsStoredAndEvictsTheLowestThenTheShortest(t *testing.T) {
	t.Parallel()

	const (
		minute = time.Minute
		low    = "s1-1"
		n2     = "s1-2" // started two minutes ago
		n20    = "s1-3" // started twenty minutes ago
	)

	t.Run("the lowest is evicted first", func(t *testing.T) {
		t.Parallel()
		r := newEvictRig(t, 3)
		r.add(low, "PRIORITY: low")
		r.add(n2, "")
		r.add(n20, "")
		r.dealAndRun(map[string]time.Duration{low: 10 * minute, n2: 2 * minute, n20: 20 * minute})

		r.add("s1-b", "PRIORITY: blocker")
		r.must(dealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-b"}}}))

		s := r.snap()
		assert.Equal(t, sprint.PriorityBlocker, s.Work.Card("s1-b").F(sprint.FieldPriority), "the blocker is stored at blocker")
		evicted := s.Fleet.Card(sprint.WorkCardID(low, 1))
		require.NotNil(t, evicted, "the low card's work card")
		assert.Equal(t, sprint.Withdrawn, evicted.Col, "the low card is evicted")
		assert.Equal(t, "s1-b", evicted.F(sprint.FieldEvictedBy), "the record names the blocker")
		assert.Equal(t, sprint.Ready, s.Work.Card(low).Col, "the evicted primary is ready again")
		// the other two keep running
		assert.Equal(t, sprint.Working, s.Work.Card(n2).Col)
		assert.Equal(t, sprint.Working, s.Work.Card(n20).Col)
		assert.Equal(t, sprint.Working, s.Work.Card("s1-b").Col, "the blocker is dealt")
	})

	t.Run("with no low the shortest running is evicted", func(t *testing.T) {
		t.Parallel()
		r := newEvictRig(t, 3)
		r.add(low, "")
		r.add(n2, "")
		r.add(n20, "")
		r.dealAndRun(map[string]time.Duration{low: 9 * minute, n2: 2 * minute, n20: 20 * minute})

		r.add("s1-b", "PRIORITY: blocker")
		r.must(dealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-b"}}}))

		s := r.snap()
		evicted := s.Fleet.Card(sprint.WorkCardID(n2, 1))
		require.NotNil(t, evicted, "the two-minute card's work card")
		assert.Equal(t, sprint.Withdrawn, evicted.Col, "the two-minute normal is evicted (shortest running)")
		assert.Equal(t, "s1-b", evicted.F(sprint.FieldEvictedBy))
		assert.Equal(t, sprint.Ready, s.Work.Card(n2).Col)
		assert.Equal(t, sprint.Working, s.Work.Card(low).Col, "the nine-minute normal keeps running")
		assert.Equal(t, sprint.Working, s.Work.Card(n20).Col, "the twenty-minute normal keeps running")
	})

	t.Run("with only blockers running nothing is evicted and the blocker waits", func(t *testing.T) {
		t.Parallel()
		r := newEvictRig(t, 3)
		r.add("s1-a", "PRIORITY: blocker")
		r.add("s1-b", "PRIORITY: blocker")
		r.add("s1-c", "PRIORITY: blocker")
		r.dealAndRun(map[string]time.Duration{"s1-a": 5 * minute, "s1-b": 3 * minute, "s1-c": 1 * minute})

		r.add("s1-d", "PRIORITY: blocker")
		res, err := r.st.Run(r.ctx, dealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-d"}}}))
		require.NoError(t, err)
		require.Len(t, res.Refused, 1)
		assert.Contains(t, res.Refused[0].Why, "every lane holds a blocker")
		s := r.snap()
		assert.Equal(t, sprint.Ready, s.Work.Card("s1-d").Col, "the blocker waits ready")
		for _, id := range []string{"s1-a", "s1-b", "s1-c"} {
			assert.Equal(t, sprint.Working, s.Work.Card(id).Col, "a blocker is never evicted")
		}
	})
}

// A primary's level is stored and read back for every level of the ladder, including blocker
// and critical (the owner, 2026-10-06: "You should be able to set the priority on a card
// higher or lower or the same. It's just a set. That's the verb."). On the twin (store.Mem).
func TestPriorityStoresEveryLevel(t *testing.T) {
	t.Parallel()
	r := newEvictRig(t, 1)
	r.add("s1-1", "")
	for _, level := range sprint.PrioritySettable {
		r.must(store.PriorityStep(sprint.PriorityReq{IDs: []string{"s1-1"}, Level: level, Reason: "the release waits on it", Who: "coordinator"}))
		got, src := sprint.CardPriority(r.snap().Work.Card("s1-1"))
		assert.Equal(t, level, got, "level %s stored", level)
		assert.Equal(t, "set", src, "level %s reads back as set", level)
	}
}

// evictRig is a sprint on the twin with one flash route and member m1 at a width.
type evictRig struct {
	t   *testing.T
	m   *store.Mem
	st  *store.Store
	ctx context.Context
	mu  sync.Mutex
	now time.Time
}

func newEvictRig(t *testing.T, width int) *evictRig {
	t.Helper()
	r := &evictRig{t: t, m: store.NewMem(), ctx: context.Background(), now: holdT0}
	n := 0
	r.st = &store.Store{B: r.m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator", AnswerRules: true,
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		NewID: func() string { r.mu.Lock(); defer r.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	require.NoError(t, r.st.Init(r.ctx))
	require.NoError(t, r.m.RowsAdd(r.ctx, "t-readers", []string{"reader-a"}))
	require.NoError(t, r.st.EnsureReaderTiers(r.ctx))
	require.NoError(t, r.m.RowSet(r.ctx, "t-readers", "reader-a", map[string]string{sprint.ReaderTiers: "flash,pro"}))
	require.NoError(t, r.m.SetCoordinator(r.ctx, "coordinator"))
	r.m.SetRoutes([]sprint.Route{{Name: "flash-a", Tier: "flash", Provider: "p", Model: "m", Tokens: 1000, Deadline: 600, Enabled: true}})
	require.NoError(t, r.st.BeatReaders(r.ctx))
	zero := 0.0
	_, err := r.st.Beat(r.ctx, "m1", &zero, hostload.Source{})
	require.NoError(t, err)
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: width}))
	return r
}

func (r *evictRig) must(step store.Step) store.Result {
	r.t.Helper()
	res, err := r.st.Run(r.ctx, step)
	require.NoError(r.t, err, step.Verb)
	require.Empty(r.t, res.Refused, "%s refused", step.Verb)
	return res
}

func (r *evictRig) snap() *sprint.Snapshot {
	r.t.Helper()
	s, err := r.st.Load(r.ctx, store.All, nil)
	require.NoError(r.t, err)
	return s
}

// add admits one flash card to stream s1.
func (r *evictRig) add(id, priority string) {
	r.t.Helper()
	brief := "c: " + id + " (s1) tier: flash\n"
	if priority != "" {
		brief += priority + "\n"
	}
	brief += "REPO: mas-bandwidth/nova-tools\n\nThe task.\n"
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{{ID: id, Brief: brief}}}))
}

// dealAndRun deals each named card and takes it into a lane at now minus its age.
func (r *evictRig) dealAndRun(ages map[string]time.Duration) {
	r.t.Helper()
	// deal all first, then take each at its start time
	var ids []string
	for id := range ages {
		ids = append(ids, id)
	}
	r.must(dealStep(sprint.DealReq{Sel: sprint.Sel{IDs: ids}}))
	s := r.snap()
	for _, id := range ids {
		wc := s.Fleet.Card(s.Work.Card(id).F("work"))
		require.NotNil(r.t, wc, id)
		r.mu.Lock()
		r.now = holdT0.Add(-ages[id])
		r.mu.Unlock()
		r.must(store.TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	}
	r.mu.Lock()
	r.now = holdT0
	r.mu.Unlock()
}
