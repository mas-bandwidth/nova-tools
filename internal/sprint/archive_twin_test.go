package sprint_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// Stream archive on the twin store (the owner, 2026-10-05 ~11:45 PM ET: "I would like you to
// remove all the already landed work streams"). 107 streams of landed cards crowded the
// table, and stream remove refuses them, since removing would lose their cards. Archiving
// hides a stream's rows and moves no card: the ledger keeps every landed card, its cost and
// its landing, and the footers count them.

// costUsage is what each card's run spent, priced by the harness.
const costUsage = "input=10 actual_usd=0.2500"

// landWithCost drives each primary through work at a priced run, its reads and the accept to
// merging, then lands them in one batch.
func (r *conflictRig) landWithCost(stream string, ids ...string) {
	r.t.Helper()
	for _, id := range ids {
		if r.snap().Work.Card(id).Col == sprint.Ready {
			r.must(dealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{id}}}))
		}
		s := r.snap()
		wc := s.Fleet.Card(s.Work.Card(id).F("work"))
		require.NotNil(r.t, wc, id)
		r.must(store.TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
		wc = r.snap().Fleet.Card(wc.ID)
		r.must(store.FinishStep(sprint.FinishReq{Usage: costUsage, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
		for range 4 {
			s = r.snap()
			if pr := s.Work.Card(id); pr.Col != sprint.Review || sprint.ReadsWanted(s, pr) == 0 {
				break
			}
			r.must(store.AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{id}}}))
			for _, rc := range r.snap().Readers.Of(id) {
				if rc.Col == sprint.Asked || rc.Col == sprint.Reading {
					r.must(store.ReadStep(sprint.ReadReq{As: rc.Row, Verdict: "ok", Finding: "f:1", Usage: costUsage, Sel: sprint.Sel{IDs: []string{rc.ID}}}))
				}
			}
		}
		r.must(store.AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{id}}}))
	}
	r.must(store.MergeStep(sprint.MergeReq{Stream: stream, Cards: ids}))
}

// ledger is what the record keeps of a stream: each landed card with its cost, and the
// stream's cost on its control card.
func ledger(s *sprint.Snapshot, stream string) map[string]string {
	out := map[string]string{"ctl": s.StreamCtl(stream).F(sprint.FieldCost)}
	for _, c := range s.Work.Cell(stream, sprint.Landed) {
		out[c.ID] = c.F(sprint.FieldCost)
	}
	return out
}

// drawn is the work table as where draws it: the stream rows it shows, and its footer's
// landed count and cost (the tick deals the cards that are not landed meanwhile).
func (r *conflictRig) drawn() (rows []string, footer string) {
	r.t.Helper()
	pinned, err := r.st.Pinned(r.ctx)
	require.NoError(r.t, err)
	require.NoError(r.t, pinned.SyncMirrors(r.ctx))
	shapes, err := r.m.Shapes(r.ctx, []string{pinned.Names.Table(sprint.Work)})
	require.NoError(r.t, err)
	lines := strings.Split(strings.TrimRight(ntable.Render(shapes[0], ntable.RenderOpts{Title: sprint.Work}), "\n"), "\n")
	for _, l := range lines[1 : len(lines)-1] {
		if f := strings.Fields(l); len(f) > 0 && strings.HasPrefix(f[0], "s") {
			rows = append(rows, f[0])
		}
	}
	f := strings.Fields(lines[len(lines)-1])
	return rows, strings.Join(f[len(f)-3:], " ")
}

// archivedNotes is the tick's notes that it archived streams.
func (r *conflictRig) archivedNotes() []string {
	r.t.Helper()
	all, _, err := r.m.NotesSince(r.ctx, "", 100000)
	require.NoError(r.t, err)
	var out []string
	for _, n := range all {
		if n.Type == sprint.NStreamArchived {
			out = append(out, n.What)
		}
	}
	return out
}

