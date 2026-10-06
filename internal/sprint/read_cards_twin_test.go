package sprint_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// Read cards on the twin store (store.Mem): a read is a consumer card (docs/SPEC-SPRINT.md
// section 6, "A read is a consumer card").

// readCardsRig is a running sprint on the twin with read cards on: members m1..m3 (width 4),
// each with its reader row reader-<m> reading flash and pro, a flash and a pro route, and no
// friend.
type readCardsRig struct {
	t       *testing.T
	m       *store.Mem
	st      *store.Store
	ctx     context.Context
	mu      sync.Mutex
	now     time.Time
	members []string
}

func newReadCardsRig(t *testing.T, members ...string) *readCardsRig {
	t.Helper()
	if len(members) == 0 {
		members = []string{"m1", "m2", "m3"}
	}
	r := &readCardsRig{t: t, m: store.NewMem(), ctx: context.Background(), now: holdT0, members: members}
	n := 0
	r.st = &store.Store{B: r.m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		NewID: func() string { r.mu.Lock(); defer r.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	require.NoError(t, r.st.Init(r.ctx))
	var readers []string
	for _, m := range members {
		readers = append(readers, sprint.ReaderPrefix+m)
	}
	require.NoError(t, r.m.RowsAdd(r.ctx, "t-readers", readers))
	require.NoError(t, r.st.EnsureReaderTiers(r.ctx))
	for _, rd := range readers {
		require.NoError(t, r.m.RowSet(r.ctx, "t-readers", rd, map[string]string{sprint.ReaderTiers: "flash,pro"}))
	}
	require.NoError(t, r.m.SetCoordinator(r.ctx, "coordinator"))
	r.m.SetRoutes([]sprint.Route{
		{Name: "flash-a", Tier: "flash", Provider: "prov-flash-a", Model: "model-flash-a", Tokens: 1000, Deadline: 600, Enabled: true},
		{Name: "pro-a", Tier: "pro", Provider: "prov-pro-a", Model: "model-pro-a", Tokens: 1000, Deadline: 600, Enabled: true},
	})
	r.beat()
	for _, m := range members {
		r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: m, Width: 4}))
	}
	r.must(store.SetStep(sprint.SetReq{ReadCards: sprint.ReadCardsOnWord, Who: "coordinator"}))
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	return r
}

func (r *readCardsRig) beat() {
	r.t.Helper()
	require.NoError(r.t, r.st.BeatReaders(r.ctx))
	zero := 0.0
	for _, m := range r.members {
		_, err := r.st.Beat(r.ctx, m, &zero, hostload.Source{})
		require.NoError(r.t, err)
	}
}

func (r *readCardsRig) must(step store.Step) store.Result {
	r.t.Helper()
	res, err := r.st.Run(r.ctx, step)
	require.NoError(r.t, err, step.Verb)
	require.Empty(r.t, res.Refused, "%s refused", step.Verb)
	return res
}

func (r *readCardsRig) tick() {
	r.t.Helper()
	r.mu.Lock()
	r.now = r.now.Add(time.Second)
	r.mu.Unlock()
	r.beat()
	_, err := r.st.Tick(r.ctx)
	require.NoError(r.t, err)
}

func (r *readCardsRig) snap() *sprint.Snapshot {
	r.t.Helper()
	s, err := r.st.Load(r.ctx, store.All, nil)
	require.NoError(r.t, err)
	return s
}

// toReview adds one card of the brief to stream s1, deals it on a tick, has its member take
// and finish it ok at a head, and ticks it into review; the member that worked it.
func (r *readCardsRig) toReview(id, brief string) string {
	r.t.Helper()
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{{ID: id, Brief: brief}}}))
	r.tick()
	s := r.snap()
	wc := s.Fleet.Card(s.Work.Card(id).F("work"))
	require.NotNil(r.t, wc, "%s was dealt", id)
	r.must(store.TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}, Who: wc.Row}))
	wc = r.snap().Fleet.Card(wc.ID)
	head := fmt.Sprintf("%040d", len(id))
	r.must(store.FinishStep(sprint.FinishReq{As: wc.Row, Head: head, Branch: "sprint/" + id, Report: "done", Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}, Who: wc.Row}))
	r.tick()
	return wc.Row
}

// readCards is the placed read cards of the primary on the fleet table.
func (r *readCardsRig) readCards(primary string) []*sprint.Card {
	r.t.Helper()
	var out []*sprint.Card
	for _, c := range r.snap().Fleet.Of(primary) {
		if c.F("kind") == "read" {
			out = append(out, c)
		}
	}
	return out
}

const proBrief = "c: the work (s1) tier: pro\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"

// TestAReviewOpensNReadCardsAtOnceOnTheTwin: on the twin, the tick that brings a pro card to
// review cuts both its reads at once, each a read card dealt to a member that did not work
// it, on the pro route, in ready on the member's row; the readers table asks nothing; card
// and where count them.
func TestAReviewOpensNReadCardsAtOnceOnTheTwin(t *testing.T) {
	t.Parallel()
	r := newReadCardsRig(t)
	worker := r.toReview("s1-1", proBrief)
	s := r.snap()
	require.Equal(t, sprint.Review, s.Work.Card("s1-1").Col)
	reads := r.readCards("s1-1")
	require.Len(t, reads, 2, "both reads at once")
	rows := map[string]bool{}
	for _, c := range reads {
		require.NotEqual(t, worker, c.Row, "never the worker")
		require.Equal(t, sprint.ReadCardID("s1-1", 1, c.Row), c.ID)
		require.Equal(t, sprint.Ready, c.Col)
		require.Equal(t, "pro", c.F(sprint.FieldTier))
		require.Equal(t, "pro-a", c.F(sprint.FieldRoute), "drawn on its tier's route")
		rows[c.Row] = true
	}
	require.Len(t, rows, 2)
	for _, c := range s.Readers.Cards() {
		require.False(t, c.Placed(), "the readers table asks nothing")
	}
	info, err := r.st.CardOf(r.ctx, "s1-1")
	require.NoError(t, err)
	require.Len(t, info.Reads, 2, "card shows the read cards")
	require.Equal(t, 2, sprint.ReadsWaiting(s), "the two reads dealt and not started are the reads waiting")
}
