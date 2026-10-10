package sprint

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Review starved and reads idle (docs/SPEC-SPRINT.md, "Review starved, and reads idle").
// The threshold is running time, default two ticks (2s). A second call at the same
// clock writes nothing: the store's quiet ticks call the machine twice without
// advancing time, and those two calls are not two ticks of the threshold.

func reviewAlarmWhen() time.Time {
	return time.Date(2026, 10, 7, 21, 40, 0, 0, time.UTC)
}

// reviewAlarmBoard is four cards in review whose attempts came back ok, and a
// fleet with no read out and no reader up. Each wants one read. None is free.
func reviewAlarmBoard() *Snapshot {
	work := NewTable(Work)
	work.SetRows([]string{"s1"})
	work.SetProp(PropReadCards, ReadCardsOnWord)
	for i, id := range []string{"s1-1", "s1-2", "s1-3", "s1-4"} {
		work.Put(&Card{
			ID: id, Row: "s1", Col: Review, Score: float64(i + 1),
			Fields: map[string]string{"result": "ok"},
		})
	}
	fleet := NewTable(Fleet)
	fleet.SetRows([]string{"m1"})
	return &Snapshot{Now: reviewAlarmWhen(), Coordinator: "coord", Work: work, Fleet: fleet}
}

// applyReviewAlarm commits the part's fleet properties and its judgments onto
// the snapshot, the way the next tick would read them. A pure test has no store
// to do it.
func applyReviewAlarm(s *Snapshot, p Plan) {
	for _, pw := range p.Props {
		tb := s.T(pw.Table)
		if pw.Value == "" {
			props := tb.Props()
			delete(props, pw.Name)
			tb.SetProps(props)
			continue
		}
		tb.SetProp(pw.Name, pw.Value)
	}
	seq := 0
	for _, n := range p.Notes {
		if n.Kind != Judgment {
			continue
		}
		seq++
		if n.ID == "" {
			n.ID = fmt.Sprintf("j%d", seq)
		}
		for _, sub := range n.Subjects() {
			s.Open = append(s.Open, Open{Key: OpenKey(n.ID, sub), Note: n})
		}
	}
	if len(p.Closes) == 0 {
		return
	}
	closed := map[string]bool{}
	for _, c := range p.Closes {
		closed[c.Key] = true
	}
	var keep []Open
	for _, o := range s.Open {
		if !closed[o.Key] {
			keep = append(keep, o)
		}
	}
	s.Open = keep
}

func reviewAlarmJudgments(p Plan, typ string) []Note {
	var out []Note
	for _, n := range p.Notes {
		if n.Kind == Judgment && n.Type == typ {
			out = append(out, n)
		}
	}
	return out
}

func TestReviewStarvedRaisesOnceWhenNoReadIsOutForTwoTicks(t *testing.T) {
	t.Parallel()
	s := reviewAlarmBoard()
	r := TickReq{Who: "seat"}

	p, due := TickReviewStarved(s, r)
	require.Zero(t, due)
	require.Empty(t, p.Notes)
	require.Len(t, p.Props, 1)
	require.Equal(t, PropWrite{Table: Fleet, Name: PropReviewStarvedEp, Value: reviewAlarmEncode(s.Now, false), WasAbsent: true}, p.Props[0])
	require.Len(t, p.Units, 1)
	require.Equal(t, PartReviewStarved, p.Units[0].Key)
	require.Empty(t, p.Units[0].Moved)
	applyReviewAlarm(s, p)

	// The same clock again is not a second tick of running time.
	p, due = TickReviewStarved(s, r)
	require.Zero(t, due)
	require.True(t, p.Empty())
	require.Empty(t, p.Props)

	s.Now = s.Now.Add(reviewStarvedDefault)
	p, due = TickReviewStarved(s, r)
	require.Zero(t, due)
	got := reviewAlarmJudgments(p, NReviewStarved)
	require.Len(t, got, 1)
	require.Empty(t, reviewAlarmJudgments(p, NReadsIdle))
	n := got[0]
	require.Equal(t, "review starved: 4 cards in review want a read and no read is out: s1-1 (no reader up may read it: no friend whose tiers reach its read tier, and no member whose reader row serves its tier, besides its own worker); s1-2 (no reader up may read it: no friend whose tiers reach its read tier, and no member whose reader row serves its tier, besides its own worker); s1-3 (no reader up may read it: no friend whose tiers reach its read tier, and no member whose reader row serves its tier, besides its own worker)", n.What)
	require.NotContains(t, n.What, "s1-4")
	require.Equal(t, []string{"s1-1", "s1-2", "s1-3"}, n.Primaries)
	require.Equal(t, 4, n.Count)
	require.Equal(t, []string{"ack", "wait"}, n.Decisions)
	require.True(t, n.SprintLevel)
	require.Equal(t, "seat", n.Who)
	require.Equal(t, s.Now, n.At)
	require.Len(t, p.Props, 1)
	require.Equal(t, reviewAlarmSaid(reviewAlarmEncode(reviewAlarmWhen(), false)), p.Props[0].Value)
	applyReviewAlarm(s, p)

	s.Now = s.Now.Add(reviewStarvedDefault)
	p, _ = TickReviewStarved(s, r)
	require.Empty(t, p.Notes, "one judgment per episode")
	require.Empty(t, p.Props)
}

