package sprint_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stream archive on the twin store (the owner, 2026-10-05: "I would like you to remove all
// the already landed work streams"): a stream whose every card landed leaves the drawn
// work and merge tables on a RUNNING machine, and every landed card, its cost and its
// landing stay in the ledger.

type archiveRig struct {
	t   *testing.T
	st  *store.Store
	m   *store.Mem
	ctx context.Context
	mu  sync.Mutex
	now time.Time
}

func newArchiveRig(t *testing.T) *archiveRig {
	t.Helper()
	r := &archiveRig{t: t, m: store.NewMem(), ctx: context.Background(), now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
	n := 0
	r.st = &store.Store{B: r.m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		NewID: func() string { r.mu.Lock(); defer r.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	require.NoError(t, r.st.Init(r.ctx))
	require.NoError(t, r.m.SetCoordinator(r.ctx, "coordinator"))
	for _, s := range []sprint.AddReq{{Stream: "done", Count: 2}, {Stream: "live", Count: 2}, {Stream: "next", Count: 1}} {
		res, err := r.st.Run(r.ctx, store.AddStep(s))
		require.NoError(t, err)
		require.Empty(t, res.Refused)
	}
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	return r
}

// to moves work cards to a column as a store write, and with landed their cost on the
// card and the stream's sum on its control card, as the merge that lands them writes it.
func (r *archiveRig) to(col string, cost string, ids ...string) {
	r.t.Helper()
	s, err := r.st.Load(r.ctx, []string{sprint.Work, sprint.Merge}, nil)
	require.NoError(r.t, err)
	var ms []ntable.BatchMemberEntry
	sums := map[string][]string{}
	for _, id := range ids {
		c := s.Work.Card(id)
		set := map[string]string{}
		if col == sprint.Landed {
			set["landed"] = r.now.Format(time.RFC3339)
			set[sprint.FieldCost] = cost
			sums[c.Row] = append(sums[c.Row], cost)
		}
		ms = append(ms, ntable.BatchMemberEntry{ID: c.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev)},
			Move: &ntable.MemberMoveOp{Row: c.Row, Col: col}, Set: set})
	}
	_, err = r.m.Apply(r.ctx, ntable.BatchManifest{Schema: 1, Table: "t-work", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Work.Revision),
		OperationID: "seed-" + col + "-" + strings.Join(ids, "-"), Members: ms})
	require.NoError(r.t, err)
	var cs []ntable.BatchMemberEntry
	for stream, vals := range sums {
		ctl := s.Merge.Card(sprint.CtlID(stream))
		sum, ok := cardcost.Sum(append(vals, ctl.F(sprint.FieldCost))...)
		require.True(r.t, ok)
		cs = append(cs, ntable.BatchMemberEntry{ID: ctl.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(ctl.Rev)}, Set: map[string]string{sprint.FieldCost: sum}})
	}
	if len(cs) > 0 {
		_, err = r.m.Apply(r.ctx, ntable.BatchManifest{Schema: 1, Table: "t-merge", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Merge.Revision),
			OperationID: "seed-ctl-" + strings.Join(ids, "-"), Members: cs})
		require.NoError(r.t, err)
	}
	require.NoError(r.t, r.st.SyncMirrors(r.ctx))
}

// ledger is what the ledger counts: the summary's landed and all, the cost column's
// footer, and every landed card with its cost and its landing.
type ledger struct {
	Landed, All int64
	Cost        string
	Cards       map[string]string
}

func (r *archiveRig) ledger() ledger {
	r.t.Helper()
	shapes, err := r.m.Shapes(r.ctx, []string{"t-work"})
	require.NoError(r.t, err)
	w := shapes[0]
	out := ledger{Cards: map[string]string{}}
	j, k := w.Column(sprint.Landed), w.Column(sprint.Cost)
	var usd []string
	for _, row := range w.Rows {
		for i, c := range w.Columns {
			if c.HasSet() && i < len(row.Cells) {
				out.All += row.Cells[i].Count
				if i == j {
					out.Landed += row.Cells[i].Count
				}
			}
		}
		if v := strings.TrimPrefix(ntable.CellText(w.Columns, row, k), "$"); v != "-" && v != "" {
			usd = append(usd, v)
		}
	}
	out.Cost, _ = cardcost.Sum(usd...)
	s, err := r.st.Load(r.ctx, []string{sprint.Work}, nil)
	require.NoError(r.t, err)
	for _, c := range s.Work.Cards() {
		if c.Placed() && c.Col == sprint.Landed {
			out.Cards[c.ID] = c.Row + " cost=" + c.F(sprint.FieldCost) + " landed=" + c.F("landed")
		}
	}
	return out
}

