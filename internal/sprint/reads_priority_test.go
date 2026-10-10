package sprint

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Reads are a card priority (docs/SPEC-SPRINT.md sections 1 and 5; the owner, 2026-10-06,
// 4:49 PM ET, with 270 cards in review, 76 working and every reader near idle: "I am now
// convinced that reads need to become a type of card priority."). Every test is on the
// in-memory world with its fixed clock, no store and no socket.

// priorityWorld is a world with no paid reader, no machine up and the friend amy up at width 2
// (room DealAhead*2 = 4), tiers flash and pro, holding one card working on her row (free
// room 3); then working primaries on the work table, flash reads she may take and frontier
// reads she may not (above her tiers) in review, and work cards ready, each a hard pin to her
// (WHO: only friend amy, so no machine is dealt one).
func priorityWorld(t *testing.T, working, flashReads, frontierReads, ready int) (*world, FriendSeat) {
	t.Helper()
	w := newWorld(t)
	w.s.Work.SetRows([]string{"s1", "s2", "s3"})
	amy := FriendSeat{Name: "amy", Width: 2, Status: Up, Tiers: []string{cardhdr.RouteFlash, cardhdr.RoutePro}}
	if working > 0 {
		amy.Running = []string{WorkCardID("s3-1", 1)} // her beat names her one card running
	}
	row := FriendRow("amy")
	w.s.Fleet.SetRows([]string{row})
	for i := range working {
		id := "s3-" + itoa(i+1)
		w.s.Work.Put(&Card{ID: id, Row: "s3", Col: Working, Score: float64(i + 1), Rev: 1, Fields: map[string]string{
			"kind": "primary", "attempt": "1", "stream": "s3", "brief": id + ": work (s3)", "work": WorkCardID(id, 1)}})
		if i == 0 {
			// her one card working: her free room is 4 - 1 = 3
			w.s.Fleet.Put(&Card{ID: WorkCardID(id, 1), Row: row, Col: Working, Score: 1, Rev: 1, Fields: map[string]string{
				"kind": "work", "primary": id, "stream": "s3", "attempt": "1", "gen": "1", "member": row}})
		}
	}
	n := 0
	put := func(brief string) {
		n++
		id := "s1-" + itoa(n)
		w.s.Work.Put(&Card{ID: id, Row: "s1", Col: Review, Score: float64(n), Rev: 1, Fields: map[string]string{
			"kind": "primary", "attempt": "1", "stream": "s1", "brief": id + ": " + brief, "head": "h-" + id}})
	}
	for range flashReads {
		put("a flash card (s1) tier: flash")
	}
	for range frontierReads {
		put("a frontier card (s1) tier: frontier")
	}
	var cards []CardAdd
	for i := range ready {
		cards = append(cards, CardAdd{ID: "s2-" + itoa(i+1), Brief: friendBrief("only friend amy")})
	}
	if ready > 0 {
		w.must(Add(w.s, AddReq{Stream: "s2", Cards: cards}))
	}
	return w, amy
}

// tickDealAndAsk is the tick's work update's deal and then its readers' ask, as one tick
// runs them in order (TickTables), each applied.
func tickDealAndAsk(t *testing.T, w *world, seats ...FriendSeat) {
	t.Helper()
	p, _ := TickDeal(w.s, TickReq{Friends: seats})
	w.must(p)
	askReaders(t, w, seats)
}

// friendReads and friendWork are the read cards and the work cards placed on her row.
func friendReads(w *world, name string) []string {
	var out []string
	for _, c := range w.s.Fleet.Column(Ready, Working) {
		if c.Row == FriendRow(name) && c.F("kind") == "read" {
			out = append(out, c.F("primary"))
		}
	}
	return out
}

func friendNewWork(w *world, name string) []string {
	var out []string
	for _, c := range w.s.Fleet.Column(Ready, Working) {
		if c.Row == FriendRow(name) && c.F("kind") == "work" && c.F("stream") == "s2" {
			out = append(out, c.F("primary"))
		}
	}
	return out
}

// A read is at reader priority, above normal work, in every deal: with review 20 above
// working 5 (the backup edge) and with review 1 below working 5 alike, the tick places the
// reads she may take before any work card, and deals work only in the room they leave. The
// deal's own plan holds the reads, ahead of its work.
func TestReadsOutrankNormalWork(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		reads, backed int
		backup        string
		work          int
	}{
		{"review 20 above working 5", 3, 17, BackupReads, 0},
		{"review 1 below working 5", 1, 0, BackupNone, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w, amy := priorityWorld(t, 5, tc.reads, tc.backed, 10)
			require.Equal(t, tc.backup, Backup(w.s))
			r, _ := friendRoom(amy)
			require.Equal(t, 3, r-friendLoad(w.s, "amy"), "her room is three")

			p, _ := TickDeal(w.s, TickReq{Friends: []FriendSeat{amy}})
			firstRead, firstWork := -1, -1
			for i, u := range p.Units {
				if u.Moved == u.Key+" asked of friend amy" && firstRead < 0 {
					firstRead = i
				}
				if strings.Contains(u.Moved, " -> working card=") && firstWork < 0 {
					firstWork = i
				}
			}
			require.GreaterOrEqual(t, firstRead, 0, "the deal's plan places the reads")
			if firstWork >= 0 {
				require.Less(t, firstRead, firstWork, "the deal's plan places a read before any normal work card")
			}
			w.must(p)
			askReaders(t, w, []FriendSeat{amy})
			assert.Len(t, friendReads(w, "amy"), tc.reads, "every read she may take is placed")
			assert.Len(t, friendNewWork(w, "amy"), tc.work, "work only in the room the reads leave")
			assert.Equal(t, 4, friendLoad(w.s, "amy"), "her room is full")
		})
	}
}

