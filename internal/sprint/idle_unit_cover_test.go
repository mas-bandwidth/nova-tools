package sprint

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// idleCoverSnapshot is deliberately small: TraceIdle and TickIdle are planners over
// a snapshot, so these tests keep their roots visible instead of going through a store.
func idleCoverSnapshot() *Snapshot {
	s := &Snapshot{Now: t0, Coordinator: "coordinator", Work: NewTable(Work), Merge: NewTable(Merge), Fleet: NewTable(Fleet)}
	s.Work.SetRows([]string{"s"})
	return s
}

func idleCoverCard(id, row, col string, fields map[string]string) *Card {
	if fields == nil {
		fields = map[string]string{}
	}
	return &Card{ID: id, Row: row, Col: col, Fields: fields}
}

func idleCoverPut(s *Snapshot, cards ...*Card) {
	for _, c := range cards {
		s.Work.Put(c)
	}
}

func idleCoverWaiting(s *Snapshot, id string, needs ...string) *Card {
	c := idleCoverCard(id, "s", Waiting, map[string]string{})
	if len(needs) > 0 {
		c.Fields["needs"] = strings.Join(needs, ",")
	}
	s.Work.Put(c)
	return c
}

func TestSprintIdleCoverRank(t *testing.T) {
	t.Parallel()
	assert.Less(t, rank(rootDropped), rank(rootMissing))
	assert.Less(t, rank(rootMissing), rank(rootJudgment))
	assert.Less(t, rank(rootJudgment), rank(rootNext))
	assert.Equal(t, -1, rank("unknown"))
}

func TestSprintIdleCoverTraceRoots(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		set  func(*Snapshot)
		want string
	}{
		{"empty", func(*Snapshot) {}, ""},
		{"dropped judgment age", func(s *Snapshot) {
			idleCoverWaiting(s, "a", "gone")
			idleCoverPut(s, idleCoverCard("gone", "s", "", map[string]string{"outcome": "dropped"}))
			s.Open = []Open{{Key: OpenKey("n", "a"), Note: Note{Kind: Judgment, Type: NBlocked, At: s.Now.Add(-time.Minute)}}}
		}, "1 behind 1 drop-blocked judgments (oldest 1m0s)"},
		{"missing need", func(s *Snapshot) { idleCoverWaiting(s, "a", "missing") }, "1 behind 1 cards with missing needs"},
		{"cycle counts missing", func(s *Snapshot) { idleCoverWaiting(s, "a", "b"); idleCoverWaiting(s, "b", "a") }, "2 behind 1 cards with missing needs"},
		{"held waiting", func(s *Snapshot) { idleCoverWaiting(s, "a").Fields[FieldHeld] = "x" }, "1 behind a (held)"},
		{"held sentinel", func(s *Snapshot) {
			idleCoverPut(s, idleCoverCard("stop", "s", Waiting, map[string]string{"kind": "sentinel", FieldHeld: "x"}))
			idleCoverWaiting(s, "a", "stop")
		}, "1 behind sentinel stop (held)"},
		{"reached sentinel", func(s *Snapshot) {
			idleCoverPut(s, idleCoverCard("stop", "s", Waiting, map[string]string{"kind": "sentinel", "reached": stamp(s.Now)}))
			idleCoverWaiting(s, "a", "stop")
		}, "1 behind sentinel stop (reached, not released)"},
		{"unreached sentinel", func(s *Snapshot) {
			idleCoverPut(s, idleCoverCard("stop", "s", Waiting, map[string]string{"kind": "sentinel"}))
			idleCoverWaiting(s, "a", "stop")
		}, "1 behind sentinel stop (waiting for the cards before it)"},
		{"judgment in flight", func(s *Snapshot) {
			idleCoverPut(s, idleCoverCard("work", "s", Working, nil))
			idleCoverWaiting(s, "a", "work")
			s.Open = []Open{{Key: OpenKey("n", "work"), Note: Note{Kind: Judgment, Type: "red", At: s.Now.Add(-time.Minute)}}}
		}, "1 behind work (red, 1m0s)"},
		{"working", func(s *Snapshot) {
			idleCoverPut(s, idleCoverCard("work", "s", Working, nil))
			idleCoverWaiting(s, "a", "work")
		}, "1 behind in flight"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := idleCoverSnapshot()
			tc.set(s)
			assert.Equal(t, tc.want, TraceIdle(s, TickReq{}))
		})
	}
}