// drawn is the streams the work and merge tables draw, as where's frame does.
func (r *archiveRig) drawn() map[string][]string {
	r.t.Helper()
	out := map[string][]string{}
	for _, t := range []string{sprint.Work, sprint.Merge} {
		shapes, err := r.m.Shapes(r.ctx, []string{"t-" + t})
		require.NoError(r.t, err)
		text := ntable.Render(shapes[0], ntable.RenderOpts{Title: t})
		out[t] = []string{}
		for _, row := range shapes[0].Rows {
			for _, l := range strings.Split(text, "\n") {
				if strings.HasPrefix(l, row.Key+" ") {
					out[t] = append(out[t], row.Key)
					break
				}
			}
		}
	}
	return out
}

func TestArchiveKeepsEveryLandedCardAndItsCost(t *testing.T) {
	t.Parallel()
	r := newArchiveRig(t)
	r.to(sprint.Landed, "0.25", "done-1", "done-2", "live-1")
	r.to(sprint.Working, "", "live-2")
	before := r.ledger()
	require.Equal(t, int64(3), before.Landed)
	require.Equal(t, int64(5), before.All)
	require.Equal(t, "0.75", before.Cost)
	both := map[string][]string{sprint.Work: {"done", "live", "next"}, sprint.Merge: {"done", "live", "next"}}
	require.Equal(t, both, r.drawn())

	// a stream with a card not landed is refused naming the card, all or none
	refused, err := r.st.ArchiveStreams(r.ctx, []string{"done", "live"})
	require.NoError(t, err)
	require.Len(t, refused, 2)
	assert.Equal(t, "live", refused[0].Key)
	assert.Contains(t, refused[0].Why, "stream live holds 1 card not landed: live-2 (working)")
	assert.Equal(t, "done", refused[1].Key)
	assert.Contains(t, refused[1].Why, "all or none")
	assert.Equal(t, both, r.drawn(), "refused: nothing changed")
	archived, err := r.st.ArchivedStreams(r.ctx)
	require.NoError(t, err)
	assert.Empty(t, archived)

	// on the RUNNING machine the landed stream leaves both drawn tables, and the ledger
	// counts every landed card, its cost and its landing as before
	m, _, err := r.st.Machine(r.ctx)
	require.NoError(t, err)
	require.True(t, m.Running())
	refused, err = r.st.ArchiveStreams(r.ctx, []string{"done"})
	require.NoError(t, err)
	require.Empty(t, refused)
	assert.Equal(t, map[string][]string{sprint.Work: {"live", "next"}, sprint.Merge: {"live", "next"}}, r.drawn())
	assert.Equal(t, before, r.ledger(), "the ledger is unchanged")
	archived, err = r.st.ArchivedStreams(r.ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"done"}, archived)
	shapes, err := r.m.Shapes(r.ctx, []string{"t-work"})
	require.NoError(t, err)
	tot := store.ArchivedOf(shapes[0])
	assert.Equal(t, store.ArchivedTotals{Streams: []string{"done"}, Landed: 2, Cost: "$0.50"}, tot)
	assert.Equal(t, "1 archived stream, 2 cards landed, $0.50", tot.Line())

	refused, err = r.st.ArchiveStreams(r.ctx, []string{"done"})
	require.NoError(t, err)
	require.Len(t, refused, 1)
	assert.Contains(t, refused[0].Why, "archived already")

	// unarchive restores it, and the tick does not take it off again on its own
	refused, err = r.st.UnarchiveStreams(r.ctx, []string{"done"})
	require.NoError(t, err)
	require.Empty(t, refused)
	assert.Equal(t, both, r.drawn())
	assert.Equal(t, before, r.ledger())
	refused, err = r.st.UnarchiveStreams(r.ctx, []string{"done"})
	require.NoError(t, err)
	require.Len(t, refused, 1)
	assert.Contains(t, refused[0].Why, "stream done is not archived")
	res, err := r.st.Tick(r.ctx)
	require.NoError(t, err)
	assert.Empty(t, res.Archived, "an unarchive by hand holds while its stream's counts hold")
	assert.Equal(t, both, r.drawn())

	// the tick archives a stream itself when its last card lands, and says so once
	r.to(sprint.Landed, "0.5", "live-2")
	res, err = r.st.Tick(r.ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"live"}, res.Archived)
	assert.Equal(t, map[string][]string{sprint.Work: {"done", "next"}, sprint.Merge: {"done", "next"}}, r.drawn())
	after := r.ledger()
	assert.Equal(t, int64(4), after.Landed)
	assert.Equal(t, "1.25", after.Cost)
	notes, _, err := r.m.NotesSince(r.ctx, "", 1000)
	require.NoError(t, err)
	var said []string
	for _, n := range notes {
		if n.Type == sprint.NStreamArchived {
			said = append(said, n.What)
		}
	}
	require.Len(t, said, 1)
	assert.Contains(t, said[0], "live")
	res, err = r.st.Tick(r.ctx)
	require.NoError(t, err)
	assert.Empty(t, res.Archived, "once")

	// a card added to an archived stream shows it again
	add, err := r.st.Run(r.ctx, store.AddStep(sprint.AddReq{Stream: "live", Count: 1}))
	require.NoError(t, err)
	require.Empty(t, add.Refused)
	res, err = r.st.Tick(r.ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"live"}, res.Shown)
	assert.Equal(t, both, r.drawn())
}