// The ladder above reader: a high card is dealt before the reads, the reads before normal work,
// and the reads of a higher primary are asked first.
func TestTheLadderOrdersTheDealAndTheAsk(t *testing.T) {
	t.Parallel()

	t.Run("high before the reads, the reads before normal", func(t *testing.T) {
		t.Parallel()
		w, amy := priorityWorld(t, 5, 3, 0, 4)
		w.s.Work.Card("s2-4").Fields[FieldPriority] = PriorityHigh
		tickDealAndAsk(t, w, amy)
		assert.Equal(t, []string{"s2-4"}, friendNewWork(w, "amy"), "the high card first, and no normal card")
		assert.Len(t, friendReads(w, "amy"), 2, "the reads in the room it leaves")
	})

	t.Run("a blocker's read is asked first", func(t *testing.T) {
		t.Parallel()
		w, amy := priorityWorld(t, 5, 5, 0, 0)
		w.s.Work.Card("s1-5").Fields[FieldPriority] = PriorityBlocker
		w.s.Work.Card("s1-4").Fields[FieldPriority] = PriorityLow
		tickDealAndAsk(t, w, amy)
		assert.ElementsMatch(t, []string{"s1-5", "s1-1", "s1-2"}, friendReads(w, "amy"), "the blocker's read, then work order; the low card's last")
	})
}

// A friend's room goes to the reads she may take first: a work card is dealt to her only when
// no read she may take waits, and a read she may not take holds nothing.
func TestAFriendsRoomGoesToReadsFirst(t *testing.T) {
	t.Parallel()

	t.Run("reads fill her room before work", func(t *testing.T) {
		t.Parallel()
		w, amy := priorityWorld(t, 1, 3, 0, 10)
		tickDealAndAsk(t, w, amy)
		assert.ElementsMatch(t, []string{"s1-1", "s1-2", "s1-3"}, friendReads(w, "amy"))
		assert.Empty(t, friendNewWork(w, "amy"), "no work while a read she may take waits")
	})

	t.Run("a read she may take that finds no room holds her work", func(t *testing.T) {
		t.Parallel()
		w, amy := priorityWorld(t, 1, 5, 0, 10)
		seats := friendReadsFirst(w.s, []FriendSeat{amy}, readsWaitingCards(w.s), Plan{})
		r, _ := friendRoom(seats[0])
		assert.LessOrEqual(t, r-friendLoad(w.s, "amy"), 0, "a waiting read she may take leaves no room for work")
		tickDealAndAsk(t, w, amy)
		assert.Len(t, friendReads(w, "amy"), 3, "three of the five fill her room")
		assert.Empty(t, friendNewWork(w, "amy"))
	})

	t.Run("a read she may not take holds no work", func(t *testing.T) {
		t.Parallel()
		w, amy := priorityWorld(t, 5, 0, 1, 10)
		tickDealAndAsk(t, w, amy)
		assert.Empty(t, friendReads(w, "amy"), "a frontier read is above her tiers")
		assert.Len(t, friendNewWork(w, "amy"), 3, "her room of three is dealt")
	})
}

