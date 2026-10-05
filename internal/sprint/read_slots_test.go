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

// An asked read of a friend's reader past the bound raises one readers-behind
// judgment on that reader, naming her and her oldest waiting read, decided by
// a wait the inbox prints as a command. A second late read does not add a
// second judgment, the sprint's own readers-behind line leaves her out (a
// machine reader late beside her keeps it), the next tick updates the one
// judgment in place, and it closes when the read is begun.
func TestAnAskedReadPastTheBoundRaisesOneJudgment(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-amy", "reader-m1")
	w.s.Fleet.SetProp(PropFriendReadSlots, `{"amy":2}`)
	w.s.ReaderStates = map[string]string{"reader-amy": ReaderUp, "reader-m1": ReaderUp}
	older := ReadCardID("s1-1", 1, "reader-amy")
	newer := ReadCardID("s1-2", 1, "reader-amy")
	putAsked := func(id, row string, age time.Duration) {
		w.s.Readers.Put(&Card{ID: id, Row: row, Col: Asked, Rev: 1, Fields: map[string]string{
			"kind": "read", "primary": "s1-1", "stream": "s1", "attempt": "1", "asked": stamp(w.s.Now.Add(-age)),
		}})
	}
	putAsked(older, "reader-amy", 11*time.Minute)
	putAsked(newer, "reader-amy", 10*time.Minute+30*time.Second)
	putAsked(ReadCardID("s1-3", 1, "reader-m1"), "reader-m1", 11*time.Minute)

	judgments := func() (friend []Note, sprint []Note) {
		for _, n := range w.notes {
			if n.Type != NReadersBehind {
				continue
			}
			if n.StreamLevel {
				sprint = append(sprint, n)
			} else {
				friend = append(friend, n)
			}
		}
		return friend, sprint
	}
	w.part(TickAsk, TickReq{})
	friend, line := judgments()
	require.Len(t, friend, 1, "one judgment on the friend reader, not one per read: %+v", friend)
	require.Equal(t, []string{"reader-amy"}, friend[0].Primaries)
	require.Contains(t, friend[0].What, "reader-amy has 2 read(s) asked and not begun past 10m0s, the oldest "+older)
	require.Contains(t, friend[0].What, "inbox/reads/"+older+"/")
	require.Equal(t, []string{"wait 10m"}, friend[0].Decisions)
	require.True(t, TickKept(friend[0].Type), "the tick keeps it, so wait answers it")
	require.Len(t, line, 1, "the machine reader keeps the sprint's line")
	require.Contains(t, line[0].What, "reader-m1")
	require.NotContains(t, line[0].What, "reader-amy", "the sprint's line leaves the friend's reader out")

	w.part(TickAsk, TickReq{})
	friend, _ = judgments()
	require.Len(t, friend, 1, "the next tick keeps the one judgment")

	// begun: both reads leave asked, and the judgment closes
	for _, id := range []string{older, newer} {
		c := w.s.Readers.Placed(id)
		require.NotNil(t, c)
		w.s.Readers.Put(&Card{ID: id, Row: "reader-amy", Col: Reading, Rev: c.Rev + 1, Fields: c.Fields})
	}
	require.Empty(t, readWaitsConds(w.s), "nothing waits once the reads are begun")

	// her own shorter read wait fires sooner; under the default window it does not
	early := newWorld(t, "reader-amy")
	early.s.Fleet.SetProp(PropFriendReadSlots, `{"amy":2}`)
	early.s.Fleet.SetProp(PropFriendReadWait, `{"amy":60}`)
	early.s.ReaderStates = map[string]string{"reader-amy": ReaderUp}
	early.s.Readers.Put(&Card{ID: older, Row: "reader-amy", Col: Asked, Rev: 1, Fields: map[string]string{
		"asked": stamp(early.s.Now.Add(-2 * time.Minute)),
	}})
	conds := readWaitsConds(early.s)
	require.Len(t, conds, 1)
	require.Contains(t, conds[0].what, older)
	require.Contains(t, conds[0].what, "past 1m0s")

	for _, v := range []string{"", "nope", `{"amy":0}`, `{"amy":-60}`, `{"bo":60}`} {
		early.s.Fleet.SetProp(PropFriendReadWait, v)
		require.Empty(t, readWaitsConds(early.s), "%q is the default 10m: two minutes is inside it", v)
	}

	// a friend reader not up holds nothing late: the ask takes her reads back
	early.s.Fleet.SetProp(PropFriendReadWait, `{"amy":60}`)
	early.s.ReaderStates = map[string]string{"reader-amy": ReaderAway}
	require.Empty(t, readWaitsConds(early.s))
}