func TestReviewStarvedRaisesWithDefaultLegacyReaders(t *testing.T) {
	t.Parallel()
	s := reviewAlarmBoard()
	props := s.Work.Props()
	delete(props, PropReadCards)
	s.Work.SetProps(props)
	require.False(t, s.ReadCardsOn())
	r := TickReq{Who: "seat"}

	p, due := TickReviewStarved(s, r)
	require.Zero(t, due)
	require.Empty(t, p.Notes)
	require.Len(t, p.Props, 1)
	require.Equal(t, PropReviewStarvedEp, p.Props[0].Name)
	applyReviewAlarm(s, p)

	s.Now = s.Now.Add(reviewStarvedDefault)
	p, due = TickReviewStarved(s, r)
	require.Zero(t, due)
	require.Len(t, reviewAlarmJudgments(p, NReviewStarved), 1)
}

func TestReviewStarvedClosesWhenAReadGoesOut(t *testing.T) {
	t.Parallel()
	s := reviewAlarmBoard()
	r := TickReq{Who: "seat"}
	applyReviewAlarm(s, mustReviewAlarm(t, s, r))
	s.Now = s.Now.Add(reviewStarvedDefault)
	raised := mustReviewAlarm(t, s, r)
	require.Len(t, reviewAlarmJudgments(raised, NReviewStarved), 1)
	applyReviewAlarm(s, raised)
	require.Len(t, s.Open, 1)

	s.Fleet.Put(&Card{
		ID: "other.r1.m1", Row: "m1", Col: Ready,
		Fields: map[string]string{"kind": "read", "primary": "other"},
	})
	p, due := TickReviewStarved(s, r)
	require.Zero(t, due)
	require.Empty(t, reviewAlarmJudgments(p, NReviewStarved))
	require.Equal(t, s.Open, p.Closes)
	var notes []Note
	for _, n := range p.Notes {
		if n.Kind == Happened && n.Type == NReviewAlarmCleared {
			notes = append(notes, n)
		}
	}
	require.Len(t, notes, 1)
	require.Equal(t, "review starved: a read is out", notes[0].What)
	require.Equal(t, "coord", notes[0].To)
	require.Equal(t, "seat", notes[0].Who)
	require.Equal(t, s.Now, notes[0].At)
	cleared := false
	for _, pw := range p.Props {
		if pw.Name == PropReviewStarvedEp {
			require.Empty(t, pw.Value)
			cleared = true
		}
	}
	require.True(t, cleared)
}

func TestReviewStarvedOffWritesNothing(t *testing.T) {
	t.Parallel()
	s := reviewAlarmBoard()
	s.Work.SetProp(PropReviewStarved, AlarmOff)
	r := TickReq{Who: "seat"}
	p, due := TickReviewStarved(s, r)
	require.Zero(t, due)
	require.True(t, p.Empty())
	require.Empty(t, p.Props)
	require.Empty(t, p.Notes)
	s.Now = s.Now.Add(reviewStarvedDefault)
	p, due = TickReviewStarved(s, r)
	require.Zero(t, due)
	require.True(t, p.Empty())
	require.Empty(t, p.Props)
	require.Empty(t, p.Notes)
}

