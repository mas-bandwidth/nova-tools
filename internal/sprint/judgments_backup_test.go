package sprint

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
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
	require.Contains(t, got.What, "run: nova-sprint reader set reader-m1 reader-free --tiers default; nova-sprint fleet up m1 --width 4; no disabled route to enable (nova-sprint has no route verb); nova-sprint resume --stream s9")
	require.NotContains(t, got.What, "route enable")
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

	// an acknowledgement holds the episode. A wait that has run out is not an
	// edge: the counts have not crossed, so the tick writes nothing and leaves
	// the acknowledgement where it is.
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
	require.Empty(t, p.Notes)
	require.Empty(t, p.Closes)
	require.Len(t, w.s.Acked, 1)
	require.Equal(t, NReadsBackedUp, w.s.Acked[0].Note.Type)
	p, _ = TickBackup(w.s, TickReq{})
	require.Empty(t, p.Notes)
	require.Empty(t, p.Closes)

	// review falls back to working: one clear judgment, and the acked rising note closes
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
	w.s.Acked = nil // the harness applies a close onto Open; a store drops this closed ack
	w.must(p)
	p, _ = TickBackup(w.s, TickReq{})
	require.Empty(t, p.Notes)
	require.Empty(t, p.Closes)

	// the clear episode, acknowledged, stays quiet after its wait runs out
	held = w.openOn(StreamSubject(""))
	require.Len(t, held, 1)
	w.s.Open = nil
	held[0].Note.Kind = Acknowledged
	w.s.Acked = append([]Open(nil), held...)
	w.s.Acked[0].Note.Review = w.s.Now.Add(-time.Minute)
	w.s.Acked[0].Note.ReviewSet = w.s.Now.Add(-2 * time.Minute)
	p, _ = TickBackup(w.s, TickReq{})
	require.Empty(t, p.Notes)
	require.Empty(t, p.Closes)
	require.Equal(t, NReadsClear, w.s.Acked[0].Note.Type)

	// it rises again: the acked clear note closes and one rising judgment is written
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

// TestBackupRecoveryNamesTheRowsThatWidenIt pins the run: line to the rows the
// snapshot holds: each reader's shown tiers, each member's width, each disabled
// route nova-config can name, and each stopped stream. A nil snapshot is none.
func TestBackupRecoveryNamesTheRowsThatWidenIt(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Readers: NewTable(Readers),
		Fleet:   NewTable(Fleet),
		Merge:   NewTable(Merge),
		Work:    NewTable(Work),
		Routes: []Route{
			{Name: "flash-on", Enabled: true},
			{Name: "zebra-off", Enabled: false},
			{Name: "Bad_Name", Enabled: false},
			{Name: "flash-off", Enabled: false},
			{Name: "flash-off", Enabled: false},
		},
	}
	s.Readers.SetRows([]string{"reader-pro", "reader-m1", "hidden-r", "bad name"})
	s.Readers.SetHidden("hidden-r")
	s.Readers.Texts = map[string]map[string]string{"reader-pro": {ReaderTiers: "pro"}}
	s.Fleet.SetRows([]string{"m1", "m2", "hidden-m", "m3"})
	s.Fleet.SetHidden("hidden-m")
	s.Fleet.Put(&Card{ID: "ctl-m1", Row: "m1", Col: Ctl, Score: 1, Rev: 1, Fields: map[string]string{FieldWidth: "4"}})
	s.Fleet.Put(&Card{ID: "ctl-m2", Row: "m2", Col: Ctl, Score: 1, Rev: 1, Fields: map[string]string{FieldWidth: "0"}})
	s.Merge.SetRows([]string{"s9", "gone", "s2"})
	s.Merge.SetHidden("gone")
	s.Merge.Put(&Card{ID: "ctl-s9", Row: "s9", Col: Ctl, Score: 1, Rev: 1, Fields: map[string]string{"state": StreamStopped}})
	s.Merge.Put(&Card{ID: "ctl-gone", Row: "gone", Col: Ctl, Score: 1, Rev: 1, Fields: map[string]string{"state": StreamStopped}})
	s.Merge.Put(&Card{ID: "ctl-s2", Row: "s2", Col: Ctl, Score: 1, Rev: 1, Fields: map[string]string{"state": StreamStopped}})

	got := BackupRecoveryCommands(s)
	require.Equal(t, []string{
		"nova-sprint reader set reader-pro --tiers pro",
		"nova-sprint reader set reader-m1 --tiers default",
		"nova-sprint fleet up m1 --width 4",
		"nova-sprint fleet up m2 --width 0",
		"nova-sprint fleet up m3 --width " + strconv.Itoa(DefaultWidth),
		"nova-config route set flash-off --enabled true",
		"nova-config route set zebra-off --enabled true",
		"nova-config apply --kind route",
		"nova-sprint resume --stream s9,s2",
	}, got)
	require.Nil(t, BackupRecoveryCommands(nil))

	require.NoError(t, config.ValidateName("flash-off"))
	require.Error(t, config.ValidateName("Bad_Name"))
	k, ok := config.Lookup(config.KindRoute)
	require.True(t, ok)
	changes, err := k.Changes(map[string]string{"enabled": "true"})
	require.NoError(t, err)
	require.Equal(t, "true", changes["enabled"])
	_, err = k.Changes(map[string]string{"enabled": "false"})
	require.Error(t, err)
	require.Contains(t, config.KindNames(), config.KindRoute)
}
