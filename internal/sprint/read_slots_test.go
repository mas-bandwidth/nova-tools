package sprint

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A friend's reader room is her read slots, not her card width and not a
// machine row of the same name. A name the map lacks keeps the machine width,
// and a reader with no row stays unbounded.
func TestAFriendsReadSlotsAreHerReaderRoomWhateverHerWidth(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-amy", "reader-m1")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "amy", Width: 8}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 4}))
	w.s.Fleet.SetProp(PropFriendReadSlots, `{"amy":2}`)
	require.Equal(t, 2, w.s.ReaderWidth("reader-amy"), "her room is her read slots, not the machine width 8")
	require.Equal(t, 4, w.s.ReaderWidth("reader-m1"), "a machine the map does not name keeps its row")
	require.Equal(t, math.MaxInt, w.s.ReaderWidth("reader-x"), "no row and no map entry stays unbounded")

	w.s.Fleet.SetProp(PropFriendReadSlots, `{"amy":0}`)
	require.Equal(t, 0, w.s.ReaderWidth("reader-amy"), "0 asks her none")
	require.NotPanics(t, func() { _ = w.s.readerRooms([]string{"reader-amy"})["reader-amy"].share() })

	w.s.Fleet.SetProp(PropFriendReadSlots, `nope`)
	require.Equal(t, 8, w.s.ReaderWidth("reader-amy"), "a property that is not the map falls through to the machine row")
}

// An asked read past the bound raises one judgment for that friend reader,
// naming her and the oldest read. A second late read does not add a second
// judgment, a reader the map does not name gets none, and the next tick
// updates the one judgment instead of writing another.
func TestAnAskedReadPastTheBoundRaisesOneJudgment(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-amy", "reader-m1")
	w.s.Fleet.SetProp(PropFriendReadSlots, `{"amy":2}`)
	w.s.ReaderStates = map[string]string{"reader-amy": ReaderUp, "reader-m1": ReaderUp}
	older := ReadCardID("s1-1", 1, "reader-amy")
	newer := ReadCardID("s1-2", 1, "reader-amy")
	putAsked := func(id string, age time.Duration) {
		w.s.Readers.Put(&Card{ID: id, Row: "reader-amy", Col: Asked, Rev: 1, Fields: map[string]string{
			"kind": "read", "primary": "s1-1", "stream": "s1", "attempt": "1", "asked": stamp(w.s.Now.Add(-age)),
		}})
	}
	putAsked(older, 11*time.Minute)
	putAsked(newer, 10*time.Minute+30*time.Second)
	w.s.Readers.Put(&Card{ID: ReadCardID("s1-3", 1, "reader-m1"), Row: "reader-m1", Col: Asked, Rev: 1, Fields: map[string]string{
		"kind": "read", "primary": "s1-3", "stream": "s1", "attempt": "1", "asked": stamp(w.s.Now.Add(-11 * time.Minute)),
	}})

	w.part(TickAsk, TickReq{})
	var got []Note
	for _, n := range w.notes {
		if n.Type == NReadWaits {
			got = append(got, n)
		}
	}
	require.Len(t, got, 1, "one judgment for the friend reader, not one per read and not one for reader-m1")
	require.Equal(t, []string{"reader-amy"}, got[0].Primaries)
	require.Contains(t, got[0].What, older)
	require.NotContains(t, got[0].What, "reader-m1")
	require.Equal(t, []string{"wait"}, got[0].Decisions)

	w.part(TickAsk, TickReq{})
	n := 0
	for _, note := range w.notes {
		if note.Type == NReadWaits {
			n++
		}
	}
	require.Equal(t, 1, n, "the next tick keeps the one judgment")

	// a shorter bound fires sooner; under the default window it does not
	early := newWorld(t, "reader-amy")
	early.s.Fleet.SetProp(PropFriendReadSlots, `{"amy":2}`)
	early.s.Work.SetProp(PropReadWait, "1m")
	early.s.ReaderStates = map[string]string{"reader-amy": ReaderUp}
	early.s.Readers.Put(&Card{ID: older, Row: "reader-amy", Col: Asked, Rev: 1, Fields: map[string]string{
		"asked": stamp(early.s.Now.Add(-2 * time.Minute)),
	}})
	conds := readWaitsConds(early.s)
	require.Len(t, conds, 1)
	require.Contains(t, conds[0].what, older)

	early.s.Work.SetProp(PropReadWait, "")
	early.s.Readers.Put(&Card{ID: older, Row: "reader-amy", Col: Asked, Rev: 1, Fields: map[string]string{
		"asked": stamp(early.s.Now.Add(-2 * time.Minute)),
	}})
	require.Empty(t, readWaitsConds(early.s), "two minutes is inside the default 10")
}