func TestReadsIdleRaisesWhenReadersAreFreeAndReadsWait(t *testing.T) {
	t.Parallel()
	s := reviewAlarmBoard()
	// A read of some other primary is already out, so the starved episode stays
	// quiet, and two readers are up with nothing asked of them.
	s.Fleet.Put(&Card{
		ID: "other.r1.m1", Row: "m1", Col: Ready,
		Fields: map[string]string{"kind": "read", "primary": "other"},
	})
	s.Readers = NewTable(Readers)
	s.Readers.SetRows([]string{"reader-a", "reader-b"})
	r := TickReq{Who: "seat"}

	p, due := TickReviewStarved(s, r)
	require.Zero(t, due)
	require.Empty(t, p.Notes)
	require.Empty(t, reviewAlarmJudgments(p, NReviewStarved))
	require.Len(t, p.Props, 1)
	require.Equal(t, PropReadsIdleEp, p.Props[0].Name)
	applyReviewAlarm(s, p)

	s.Now = s.Now.Add(reviewStarvedDefault)
	p, due = TickReviewStarved(s, r)
	require.Zero(t, due)
	got := reviewAlarmJudgments(p, NReadsIdle)
	require.Len(t, got, 1)
	require.Empty(t, reviewAlarmJudgments(p, NReviewStarved))
	require.Equal(t, "reads idle: 2 readers free and 4 cards in review want a read", got[0].What)
	require.Equal(t, 4, got[0].Count)
	require.Nil(t, got[0].Primaries)
	require.Equal(t, []string{"ack", "wait"}, got[0].Decisions)
	require.True(t, got[0].SprintLevel)
	require.Equal(t, "seat", got[0].Who)
}

func TestReviewStarvedWindowNeverBelowOneTick(t *testing.T) {
	t.Parallel()
	s := reviewAlarmBoard()
	s.Work.SetProp(PropReviewStarved, "500ms")
	r := TickReq{}
	start := s.Now
	p, _ := TickReviewStarved(s, r)
	require.Empty(t, p.Notes)
	applyReviewAlarm(s, p)

	s.Now = start.Add(500 * time.Millisecond)
	p, _ = TickReviewStarved(s, r)
	require.Empty(t, p.Notes)
	require.Empty(t, p.Props)

	s.Now = start.Add(reviewStarvedTick)
	p, _ = TickReviewStarved(s, r)
	require.Len(t, reviewAlarmJudgments(p, NReviewStarved), 1)
}

func TestReviewStarvedRunsInDoneWithoutAddingAnOperation(t *testing.T) {
	t.Parallel()
	for _, parts := range [][]TickPartDef{TickEnd, TickEndWith(false, false), TickEndWith(true, true)} {
		require.Equal(t, PartDone, parts[len(parts)-1].Name)
		for _, p := range parts {
			require.NotEqual(t, PartReviewStarved, p.Name)
		}
		alarm, due := parts[len(parts)-1].Fn(reviewAlarmBoard(), TickReq{})
		require.Zero(t, due)
		require.Len(t, alarm.Props, 1)
		require.Equal(t, PropReviewStarvedEp, alarm.Props[0].Name)
	}
	for _, p := range TickParts {
		require.NotEqual(t, PartReviewStarved, p.Name)
	}
}

func TestReviewStarvedNilSnapshotIsEmpty(t *testing.T) {
	t.Parallel()
	p, due := TickReviewStarved(nil, TickReq{})
	require.Zero(t, due)
	require.True(t, p.Empty())
	require.Empty(t, p.Props)

	p, due = TickReviewStarved(&Snapshot{}, TickReq{})
	require.Zero(t, due)
	require.True(t, p.Empty())

	p, due = TickReviewStarved(&Snapshot{Work: NewTable(Work)}, TickReq{})
	require.Zero(t, due)
	require.True(t, p.Empty())
}

func mustReviewAlarm(t *testing.T, s *Snapshot, r TickReq) Plan {
	t.Helper()
	p, due := TickReviewStarved(s, r)
	require.Zero(t, due)
	return p
}
