package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The one ask, applied, still checks: a friend read and a fleet read are
// readers-table cards, and a frontier card is not handed to a fleet reader.
func TestAskDealsAFriendAndAFleetReaderOnOnePath(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a", "reader-b")
	w.s.Work.SetRows([]string{"s1"})
	w.s.ReaderStates = map[string]string{"reader-a": ReaderUp, "reader-b": ReaderUp}
	put := func(id, tier string, score float64) {
		w.s.Work.Put(&Card{ID: id, Row: "s1", Col: Review, Score: score, Fields: map[string]string{
			"brief": id + ": work (s1) tier: " + tier + "\n", "attempt": "1", "head": "abc123",
		}})
	}
	put("s1-1", "flash", 1)
	put("s1-2", "pro", 2)
	put("s1-4", "frontier", 4)
	p, _ := TickAsk(w.s, TickReq{Friends: []FriendSeat{
		{Name: "amy", Width: 1, Status: Up, Tiers: []string{"flash"}},
		{Name: "bea", Width: 1, Status: Up, Tiers: []string{"frontier"}},
	}})
	w.must(p)
	require.NotNil(t, w.s.Readers.Placed(ReadCardID("s1-1", 1, "friend-amy")))
	require.NotNil(t, w.s.Readers.Placed(ReadCardID("s1-2", 1, "friend-bea")))
	assert.Nil(t, w.s.Readers.Card(ReadCardID("s1-4", 1, "friend-amy")))
	assert.Nil(t, w.s.Readers.Card(ReadCardID("s1-4", 1, "reader-a")))
	assert.Nil(t, w.s.Readers.Card(ReadCardID("s1-4", 1, "reader-b")))
	var fleet int
	for _, rd := range []string{"reader-a", "reader-b"} {
		if w.s.Readers.Placed(ReadCardID("s1-2", 1, rd)) != nil {
			fleet++
		}
	}
	assert.Equal(t, 1, fleet, "one fleet reader serves the pro card with bea")
	w.clean("one read path")
}
