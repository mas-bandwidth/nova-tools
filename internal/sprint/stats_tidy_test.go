package sprint_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// Stats tidy on the twin store (the owner, 2026-10-06: "can you please clear the sets of
// done consumer cards for all friends and fleet", "I would like a semi-fresh start to stats
// now"; the verb renamed tidy the same day, "reset sounds too aggressive"). The done cells
// lose their history, the archive keeps it, and the work stays where it was.

// places is every card of every table at its place and generation, by table and id.
func places(s *sprint.Snapshot) map[string]map[string]string {
	out := map[string]map[string]string{}
	for name, t := range map[string]*sprint.Table{sprint.Work: s.Work, sprint.Merge: s.Merge, sprint.Readers: s.Readers, sprint.Fleet: s.Fleet} {
		out[name] = map[string]string{}
		for _, c := range t.Cards() {
			if c.Placed() {
				out[name][c.ID] = c.Row + ":" + c.Col + "@" + c.F("gen")
			}
		}
	}
	return out
}

// doneCounts is each fleet row's ok and failed counts, as the table's done and ok% count them.
func doneCounts(s *sprint.Snapshot) map[string][2]int {
	out := map[string][2]int{}
	for _, row := range s.Fleet.Rows() {
		out[row] = [2]int{s.Fleet.Count(row, sprint.DoneOK), s.Fleet.Count(row, sprint.DoneFailed)}
	}
	return out
}