// A low card is dealt only to a lane no read and no normal card can fill: to a friend after
// her reads and her normal cards, and on the fleet after every normal card ready.
func TestLowFillsOnlyAnIdleLane(t *testing.T) {
	t.Parallel()
	low := func(id string) CardAdd {
		return CardAdd{ID: id, Brief: "c: a low card\nREPO: mas-bandwidth/nova-tools\nWHO: only friend amy\nPRIORITY: low\n\nThe task."}
	}

	t.Run("a friend", func(t *testing.T) {
		t.Parallel()
		w, amy := priorityWorld(t, 5, 1, 0, 0)
		// the low cards are admitted first, so work order alone would deal them first
		w.must(Add(w.s, AddReq{Stream: "s2", Cards: []CardAdd{low("s2-1"), low("s2-2")}}))
		w.must(Add(w.s, AddReq{Stream: "s2", Cards: []CardAdd{{ID: "s2-3", Brief: friendBrief("only friend amy")}}}))
		require.Equal(t, PriorityLow, w.s.Work.Card("s2-1").F(FieldPriority), "the brief's PRIORITY line seeds it")
		tickDealAndAsk(t, w, amy)
		assert.Equal(t, []string{"s1-1"}, friendReads(w, "amy"), "the read first")
		assert.ElementsMatch(t, []string{"s2-3", "s2-1"}, friendNewWork(w, "amy"), "then the normal card, then a low card in the lane left")
		assert.Equal(t, Ready, w.s.StateOf("s2-2"), "no lane is left for the second low card")

		// with no lane left for anything higher, the next lane is the low card's
		w2, amy2 := priorityWorld(t, 5, 0, 0, 0)
		w2.must(Add(w2.s, AddReq{Stream: "s2", Cards: []CardAdd{low("s2-1")}}))
		tickDealAndAsk(t, w2, amy2)
		assert.Equal(t, []string{"s2-1"}, friendNewWork(w2, "amy"), "an idle lane nothing higher can fill takes the low card")
	})

	t.Run("the fleet", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a")
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 1}))
		w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{
			{ID: "s1-1", Brief: "c: a low card\nPRIORITY: low\n\nThe task."},
			{ID: "s1-2", Brief: "c: a normal card\n\nThe task."},
			{ID: "s1-3", Brief: "c: a normal card\n\nThe task."},
		}}))
		w.part(TickDeal, TickReq{})
		// width 1 holds DealAhead (2) cards: both normal cards, the low card left ready
		assert.Equal(t, Working, w.s.StateOf("s1-2"))
		assert.Equal(t, Working, w.s.StateOf("s1-3"))
		assert.Equal(t, Ready, w.s.StateOf("s1-1"), "a low card waits while a normal card fills the lane")
	})
}

