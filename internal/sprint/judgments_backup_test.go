package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestReadsBackedUpRaisesOneJudgmentAtTheEdge is the backup edges: one judgment
// when review rises above working, one when it does not, and the same pair for
// merging above review plus working. A tick that holds the predicate writes
// nothing. Both edges may flip in one tick, one judgment each.
func TestReadsBackedUpRaisesOneJudgmentAtTheEdge(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Now = t0
	w.s.Work.SetRows([]string{"s1", "old"})
	w.s.Work.SetHidden("old")
	w.s.Readers.SetRows([]string{"reader-m1", "reader-free"})
	w.s.Fleet.SetRows([]string{"m1"})
	w.s.Merge.SetRows([]string{"s1", "s9", "gone"})
	w.s.Merge.SetHidden("gone")
	hour := stamp(t0.Add(-time.Hour))
	two := stamp(t0.Add(-2 * time.Hour))
	put := func(tb *Table, id, row, col string, fields map[string]string) {
		tb.Put(&Card{ID: id, Row: row, Col: col, Score: 1, Rev: 1, Fields: fields})
	}
	put(w.s.Work, "s1-w", "s1", Working, map[string]string{"kind": "primary", "admitted": two})
	put(w.s.Work, "s1-a", "s1", Review, map[string]string{"kind": "primary", "finished_at": two, "admitted": two})
	put(w.s.Work, "s1-old", "old", Review, map[string]string{"kind": "primary", "finished_at": stamp(t0.Add(-3 * time.Hour))})
	put(w.s.Work, "s1-stop", "s1", Review, map[string]string{"kind": "sentinel", "finished_at": stamp(t0.Add(-4 * time.Hour))})
	put(w.s.Work, "s1-land", "s1", Landed, map[string]string{"kind": "primary", "landed": hour})
	put(w.s.Readers, "rd1", "reader-m1", Reading, map[string]string{"primary": "s1-a"})
	put(w.s.Fleet, "ctl-m1", "m1", Ctl, map[string]string{"width": "4"})
	put(w.s.Merge, "ctl-s9", "s9", Ctl, map[string]string{"state": StreamStopped})
	put(w.s.Merge, "ctl-gone", "gone", Ctl, map[string]string{"state": StreamStopped})

	// review equals working once the archived row and the sentinel are aside: no edge
	p, n := TickBackup(w.s, TickReq{})
	require.Zero(t, n)
	require.Empty(t, p.Notes)
	require.Empty(t, p.Closes)

	// review rises above working: one judgment, and the text is this edge's
	put(w.s.Work, "s1-b", "s1", Review, map[string]string{"kind": "primary", "finished_at": hour, "admitted": hour})
	put(w.s.Work, "s1-c", "s1", Review, map[string]string{"kind": "primary", "finished_at": stamp(t0.Add(-time.Minute)), "admitted": hour})
	p, n = TickBackup(w.s, TickReq{})
	require.Zero(t, n)
	require.Len(t, p.Notes, 1)
	require.Empty(t, p.Closes)
	got := p.Notes[0]
	require.Equal(t, Judgment, got.Kind)
	require.Equal(t, NReadsBackedUp, got.Type)
	require.True(t, got.StreamLevel)
	require.Empty(t, got.Stream)
	require.True(t, got.Marked)
	require.Equal(t, MachineActor, got.Who)
	require.Equal(t, []string{"s1-a"}, got.Primaries)
	require.Equal(t, []string{"ack", "wait"}, got.Decisions)
	require.Contains(t, got.What, "review 3 is above working 1, merging 0")
	require.Contains(t, got.What, "oldest in review is s1-a, age 2h0m0s")
	require.Contains(t, got.What, "readers reading 1 of width 4")
	require.Contains(t, got.What, "lander last landed at "+hour)
	require.Contains(t, got.What, "stopped streams s9")
	require.Contains(t, got.What, "nova-sprint reader set --tiers")
	require.Contains(t, got.What, "nova-sprint fleet up --width")
	require.Contains(t, got.What, "nova-sprint route enable")
	require.Contains(t, got.What, "nova-sprint resume --stream")
	w.must(p)

	// the predicate holds, and the counts move: still one judgment, not one a tick
	put(w.s.Work, "s1-d", "s1", Review, map[string]string{"kind": "primary", "admitted": hour})
	for i := 0; i < 3; i++ {
		w.tick(time.Minute)
		p, n = TickBackup(w.s, TickReq{})
		require.Zero(t, n)
		require.Empty(t, p.Notes, "tick %d while review stays above working", i)
		require.Empty(t, p.Closes, "tick %d while review stays above working", i)
	}

	// an acknowledgement holds it; a wait that has run out raises it once more
	held := w.openOn(StreamSubject(""))
	require.Len(t, held, 1)
	w.s.Open = nil
	held[0].Note.Kind = Acknowledged
	w.s.Acked = append([]Open(nil), held...)
	p, _ = TickBackup(w.s, TickReq{})
	require.Empty(t, p.Notes)
	require.Empty(t, p.Closes)
	w.s.Acked[0].Note.Review = w.s.Now.Add(-time.Minute)
	w.s.Acked[0].Note.ReviewSet = w.s.Now.Add(-2 * time.Minute)
	p, _ = TickBackup(w.s, TickReq{})
	require.Len(t, p.Notes, 1)
	require.Equal(t, NReadsBackedUp, p.Notes[0].Type)
	require.Len(t, p.Closes, 1)
	w.s.Acked = nil
	w.must(p)

	// review falls back to working: one clear judgment, and the rising note closes
	for _, id := range []string{"s1-a", "s1-b", "s1-c", "s1-d"} {
		c := w.s.Work.Card(id)
		c.Col = Working
		w.s.Work.Put(c)
	}
	p, _ = TickBackup(w.s, TickReq{})
	require.Len(t, p.Notes, 1)
	require.Equal(t, NReadsClear, p.Notes[0].Type)
	require.Contains(t, p.Notes[0].What, "review 0 is not above working 5, merging 0")
	require.Contains(t, p.Notes[0].What, "oldest in review is none")
	require.Len(t, p.Closes, 1)
	require.Equal(t, NReadsBackedUp, p.Closes[0].Note.Type)
	w.must(p)
	p, _ = TickBackup(w.s, TickReq{})
	require.Empty(t, p.Notes)
	require.Empty(t, p.Closes)

	// it rises again: the clear note closes and one rising judgment is written
	for _, id := range []string{"s1-a", "s1-b", "s1-c"} {
		c := w.s.Work.Card(id)
		c.Col = Review
		w.s.Work.Put(c)
	}
	p, _ = TickBackup(w.s, TickReq{})
	require.Len(t, p.Notes, 1)
	require.Equal(t, NReadsBackedUp, p.Notes[0].Type)
	require.Len(t, p.Closes, 1)
	require.Equal(t, NReadsClear, p.Closes[0].Note.Type)

	// merging alone, then both edges in one tick
	m := newWorld(t)
	m.s.Now = t0
	m.s.Work.SetRows([]string{"s1"})
	put(m.s.Work, "s1-w", "s1", Working, map[string]string{"kind": "primary"})
	put(m.s.Work, "s1-m", "s1", Merging, map[string]string{"kind": "primary", "accepted": two})
	put(m.s.Work, "s1-n", "s1", Merging, map[string]string{"kind": "primary", "accepted": hour})
	p, _ = TickBackup(m.s, TickReq{})
	require.Len(t, p.Notes, 1)
	require.Equal(t, NMergesBackedUp, p.Notes[0].Type)
	require.Contains(t, p.Notes[0].What, "merging 2 is above review 0 plus working 1")
	require.Contains(t, p.Notes[0].What, "oldest in merging is s1-m, age 2h0m0s")
	require.Equal(t, []string{"s1-m"}, p.Notes[0].Primaries)
	m.must(p)
	p, _ = TickBackup(m.s, TickReq{})
	require.Empty(t, p.Notes)
	for _, id := range []string{"s1-m", "s1-n"} {
		c := m.s.Work.Card(id)
		c.Col = Working
		m.s.Work.Put(c)
	}
	p, _ = TickBackup(m.s, TickReq{})
	require.Len(t, p.Notes, 1)
	require.Equal(t, NMergesClear, p.Notes[0].Type)
	require.Contains(t, p.Notes[0].What, "merging 0 is not above review 0 plus working 3")
	require.Len(t, p.Closes, 1)

	both := newWorld(t)
	both.s.Now = t0
	both.s.Work.SetRows([]string{"s1"})
	put(both.s.Work, "s1-w", "s1", Working, map[string]string{"kind": "primary"})
	put(both.s.Work, "s1-r", "s1", Review, map[string]string{"kind": "primary", "finished_at": hour})
	put(both.s.Work, "s1-r2", "s1", Review, map[string]string{"kind": "primary", "finished_at": hour})
	put(both.s.Work, "s1-m1", "s1", Merging, map[string]string{"kind": "primary", "accepted": hour})
	put(both.s.Work, "s1-m2", "s1", Merging, map[string]string{"kind": "primary", "accepted": hour})
	put(both.s.Work, "s1-m3", "s1", Merging, map[string]string{"kind": "primary", "accepted": hour})
	put(both.s.Work, "s1-m4", "s1", Merging, map[string]string{"kind": "primary"})
	p, _ = TickBackup(both.s, TickReq{})
	require.Len(t, p.Notes, 2)
	require.Equal(t, NReadsBackedUp, p.Notes[0].Type)
	require.Equal(t, NMergesBackedUp, p.Notes[1].Type)
	both.must(p)
	p, _ = TickBackup(both.s, TickReq{})
	require.Empty(t, p.Notes)
	require.Empty(t, p.Closes)

	seen, beforeDone := false, false
	for i, part := range TickEnd {
		if part.Name != PartBackup {
			continue
		}
		seen = true
		beforeDone = i+1 < len(TickEnd) && TickEnd[i+1].Name == PartDone
	}
	require.True(t, seen)
	require.True(t, beforeDone)
	for _, part := range TickParts {
		require.NotEqual(t, PartBackup, part.Name)
	}
}