func TestStatsTidyZeroesCountersAndKeepsTheWork(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	_, _, _, err := r.st.SetMachine(r.ctx, false)
	require.NoError(t, err)
	// s1's three cards landed, priced: their work cards are history on m1 and m2
	r.landWithCost("s1", "s1-1", "s1-2", "s1-3")
	// s2: one card merging, one in review, one working, one dealt, one ready: the work
	r.must(store.AddStep(sprint.AddReq{Stream: "s2", Count: 5, Brief: "c: the work (s2) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"}))
	r.toMerging("s2-1")
	r.must(dealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s2-2", "s2-3", "s2-4"}}}))
	for _, id := range []string{"s2-2", "s2-3"} {
		wc := r.snap().Fleet.Card(r.snap().Work.Card(id).F("work"))
		r.must(store.TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	}
	wc := r.snap().Fleet.Card(r.snap().Work.Card("s2-2").F("work"))
	r.must(store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	// an hour on: no finish is within the friend-finish window
	r.mu.Lock()
	r.now = r.now.Add(time.Hour)
	r.mu.Unlock()

	before := r.snap()
	cols := map[string]string{}
	for _, id := range []string{"s1-1", "s1-2", "s1-3", "s2-1", "s2-2", "s2-3", "s2-4", "s2-5"} {
		cols[id] = before.Work.Card(id).Col
	}
	assert.Equal(t, map[string]string{"s1-1": sprint.Landed, "s1-2": sprint.Landed, "s1-3": sprint.Landed, "s2-1": sprint.Merging,
		"s2-2": sprint.Review, "s2-3": sprint.Working, "s2-4": sprint.Working, "s2-5": sprint.Ready}, cols, "cards in every column")
	assert.Equal(t, sprint.Ready, before.Fleet.Card(before.Work.Card("s2-4").F("work")).Col, "s2-4 dealt, not taken")
	counts := doneCounts(before)
	total := 0
	for _, c := range counts {
		total += c[0] + c[1]
	}
	require.Equal(t, 5, total, "three landed, one merging, one in review: five done")
	open, err := r.m.OpenNotes(r.ctx)
	require.NoError(t, err)
	where := places(before)
	costBefore := before.StreamCtl("s1").F(sprint.FieldCost)
	require.NotEmpty(t, costBefore)

	// --dry-run says what would move and writes nothing
	dry, err := r.st.TidyStats(r.ctx, store.TidyReq{Kinds: sprint.TidyKinds, Reason: "a fresh start", DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, 3, dry.Moved)
	assert.Equal(t, 2, dry.Kept)
	assert.Equal(t, where, places(r.snap()), "a dry run moves nothing")
	rec, err := r.st.StatsTidied(r.ctx)
	require.NoError(t, err)
	assert.Zero(t, rec.Since, "a dry run records nothing")

	res, err := r.st.TidyStats(r.ctx, store.TidyReq{Kinds: sprint.TidyKinds, Reason: "a fresh start"})
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	assert.Equal(t, 3, res.Moved)
	assert.Equal(t, 2, res.Kept)
	at := r.st.Now().UTC()
	assert.Equal(t, "stats:archive:"+at.Format(time.RFC3339), res.Archive)

	after := r.snap()
	// the counters: only the live work's cards stay done (s2-1 merging, s2-2 in review)
	var kept []string
	for _, row := range after.Fleet.Rows() {
		for _, c := range append(after.Fleet.Cell(row, sprint.DoneOK), after.Fleet.Cell(row, sprint.DoneFailed)...) {
			kept = append(kept, c.F(sprint.PrimaryField))
		}
	}
	assert.ElementsMatch(t, []string{"s2-1", "s2-2"}, kept, "the history left the done cells; the live work's cards stayed")
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		w := after.Fleet.Placed(sprint.WorkCardID(id, 1))
		assert.Nil(t, w, "%s's work card is off the done cell", id)
	}
	// every record kept: read by id, as stats reads it
	s, err := r.st.Load(r.ctx, []string{sprint.Work, sprint.Fleet, sprint.Readers}, sprint.StatsRecords)
	require.NoError(t, err)
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		require.NotNil(t, s.Fleet.Card(sprint.WorkCardID(id, 1)), "%s's work card record is kept", id)
		assert.Equal(t, "yes", s.Fleet.Card(sprint.WorkCardID(id, 1)).F("ok"))
	}
	// every other card where it was: work, merge, readers, and the fleet's open cells
	got := places(after)
	for _, name := range []string{sprint.Work, sprint.Merge, sprint.Readers} {
		assert.Equal(t, where[name], got[name], "the %s table is untouched", name)
	}
	for id, place := range where[sprint.Fleet] {
		if c := before.Fleet.Card(id); c.Col == sprint.DoneOK || c.Col == sprint.DoneFailed {
			continue
		}
		assert.Equal(t, place, got[sprint.Fleet][id], "%s stays where it was", id)
	}
	for id := range cols {
		assert.Equal(t, cols[id], after.Work.Card(id).Col, id)
	}
	openAfter, err := r.m.OpenNotes(r.ctx)
	require.NoError(t, err)
	assert.Equal(t, open, openAfter, "the judgments are unchanged")
	r.clean("tidied")

	// the archive holds the old values
	var arch store.StatsArchive
	raw, ok, err := r.m.GetKey(r.ctx, res.Archive)
	require.NoError(t, err)
	require.True(t, ok, "the archive record is written")
	require.NoError(t, json.Unmarshal([]byte(raw), &arch))
	assert.Equal(t, "a fresh start", arch.Reason)
	assert.Equal(t, sprint.TidyKinds, arch.Kinds)
	archived := map[string][2]int{}
	var moved []string
	for _, row := range arch.Rows {
		archived[row.Row] = [2]int{row.OK, row.Failed}
		moved = append(moved, row.Moved...)
	}
	for row, c := range counts {
		if c[0]+c[1] > 0 {
			assert.Equal(t, c, archived[row], "%s's counters are archived", row)
		}
	}
	assert.ElementsMatch(t, []string{sprint.WorkCardID("s1-1", 1), sprint.WorkCardID("s1-2", 1), sprint.WorkCardID("s1-3", 1)}, moved)
	assert.Equal(t, sprint.StreamBase{Cost: costBefore, Landed: 3}, arch.Streams["s1"])
	assert.NotEmpty(t, arch.Routes, "the route counters are archived")

	// the record names the tidy; the stream's cost counts from it
	rec, err = r.st.StatsTidied(r.ctx)
	require.NoError(t, err)
	assert.Equal(t, at, rec.Since)
	assert.Equal(t, "a fresh start", rec.Reason)
	assert.Equal(t, []string{res.Archive}, rec.Archives)
	since, err := r.st.StatsSince(r.ctx, sprint.TidyStreams)
	require.NoError(t, err)
	assert.Equal(t, at, since)
	pinned, err := r.st.Pinned(r.ctx)
	require.NoError(t, err)
	shapes, err := r.m.Shapes(r.ctx, []string{pinned.Names.Table(sprint.Work)})
	require.NoError(t, err)
	row := shapes[0].Row("s1")
	require.GreaterOrEqual(t, row, 0)
	assert.Equal(t, "-", shapes[0].Rows[row].Texts[sprint.Cost], "nothing landed in s1 since the tidy")
	assert.Equal(t, costBefore, after.StreamCtl("s1").F(sprint.FieldCost), "the control card keeps the epoch's cost")

	// stats counts from the tidy: nothing has happened since; the whole epoch is still there
	ps := sprint.StatsSince(s, since)
	assert.Equal(t, since, ps.Since)
	assert.Zero(t, ps.Primaries)
	assert.Empty(t, ps.Work)
	assert.Empty(t, ps.Routes)
	assert.Equal(t, 8, sprint.Stats(s).Primaries)
}

func TestASecondStatsTidyWithinAMinuteIsRefused(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	_, _, _, err := r.st.SetMachine(r.ctx, false)
	require.NoError(t, err)
	r.landWithCost("s1", "s1-1")
	r.mu.Lock()
	r.now = r.now.Add(time.Hour)
	r.mu.Unlock()
	r.beat()
	first, err := r.st.TidyStats(r.ctx, store.TidyReq{Kinds: []string{sprint.TidyFleet}, Reason: "first"})
	require.NoError(t, err)
	require.Empty(t, first.Refused)
	r.mu.Lock()
	r.now = r.now.Add(59 * time.Second)
	r.mu.Unlock()
	r.beat()
	r.landWithCost("s1", "s1-2")
	r.mu.Lock()
	r.now = r.now.Add(time.Hour - 59*time.Second - 2*time.Second)
	r.mu.Unlock()
	where := places(r.snap())
	second, err := r.st.TidyStats(r.ctx, store.TidyReq{Kinds: []string{sprint.TidyFleet}, Reason: "second"})
	require.NoError(t, err)
	assert.Empty(t, second.Refused, "an hour on, a second tidy runs")
	r.mu.Lock()
	r.now = r.now.Add(30 * time.Second)
	r.mu.Unlock()
	third, err := r.st.TidyStats(r.ctx, store.TidyReq{Kinds: []string{sprint.TidyFleet}, Reason: "third"})
	require.NoError(t, err)
	assert.Contains(t, third.Refused, "a second within 1m0s is refused, nothing written")
	assert.NotEqual(t, where, places(r.snap()), "the second tidy moved s1-2's card")
	rec, err := r.st.StatsTidied(r.ctx)
	require.NoError(t, err)
	assert.Equal(t, "second", rec.Reason, "the refused tidy wrote nothing")
	assert.Len(t, rec.Archives, 2)
}
