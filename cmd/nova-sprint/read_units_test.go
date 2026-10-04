package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestAnyUnitWithRoomAtOrAboveTheReadTierServesARead drives a friend read and
// a fleet-reader read through TickAsk, the one ask. Amy is flash and takes the
// flash card. Bea is frontier and takes one read of the pro card; a fleet
// reader takes the other. The next flash card finds no friend with room and
// goes to a fleet reader. The frontier card is not handed to amy or to a fleet
// reader. The sprint clock is t0. No sleep, no wall clock, no network.
func TestAnyUnitWithRoomAtOrAboveTheReadTierServesARead(t *testing.T) {
	t.Parallel()
	s := &sprint.Snapshot{
		Now:     t0,
		Work:    sprint.NewTable(sprint.Work),
		Readers: sprint.NewTable(sprint.Readers),
		Merge:   sprint.NewTable(sprint.Merge),
		Fleet:   sprint.NewTable(sprint.Fleet),
		ReaderStates: map[string]string{
			"reader-a": sprint.ReaderUp,
			"reader-b": sprint.ReaderUp,
		},
	}
	s.Work.SetRows([]string{"s1"})
	s.Readers.SetRows([]string{"reader-a", "reader-b"})
	put := func(id, tier string, score float64) {
		s.Work.Put(&sprint.Card{ID: id, Row: "s1", Col: sprint.Review, Score: score, Fields: map[string]string{
			"brief":   id + ": work (s1) tier: " + tier + "\n\nAS A READ\nLook at main.go.\n",
			"attempt": "1", "head": "abc123",
		}})
	}
	put("s1-1", "flash", 1)
	put("s1-2", "pro", 2)
	put("s1-3", "flash", 3)
	put("s1-4", "frontier", 4)
	seats := []sprint.FriendSeat{
		{Name: "amy", Width: 1, Status: sprint.Up, Tiers: []string{"flash"}},
		{Name: "bea", Width: 1, Status: sprint.Up, Tiers: []string{"frontier"}},
	}
	p, _ := sprint.TickAsk(s, sprint.TickReq{Friends: seats})
	require.Empty(t, p.Refused, "refused: %v", p.Refused)

	got := map[string]madeRead{}
	for _, u := range p.Units {
		for _, ch := range u.Changes {
			if ch.Entry.Create == nil {
				continue
			}
			got[ch.Entry.ID] = madeRead{row: ch.Entry.Create.Row, col: ch.Entry.Create.Col, set: ch.Entry.Set}
		}
	}
	due := t0.Add(30 * time.Minute).UTC().Format(time.RFC3339)
	amyID := sprint.ReadCardID("s1-1", 1, "friend-amy")
	amy, ok := got[amyID]
	require.True(t, ok, "amy serves s1-1, got %v", got)
	assert.Equal(t, "friend-amy", amy.row)
	assert.Equal(t, sprint.Asked, amy.col)
	assert.Equal(t, "friend.amy", amy.set["who"])
	assert.Equal(t, "friend-amy", amy.set["reader"])
	assert.Equal(t, due, amy.set["due"])
	assert.Equal(t, "1800", amy.set["deadline"])
	assert.Equal(t, "flash", amy.set["tier"])
	assert.NotContains(t, amy.set, "usd")
	assert.NotContains(t, amy.set, "route")

	beaID := sprint.ReadCardID("s1-2", 1, "friend-bea")
	bea, ok := got[beaID]
	require.True(t, ok, "bea serves one read of the pro card, got %v", got)
	assert.Equal(t, "friend-bea", bea.row)
	assert.Equal(t, "friend.bea", bea.set["who"])
	assert.Equal(t, due, bea.set["due"])
	assert.Equal(t, "1800", bea.set["deadline"])
	assert.Equal(t, "pro", bea.set["tier"], "the card's class, not readTierOf and not bea's class")
	assert.NotContains(t, bea.set, "usd")
	_, amyPro := got[sprint.ReadCardID("s1-2", 1, "friend-amy")]
	assert.False(t, amyPro, "a flash friend does not serve a pro read")

	fleetPro := fleetRead(got, "s1-2")
	require.NotEmpty(t, fleetPro, "a fleet reader serves the pro card's other read: %v", got)
	assert.NotContains(t, got[fleetPro].set, "who")

	_, amyFlash2 := got[sprint.ReadCardID("s1-3", 1, "friend-amy")]
	_, beaFlash2 := got[sprint.ReadCardID("s1-3", 1, "friend-bea")]
	assert.False(t, amyFlash2, "amy has no room left")
	assert.False(t, beaFlash2, "bea has no room left")
	fleetFlash := fleetRead(got, "s1-3")
	require.NotEmpty(t, fleetFlash, "a fleet reader serves the flash card no friend has room for: %v", got)
	assert.NotEqual(t, fleetPro, fleetFlash, "the two fleet reads are two readers")

	for id := range got {
		assert.NotContains(t, id, "s1-4", "a frontier read is not dealt: %s", id)
	}
	var few bool
	for _, n := range p.Notes {
		if n.Type == sprint.NFewReaders {
			few = true
		}
	}
	assert.True(t, few, "the frontier card waits: notes %v", p.Notes)

	var rows []string
	for _, ra := range p.Rows {
		if ra.Table == sprint.Readers {
			rows = append(rows, ra.Row)
		}
	}
	assert.ElementsMatch(t, []string{"friend-amy", "friend-bea"}, rows)

	brief := sprint.FriendReadBrief("amy", "s1-1", "s1-1: work (s1) tier: flash\n\nAS A READ\nLook at main.go.\n\nNEXT\n", "sprint/s1-1", "startsha", "abc123", 1, t0.Add(30*time.Minute))
	assert.Contains(t, brief, "WHO: friend amy\n")
	assert.Contains(t, brief, "deadline: "+due+"\n")
	assert.Contains(t, brief, "AS A READ\nLook at main.go.\n")
	assert.NotContains(t, brief, "NEXT")

	held := friendReadSnapshot(t)
	id := sprint.ReadCardID("s1-1", 1, "friend-amy")
	land := sprint.CloseFriendRead(held, "friend-amy", id, "Verdict: LAND\n", "amy")
	want := sprint.Read(held, sprint.ReadReq{Sel: sprint.Sel{IDs: []string{id}}, As: "friend-amy", Verdict: "ok", Who: "amy"})
	assert.Equal(t, want, land, "LAND closes through Read")
	require.NotEmpty(t, land.Units)
	assert.Equal(t, sprint.OK, land.Units[0].Changes[0].Entry.Move.Col)

	broken := sprint.CloseFriendRead(held, "friend-amy", id, "Verdict: HOLD\nmain.go is wrong\n", "amy")
	wantBroken := sprint.Read(held, sprint.ReadReq{Sel: sprint.Sel{IDs: []string{id}}, As: "friend-amy", Verdict: "broken", Finding: "main.go is wrong", Who: "amy"})
	if !reflect.DeepEqual(wantBroken, broken) {
		t.Fatalf("HOLD closes through Read\n got %+v\nwant %+v", broken, wantBroken)
	}
	assert.Equal(t, sprint.Broken, broken.Units[0].Changes[0].Entry.Move.Col)
}