// A fleet reader reads only the tiers its row names, and a row naming none reads flash while
// the store holds routes ("I'm ok with flash readers on fleet but not pro"): a pro read is never
// asked of a flash row or of a fleet row that names no tier, and is asked of a row that names
// pro. A friend's reader (its own model) naming no tier reads every tier.
func TestFleetReadersReadOnlyTheirTiers(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T, tiers map[string]string, readers ...string) *world {
		w := newWorld(t, readers...)
		w.s.Routes = []Route{
			{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "f", Enabled: true},
			{Name: "pro-a", Tier: cardhdr.RoutePro, Provider: "p", Model: "p", Enabled: true},
		}
		w.s.Readers.Texts = map[string]map[string]string{}
		for rd, tv := range tiers {
			w.s.Readers.Texts[rd] = map[string]string{ReaderTiers: tv}
		}
		putReview(w, "s1-1", "s1-1: a pro card (s1) tier: pro\n", 1, 1, "h1")
		w.s.Work.Card("s1-1").Fields[FieldTierNow] = cardhdr.RoutePro
		return w
	}
	asked := func(w *world, reader string) bool {
		return w.s.Readers.Card(ReadCardID("s1-1", 1, reader)) != nil
	}

	t.Run("a flash row is never asked a pro read", func(t *testing.T) {
		t.Parallel()
		w := setup(t, map[string]string{"reader-batman": "flash", "reader-vision": "flash"}, "reader-batman", "reader-vision")
		require.Equal(t, cardhdr.RoutePro, w.s.readTierOf(w.s.Work.Card("s1-1")))
		askReaders(t, w, nil)
		assert.False(t, asked(w, "reader-batman"))
		assert.False(t, asked(w, "reader-vision"))
	})

	t.Run("a fleet row naming no tier reads flash only", func(t *testing.T) {
		t.Parallel()
		w := setup(t, nil, "reader-batman-2", "reader-vision-2")
		askReaders(t, w, nil)
		assert.False(t, asked(w, "reader-batman-2"), "the fleet does not read pro unless its row says so")
		assert.False(t, asked(w, "reader-vision-2"))
		assert.True(t, w.s.readerServesTier("reader-batman-2", cardhdr.RouteFlash), "it reads flash")
		assert.False(t, w.s.readerServesTier("reader-batman-2", cardhdr.RouteHeavy))
	})

	t.Run("a friend's reader naming no tier reads every tier", func(t *testing.T) {
		t.Parallel()
		w := setup(t, nil, "reader-stella", "reader-emma")
		w.s.Fleet.SetRows([]string{FriendRow("stella"), FriendRow("emma")})
		assert.True(t, w.s.readerServesTier("reader-stella", cardhdr.RoutePro))
		assert.True(t, w.s.readerServesTier("reader-emma", cardhdr.RouteFrontier))
	})

	t.Run("a row naming pro is asked it", func(t *testing.T) {
		t.Parallel()
		w := setup(t, map[string]string{"reader-johnny": "flash,pro", "reader-zhi": "flash,pro"}, "reader-johnny", "reader-zhi")
		askReaders(t, w, nil)
		assert.True(t, asked(w, "reader-johnny") || asked(w, "reader-zhi"), "a pro row reads the pro card's first read")
	})

}

// The backup state is the table's three counts: reads when review exceeds working, merges
// when merging exceeds review and working together, none else; and the reads waiting are the
// reads wanted now over the primaries in review, every read a card needs at once.
func TestBackupStateIsTheTablesThreeCounts(t *testing.T) {
	t.Parallel()
	assert.Equal(t, BackupNone, BackupOf(5, 5, 0))
	assert.Equal(t, BackupReads, BackupOf(5, 6, 0))
	assert.Equal(t, BackupReads, BackupOf(76, 270, 51), "the owner's 4:49 PM table")
	assert.Equal(t, BackupMerges, BackupOf(2, 3, 6))
	assert.Equal(t, BackupMerges, BackupOf(2, 9, 12), "merges names the further bottleneck")

	w, _ := priorityWorld(t, 5, 3, 17, 0)
	assert.Equal(t, 3+17*2, ReadsWaiting(w.s), "every read each card needs is wanted now (reads are asked together): one for each flash card, two for each frontier card")
	working, review, merging := PipelineCounts(w.s)
	assert.Equal(t, [3]int{5, 20, 0}, [3]int{working, review, merging})
}

// readersReadEveryTier names every tier on each reader row of the world, as reader set
// --tiers all does: a fleet row that names no tier reads flash alone while the store holds
// routes (fleetReadsFlashOnly).
func readersReadEveryTier(w *world) {
	if w.s.Readers.Texts == nil {
		w.s.Readers.Texts = map[string]map[string]string{}
	}
	for _, rd := range w.s.Readers.Rows() {
		w.s.Readers.Texts[rd] = map[string]string{ReaderTiers: "flash,pro,heavy,frontier"}
	}
}

// Backup is the snapshot's backup state (BackupOf over PipelineCounts).
func Backup(s *Snapshot) string { return BackupOf(PipelineCounts(s)) }

// PipelineCounts is the work table's primaries working, in review and merging, sentinels
// aside, over the streams on the table.
func PipelineCounts(s *Snapshot) (working, review, merging int) {
	if s == nil || s.Work == nil {
		return 0, 0, 0
	}
	for _, c := range s.Work.Column(Working, Review, Merging) {
		if IsSentinel(c) {
			continue
		}
		switch c.Col {
		case Working:
			working++
		case Review:
			review++
		case Merging:
			merging++
		}
	}
	return working, review, merging
}
