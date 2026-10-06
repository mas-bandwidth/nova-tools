package sprint_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Stream archive on the twin store (store.Mem; docs/SPEC-SPRINT.md section 11; the owner,
// 2026-10-05: "I would like you to remove all the already landed work streams"): a stream
// whose every card landed leaves the drawn tables and keeps its record, on a RUNNING
// machine; stream unarchive draws it again; the tick archives a stream itself when its
// last card lands.

// archiveSpend is what each attempt of the rig's cards spent, as the member reports it.
const archiveSpend = "input=100 actual_usd=1.25 actual_by=harness"

// landWithSpend drives each primary through work (spending archiveSpend), its reads and
// the accept, then merges them: each lands with a cost.
func (r *conflictRig) landWithSpend(stream string, ids ...string) {
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
		r.must(store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}, Usage: archiveSpend}))
		for range 4 {
			s = r.snap()
			if pr := s.Work.Card(id); pr.Col != sprint.Review || sprint.ReadsWanted(s, pr) == 0 {
				break
			}
			r.must(store.AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{id}}}))
			for _, rc := range r.snap().Readers.Of(id) {
				if rc.Col == sprint.Asked || rc.Col == sprint.Reading {
					r.must(store.ReadStep(sprint.ReadReq{As: rc.Row, Verdict: "ok", Finding: "f:1", Sel: sprint.Sel{IDs: []string{rc.ID}}}))
				}
			}
		}
		r.must(store.AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{id}}}))
	}
	r.must(store.MergeStep(sprint.MergeReq{Stream: stream, Cards: ids}))
	for _, id := range ids {
		require.Equal(r.t, sprint.Landed, r.snap().Work.Card(id).Col, id)
	}
}

// archiveLedger is what the ledger says of the sprint and of one stream: the work table's
// landed and all counts and its cost footer, the stream's cost cell, its costs by tier,
// and where each of its cards is.
type archiveLedger struct {
	Landed, All int64
	Footer      string
	Cost        string
	Costs       sprint.TierCosts
	Cards       map[string]string
}

func (r *conflictRig) shapes() (work, merge ntable.Table) {
	r.t.Helper()
	sh, err := r.m.Shapes(r.ctx, []string{"t-" + sprint.Work, "t-" + sprint.Merge})
	require.NoError(r.t, err)
	return sh[0], sh[1]
}

func (r *conflictRig) ledger(stream string) archiveLedger {
	r.t.Helper()
	work, _ := r.shapes()
	l := archiveLedger{Cards: map[string]string{}}
	j := work.Column(sprint.Landed)
	for _, row := range work.Rows {
		for k, c := range work.Columns {
			if c.HasSet() && k < len(row.Cells) {
				l.All += row.Cells[k].Count
				if k == j {
					l.Landed += row.Cells[k].Count
				}
			}
		}
		if row.Key == stream {
			l.Cost = row.Texts[sprint.Cost]
		}
	}
	text := ntable.Render(work, ntable.RenderOpts{Title: sprint.Work})
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	l.Footer = lines[len(lines)-1]
	s := r.snap()
	l.Costs = sprint.StreamTierCosts(s)[stream]
	for _, c := range s.Work.Cards() {
		if c.Placed() && c.Row == stream {
			l.Cards[c.ID] = c.Col
		}
	}
	for _, c := range s.Merge.Cards() {
		if c.Placed() && c.Row == stream {
			l.Cards["merge:"+c.ID] = c.Col
		}
	}
	return l
}

// archived is whether the stream's rows of the work and merge tables are hidden, each.
func (r *conflictRig) archived(stream string) (work, merge bool) {
	r.t.Helper()
	w, m := r.shapes()
	for _, row := range w.Rows {
		if row.Key == stream {
			work = row.Hidden
		}
	}
	for _, row := range m.Rows {
		if row.Key == stream {
			merge = row.Hidden
		}
	}
	return work, merge
}

func (r *conflictRig) notes(typ string) []sprint.Note {
	r.t.Helper()
	notes, _, err := r.m.NotesSince(r.ctx, "", 100000)
	require.NoError(r.t, err)
	var out []sprint.Note
	for _, n := range notes {
		if n.Type == typ {
			out = append(out, n)
		}
	}
	return out
}