func TestSprintIdleCoverTraceRoutesStreamsAndOrder(t *testing.T) {
	t.Parallel()
	t.Run("stopped stream", func(t *testing.T) {
		t.Parallel()
		s := idleCoverSnapshot()
		idleCoverPut(s, idleCoverCard("merge", "s", Merging, nil))
		idleCoverWaiting(s, "a", "merge")
		s.Merge.Put(idleCoverCard(CtlID("s"), "s", Merging, map[string]string{"state": StreamStopped, "cause": "conflict"}))
		assert.Equal(t, "1 behind stream s stopped (conflict)", TraceIdle(s, TickReq{}))
	})
	t.Run("no route", func(t *testing.T) {
		t.Parallel()
		s := idleCoverSnapshot()
		s.Routes = []Route{{Name: "flash", Tier: "flash", Enabled: true}}
		idleCoverPut(s, idleCoverCard("pro", "s", Ready, map[string]string{FieldTier: "pro"}))
		idleCoverWaiting(s, "a", "pro")
		assert.Equal(t, "1 behind tier pro: no route serves it", TraceIdle(s, TickReq{}))
	})
	t.Run("groups and cap", func(t *testing.T) {
		t.Parallel()
		s := idleCoverSnapshot()
		for i := 0; i < IdleRoots+1; i++ {
			stop := fmt.Sprintf("stop%d", i)
			idleCoverPut(s, idleCoverCard(stop, "s", Waiting, map[string]string{"kind": "sentinel", FieldHeld: "x"}))
			idleCoverWaiting(s, fmt.Sprintf("a%d", i), stop)
		}
		got := TraceIdle(s, TickReq{})
		assert.Contains(t, got, "and 1 roots more")
		assert.True(t, strings.HasPrefix(got, "1 behind sentinel stop"))
	})
}

func TestSprintIdleCoverTickEpisodes(t *testing.T) {
	t.Parallel()
	newIdle := func() *Snapshot {
		s := idleCoverSnapshot()
		s.Fleet.SetRows([]string{"m"})
		s.Fleet.Put(idleCoverCard(CtlID("m"), "m", Up, map[string]string{"status": Up, "width": "2"}))
		idleCoverWaiting(s, "a")
		return s
	}
	t.Run("disabled and nil", func(t *testing.T) {
		t.Parallel()
		s := newIdle()
		p, _ := TickIdle(s, TickReq{})
		assert.Empty(t, p)
		s.Fleet = nil
		p, _ = TickIdle(s, TickReq{IdleAlarm: true})
		assert.Empty(t, p)
	})
	t.Run("starts then waits then speaks", func(t *testing.T) {
		t.Parallel()
		s := newIdle()
		p, _ := TickIdle(s, TickReq{IdleAlarm: true})
		require.Len(t, p.Props, 1)
		assert.Equal(t, PropIdleSince, p.Props[0].Name)
		assert.Empty(t, p.Units[0].Notes)
		s.Fleet.SetProp(PropIdleSince, stamp(s.Now.Add(-IdleWindow)))
		p, _ = TickIdle(s, TickReq{IdleAlarm: true, Stopped: func(_, _ time.Time) time.Duration { return time.Second }})
		assert.Empty(t, p.Units, "stopped time keeps the episode inside its running window")
		p, _ = TickIdle(s, TickReq{IdleAlarm: true})
		require.Len(t, p.Units, 1)
		require.Len(t, p.Units[0].Notes, 1)
		assert.Equal(t, NIdle, p.Units[0].Notes[0].Type)
		assert.Equal(t, "coordinator", p.Units[0].Notes[0].To)
		assert.Contains(t, p.Units[0].Notes[0].What, "fleet 0/2:")
		assert.Equal(t, PropIdleSaid, p.Props[0].Name)
	})
	t.Run("recovery after said and unsaid", func(t *testing.T) {
		t.Parallel()
		for _, said := range []bool{false, true} {
			said := said
			t.Run(fmt.Sprintf("said=%v", said), func(t *testing.T) {
				t.Parallel()
				s := newIdle()
				s.Fleet.SetProp(PropIdleSince, stamp(s.Now.Add(-IdleWindow)))
				if said {
					s.Fleet.SetProp(PropIdleSaid, stamp(s.Now.Add(-time.Minute)))
				}
				s.Fleet.Put(idleCoverCard("m-work", "m", Working, nil))
				p, _ := TickIdle(s, TickReq{IdleAlarm: true})
				require.Len(t, p.Props, 2)
				require.Len(t, p.Units, 1)
				if said {
					require.Len(t, p.Units[0].Notes, 1)
					assert.Equal(t, NIdleCleared, p.Units[0].Notes[0].Type)
					assert.Contains(t, p.Units[0].Notes[0].What, "after 5m0s")
				} else {
					assert.Empty(t, p.Units[0].Notes)
				}
			})
		}
	})
	t.Run("fleet off", func(t *testing.T) {
		t.Parallel()
		s := newIdle()
		s.Work.SetProp(PropFleet, SwitchOff)
		p, _ := TickIdle(s, TickReq{IdleAlarm: true})
		assert.Empty(t, p)
	})
}