func TestArchiveKeepsEveryLandedCardAndItsCost(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	r.must(store.AddStep(sprint.AddReq{Stream: "s2", Count: 1, Brief: "c: the work (s2) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"}))
	r.landWithCost("s1", "s1-1", "s1-2", "s1-3")
	// Stop after the owned work and reads settle; STOP does not admit new takes.
	_, _, _, err := r.st.SetMachine(r.ctx, false)
	require.NoError(t, err)
	s := r.snap()
	kept := ledger(s, "s1")
	require.Len(t, kept, 4)
	for id, cost := range kept {
		require.NotEmpty(t, cost, "%s is priced", id)
	}
	rows, footer := r.drawn()
	require.Equal(t, []string{"s1", "s2"}, rows)
	require.Contains(t, footer, "$", "the footer sums the landed cost")

	// a stream with a card not landed is refused, naming it; all or none, nothing changed
	refused, err := r.st.ArchiveStreams(r.ctx, []string{"s1", "s2"})
	require.NoError(t, err)
	require.Len(t, refused, 2)
	assert.Equal(t, "s2", refused[0].Key)
	assert.Contains(t, refused[0].Why, "s2-1 ready")
	assert.Contains(t, refused[0].Why, "not landed")
	assert.Contains(t, refused[1].Why, "not archived")
	got, _ := r.drawn()
	assert.Equal(t, []string{"s1", "s2"}, got, "refused: nothing changed")
	refused, err = r.st.ArchiveStreams(r.ctx, []string{"zz"})
	require.NoError(t, err)
	require.Len(t, refused, 1)
	assert.Contains(t, refused[0].Why, "no stream zz")

	// archived: off the drawn table; every landed card, its cost and the footer kept
	refused, err = r.st.ArchiveStreams(r.ctx, []string{"s1"})
	require.NoError(t, err)
	require.Empty(t, refused)
	s = r.snap()
	assert.True(t, s.Work.Hidden("s1"))
	assert.True(t, s.Merge.Hidden("s1"))
	assert.Equal(t, []string{"s1"}, sprint.ArchivedStreams(s))
	assert.Equal(t, kept, ledger(s, "s1"), "the ledger keeps every landed card and its cost")
	got, gotFooter := r.drawn()
	assert.Equal(t, []string{"s2"}, got, "the archived stream leaves the table")
	assert.Equal(t, footer, gotFooter, "the totals are unchanged")
	r.clean("archived")

	// on a RUNNING machine: the tick leaves a stream archived by hand as it is, and says nothing
	_, _, _, err = r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	r.tick()
	assert.True(t, r.snap().Work.Hidden("s1"))
	assert.Empty(t, r.archivedNotes(), "a stream archived by hand is no news")

	// unarchive restores it, and the tick does not archive it again
	refused, err = r.st.UnarchiveStreams(r.ctx, []string{"s1"})
	require.NoError(t, err)
	require.Empty(t, refused)
	r.tick()
	r.tick()
	got, gotFooter = r.drawn()
	assert.Equal(t, []string{"s1", "s2"}, got, "unarchive restores the row")
	assert.Equal(t, footer, gotFooter)
	assert.Equal(t, kept, ledger(r.snap(), "s1"))
	refused, err = r.st.UnarchiveStreams(r.ctx, []string{"s2"})
	require.NoError(t, err)
	require.Len(t, refused, 1)
	assert.Contains(t, refused[0].Why, "stream s2 is not archived")

	// archived again while RUNNING: no stop needed
	refused, err = r.st.ArchiveStreams(r.ctx, []string{"s1"})
	require.NoError(t, err)
	require.Empty(t, refused)
	assert.True(t, r.snap().Work.Hidden("s1"))

	// the tick archives a stream itself when its last card lands, and names it once
	r.landWithCost("s2", "s2-1")
	r.tick()
	r.tick()
	s = r.snap()
	assert.True(t, s.Work.Hidden("s2"), "the tick archived s2")
	assert.Equal(t, sprint.Landed, s.Work.Card("s2-1").Col)
	assert.NotEmpty(t, ledger(s, "s2")["s2-1"])
	notes := r.archivedNotes()
	require.Len(t, notes, 1, "one note, once")
	assert.Contains(t, notes[0], "s2")
	assert.NotContains(t, notes[0], "s1")
	got, gotFooter = r.drawn()
	assert.Empty(t, got)
	assert.NotEqual(t, footer, gotFooter, "s2's landing counts in the footer")
	r.clean("archived by the tick")

	// a card added to an archived stream draws it again
	r.must(store.AddStep(sprint.AddReq{Stream: "s2", Count: 1, Brief: "c: more (s2) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"}))
	r.tick()
	assert.False(t, r.snap().Work.Hidden("s2"), "work again: drawn")
	assert.True(t, r.snap().Work.Hidden("s1"))

	// a clear keeps the streams, and an archived one stays archived
	_, err = r.st.Clear(r.ctx)
	require.NoError(t, err)
	s = r.snap()
	assert.True(t, s.Work.HasRow("s1"))
	assert.Equal(t, []string{"s1"}, sprint.ArchivedStreams(s))
}
