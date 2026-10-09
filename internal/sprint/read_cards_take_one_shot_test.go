package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/stretchr/testify/require"
)

// A one-shot friend counts each read or work card as one configured physical lane even when read cards are on
// (docs/SPEC-SPRINT.md section 1, "A friend's card"). Loading the setting for
// manual Take must not turn those lanes into half-slots.
func TestOneShotTakeCountsReadAndWorkAsFullConfiguredLanes(t *testing.T) {
	t.Parallel()
	w := readCardsWorld(t, 4, "m1")
	row := FriendRow("amy")
	w.s.Fleet.SetRows(append(w.s.Fleet.Rows(), row))
	w.s.Friends = []FriendSeat{{Name: "amy", Width: 2, Status: Up, Mode: config.FriendModeOneShot}}
	w.s.Fleet.Put(&Card{ID: ReadCardID("s1-1", 1, "amy"), Row: row, Col: Working, Fields: map[string]string{
		"kind": "read", FieldReadCard: "1", "primary": "s1-1", "attempt": "1", "gen": "1", "stream": "s1",
	}})
	queuedRead := ReadCardID("s1-2", 1, "amy")
	queuedWork := WorkCardID("s1-3", 1)
	w.s.Fleet.Put(&Card{ID: queuedRead, Row: row, Col: Ready, Fields: map[string]string{
		"kind": "read", FieldReadCard: "1", "primary": "s1-2", "attempt": "1", "gen": "1", "stream": "s1",
	}})
	w.s.Fleet.Put(&Card{ID: queuedWork, Row: row, Col: Ready, Fields: map[string]string{
		"kind": "work", "primary": "s1-3", "attempt": "1", "gen": "1", "stream": "s1",
	}})

	p := Take(w.s, TakeReq{As: row, Sel: Sel{IDs: []string{queuedRead, queuedWork}}, Gens: map[string]int{queuedRead: 1, queuedWork: 1}})
	require.Len(t, p.Units, 1, "one active read leaves one of the two configured lanes free")
	require.Len(t, p.Refused, 1, "the second queued card cannot exceed configured width")

	// At full width, neither a further read nor work card can be taken.
	w.s.Fleet.Put(&Card{ID: WorkCardID("s1-4", 1), Row: row, Col: Working, Fields: map[string]string{
		"kind": "work", "primary": "s1-4", "attempt": "1", "gen": "1", "stream": "s1",
	}})
	p = Take(w.s, TakeReq{As: row, Sel: Sel{IDs: []string{queuedRead, queuedWork}}, Gens: map[string]int{queuedRead: 1, queuedWork: 1}})
	require.Empty(t, p.Units, "two active cards fill the one-shot friend's configured width")
	require.Len(t, p.Refused, 2)
}