func TestArchiveKeepsEveryLandedCardAndItsCost(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	r.must(store.AddStep(sprint.AddReq{Stream: "s2", Count: 1, Brief: "c: the other work (s2) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"}))
	r.tick()
	// s1 lands whole; the tick archives it, and the coordinator draws it again (the tick
	// leaves a stream unarchived by hand drawn): the sprint settled, s1 shown and landed
	r.landWithSpend("s1", "s1-1", "s1-2", "s1-3")
	r.tick()
	refused, err := r.st.ArchiveStreams(r.ctx, []string{"s1"}, false)
	require.NoError(t, err)
	require.Empty(t, refused)
	r.tick()
	if r.snap().Work.Card("s2-1").Col == sprint.Ready {
		r.must(dealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s2-1"}}}))
	}
	require.Equal(t, sprint.Working, r.snap().Work.Card("s2-1").Col)
	r.tick()
	before := r.ledger("s1")
	told := len(r.notes(sprint.NStreamArchived))
	require.Equal(t, 1, told, "the tick archived s1 once, and said so")
	require.Equal(t, int64(3), before.Landed)
	require.NotEmpty(t, before.Costs.TotalCost, "the landed cards carry their spend")
	require.True(t, strings.HasPrefix(before.Cost, "$"), "the stream's cost cell: %q", before.Cost)

	// a stream with a card not landed is refused, naming the card; all or none
	refused, err = r.st.ArchiveStreams(r.ctx, []string{"s2"}, true)
	require.NoError(t, err)
	require.Len(t, refused, 1)
	assert.Equal(t, "s2", refused[0].Key)
	assert.Contains(t, refused[0].Why, "s2-1 (working)")
	refused, err = r.st.ArchiveStreams(r.ctx, []string{"s1", "s2"}, true)
	require.NoError(t, err)
	require.Len(t, refused, 2, "one refused refuses the other with it")
	assert.Contains(t, refused[1].Why, "all or none")
	w, m := r.archived("s1")
	require.False(t, w || m, "nothing was changed")
	refused, err = r.st.ArchiveStreams(r.ctx, []string{"s9"}, true)
	require.NoError(t, err)
	require.Len(t, refused, 1)
	assert.Contains(t, refused[0].Why, "no stream s9")

	// archived on the RUNNING machine: the rows leave the drawn tables, the ledger is unchanged
	mr, _, err := r.st.Machine(r.ctx)
	require.NoError(t, err)
	require.True(t, mr.Running())
	refused, err = r.st.ArchiveStreams(r.ctx, []string{"s1"}, true)
	require.NoError(t, err)
	require.Empty(t, refused)
	w, m = r.archived("s1")
	require.True(t, w && m, "both rows hidden")
	work, merge := r.shapes()
	assert.NotContains(t, ntable.Render(work, ntable.RenderOpts{Title: sprint.Work}), "s1 ", "the work table draws no s1")
	assert.NotContains(t, ntable.Render(merge, ntable.RenderOpts{Title: sprint.Merge}), "s1 ", "the merge table draws no s1")
	assert.Contains(t, ntable.Render(work, ntable.RenderOpts{Title: sprint.Work}), "s2 ")
	assert.Equal(t, before, r.ledger("s1"), "every landed card, its cost and the totals are kept")
	sum := sprint.ArchiveSumOf(work)
	require.NotNil(t, sum)
	assert.Equal(t, []string{"s1"}, sum.Streams)
	assert.Equal(t, int64(3), sum.Landed)
	assert.Equal(t, before.Cost, sum.Cost)
	assert.Equal(t, "1 archived stream, 3 cards landed, "+before.Cost, sum.Line())

	// the machine goes on: a tick leaves it archived, says nothing, and the sprint is lawful
	r.tick()
	w, m = r.archived("s1")
	assert.True(t, w && m)
	assert.Len(t, r.notes(sprint.NStreamArchived), told, "a stream archived by hand is no news")
	assert.Equal(t, before, r.ledger("s1"))
	r.clean("archived")

	// unarchive restores it, and the tick leaves it shown: nothing of it landed since
	refused, err = r.st.ArchiveStreams(r.ctx, []string{"s1"}, false)
	require.NoError(t, err)
	require.Empty(t, refused)
	w, m = r.archived("s1")
	assert.False(t, w || m, "both rows drawn again")
	work, _ = r.shapes()
	assert.Contains(t, ntable.Render(work, ntable.RenderOpts{Title: sprint.Work}), "s1 ")
	assert.Nil(t, sprint.ArchiveSumOf(work))
	r.tick()
	w, _ = r.archived("s1")
	assert.False(t, w, "the tick does not archive a stream unarchived by hand again")
	assert.Equal(t, before, r.ledger("s1"))
	r.clean("unarchived")
}

func TestTheTickArchivesAStreamWhenItsLastCardLands(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	r.landWithSpend("s1", "s1-1", "s1-2")
	r.tick()
	w, _ := r.archived("s1")
	require.False(t, w, "s1-3 has not landed")
	assert.Empty(t, r.notes(sprint.NStreamArchived))

	r.landWithSpend("s1", "s1-3")
	before := r.ledger("s1")
	r.tick()
	w, m := r.archived("s1")
	require.True(t, w && m, "the tick archives the stream whose last card landed")
	after := r.ledger("s1")
	assert.Equal(t, before.Cards, after.Cards, "every card of it kept where it landed")
	assert.Equal(t, before.Costs, after.Costs, "and its cost")
	assert.Equal(t, int64(3), after.Landed)
	assert.Equal(t, int64(3), after.All)
	notes := r.notes(sprint.NStreamArchived)
	require.Len(t, notes, 1)
	assert.Equal(t, sprint.Happened, notes[0].Kind, "information, no judgment")
	assert.Contains(t, notes[0].What, "streams=s1")
	assert.Empty(t, r.open(sprint.NStreamArchived))
	r.tick()
	assert.Equal(t, after, r.ledger("s1"))
	assert.Len(t, r.notes(sprint.NStreamArchived), 1, "named once")

	// a card added to the archived stream draws it again, and its landing archives it again
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"s1-4"}, Brief: "c: more work (s1) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"}))
	r.tick()
	r.tick()
	w, m = r.archived("s1")
	require.False(t, w || m, "an archived stream that holds a card not landed is drawn again")
	r.landWithSpend("s1", "s1-4")
	r.tick()
	w, _ = r.archived("s1")
	assert.True(t, w)
	assert.Len(t, r.notes(sprint.NStreamArchived), 2)
	r.clean("archived by the tick")
}