type madeRead struct {
	row, col string
	set      map[string]string
}

func fleetRead(got map[string]madeRead, primary string) string {
	for id, m := range got {
		if m.set["primary"] == primary && !sprint.IsFriendReader(m.row) {
			return id
		}
	}
	return ""
}

func friendReadSnapshot(t *testing.T) *sprint.Snapshot {
	t.Helper()
	s := &sprint.Snapshot{Now: t0, Work: sprint.NewTable(sprint.Work), Readers: sprint.NewTable(sprint.Readers)}
	s.Work.SetRows([]string{"s1"})
	s.Readers.SetRows([]string{"friend-amy"})
	s.Work.Put(&sprint.Card{ID: "s1-1", Row: "s1", Col: sprint.Review, Score: 1, Fields: map[string]string{
		"brief": "s1-1: work (s1) tier: flash\n", "attempt": "1", "head": "abc123",
	}})
	id := sprint.ReadCardID("s1-1", 1, "friend-amy")
	s.Readers.Put(&sprint.Card{ID: id, Row: "friend-amy", Col: sprint.Asked, Score: 1, Rev: 1, Fields: map[string]string{
		"kind": "read", "primary": "s1-1", "stream": "s1", "reader": "friend-amy", "attempt": "1", "head": "abc123",
	}})
	return s
}

// The friends table counts a friend's reads in the columns it already has.
func TestFriendReadsCountInTheFriendsTable(t *testing.T) {
	t.Parallel()
	readers := ntable.Table{Columns: []ntable.Column{{Name: sprint.Asked}, {Name: sprint.Reading}, {Name: sprint.OK}, {Name: sprint.Broken}},
		Rows: []ntable.Row{{Key: "friend-amy", Cells: []ntable.Cell{{Count: 1}, {Count: 0}, {Count: 2}, {Count: 3}}}}}
	got := friendReadCounts(readers)
	assert.Equal(t, 1, got["amy"].Working)
	assert.Equal(t, 2, got["amy"].OK)
	assert.Equal(t, 3, got["amy"].Failed)
	assert.Empty(t, friendReadCounts(ntable.Table{Rows: []ntable.Row{{Key: "reader-a"}}}))
}
