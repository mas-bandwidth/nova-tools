package refmodel_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
)

// TestFleetDownAndLevelGoRoundTheFleet verifies on the reference model (the twin)
// with eight members that:
//  1. When a member goes down, its cards go round the fleet from the rolling
//     index (DealLast) via PlaceOn, and DealLast moves past each member dealt to.
//     A destination mapping that picks shortest queue by name is rejected with badChoice.
//  2. Levelling (Level / levelRound) moves excess cards from the longest queue
//     to members below the mean starting past DealLast, advancing DealLast past
//     the recipient. Moves not matching the rolling index are rejected.
func TestFleetDownAndLevelGoRoundTheFleet(t *testing.T) {
	t.Parallel()
	members := []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"}
	s := refmodel.New([]string{"r1"}, members, "tester")
	for _, m := range members {
		s.Members[m] = refmodel.Up
	}

	// 12 primaries: p01..p12 on stream s1
	// Primaries p01..p04 have score 1..4; p05..p08 score 5..8; p09..p12 score 9..12
	for i := 1; i <= 12; i++ {
		id := fmt.Sprintf("p%02d", i)
		s.Primaries[id] = refmodel.Primary{
			Stream: "s1", State: refmodel.Working, Score: float64(i), Attempt: 1,
		}
	}

	// Deal 12 cards round m1..m8:
	// m1 has p01.w1 and p09.w1 (ready)
	// m2..m8 have their cards finished (off)
	s.Work["p01.w1"] = refmodel.WorkCard{Primary: "p01", Member: "m1", Place: refmodel.FReady, Gen: 1}
	s.Work["p09.w1"] = refmodel.WorkCard{Primary: "p09", Member: "m1", Place: refmodel.FReady, Gen: 1}
	s.Work["p02.w1"] = refmodel.WorkCard{Primary: "p02", Member: "m2", Place: refmodel.FDone, Gen: 1}
	s.Work["p10.w1"] = refmodel.WorkCard{Primary: "p10", Member: "m2", Place: refmodel.FDone, Gen: 1}
	s.Work["p03.w1"] = refmodel.WorkCard{Primary: "p03", Member: "m3", Place: refmodel.FDone, Gen: 1}
	s.Work["p11.w1"] = refmodel.WorkCard{Primary: "p11", Member: "m3", Place: refmodel.FDone, Gen: 1}
	s.Work["p04.w1"] = refmodel.WorkCard{Primary: "p04", Member: "m4", Place: refmodel.FDone, Gen: 1}
	s.Work["p12.w1"] = refmodel.WorkCard{Primary: "p12", Member: "m4", Place: refmodel.FDone, Gen: 1}
	s.Work["p05.w1"] = refmodel.WorkCard{Primary: "p05", Member: "m5", Place: refmodel.FDone, Gen: 1}
	s.Work["p06.w1"] = refmodel.WorkCard{Primary: "p06", Member: "m6", Place: refmodel.FDone, Gen: 1}
	s.Work["p07.w1"] = refmodel.WorkCard{Primary: "p07", Member: "m7", Place: refmodel.FDone, Gen: 1}
	s.Work["p08.w1"] = refmodel.WorkCard{Primary: "p08", Member: "m8", Place: refmodel.FDone, Gen: 1}

	// The rolling index is past m4 (counter 4)
	s.DealLast = "4"

	// All other members m2..m8 have 0 ready/working cards.
	// Under shortest queue by name, m1's cards would be assigned to m2 and m3 (ties by name).
	shortestDest := map[string]string{
		"p01.w1": "m2",
		"p09.w1": "m3",
	}
	_, err := refmodel.FleetDown(s, "m1", shortestDest)
	require.Error(t, err, "FleetDown with shortest-queue-by-name dest succeeded, want refusal (not round the fleet)")

	// Under rolling index from DealLast "4" (past m4), cards must go to m5 and m6:
	roundDest := map[string]string{
		"p01.w1": "m5",
		"p09.w1": "m6",
	}
	sDown, err := refmodel.FleetDown(s, "m1", roundDest)
	require.NoError(t, err, "FleetDown with rolling index dest failed: %v", err)
	require.Equal(t, "6", sDown.DealLast, "DealLast after FleetDown: %q, want 6", sDown.DealLast)
	w := sDown.Work["p01.w1"]
	require.Equal(t, "m5", w.Member, "p01.w1 after FleetDown: %+v, want on m5 ready", w)
	require.Equal(t, refmodel.FReady, w.Place, "p01.w1 after FleetDown: %+v, want on m5 ready", w)
	w = sDown.Work["p09.w1"]
	require.Equal(t, "m6", w.Member, "p09.w1 after FleetDown: %+v, want on m6 ready", w)
	require.Equal(t, refmodel.FReady, w.Place, "p09.w1 after FleetDown: %+v, want on m6 ready", w)

	// Now test Levelling on the twin with 8 members:
	// Bring m1 back up.
	sLevel := sDown.Clone()
	sLevel.Members["m1"] = refmodel.Up

	// Set up uneven ready queues:
	// m1: 0 cards
	// m2..m6: 1 card each (d02..d06)
	// m7..m8: 2 cards each (d07a, d07b, d08a, d08b)
	// Total = 9 cards across 8 members. Mean = 9/8 = 1.
	// Longest queue has 2 cards, shortest (m1) has 0 cards (diff = 2 > 1).
	// Set DealLast = "m8".
	sLevel.Work = map[string]refmodel.WorkCard{}
	sLevel.Primaries = map[string]refmodel.Primary{}
	makeCard := func(id, m string, score float64) {
		sLevel.Primaries[id] = refmodel.Primary{Stream: "s2", State: refmodel.Working, Score: score, Attempt: 1}
		sLevel.Work[id+".w1"] = refmodel.WorkCard{Primary: id, Member: m, Place: refmodel.FReady, Gen: 1}
	}
	makeCard("d02", "m2", 2)
	makeCard("d03", "m3", 3)
	makeCard("d04", "m4", 4)
	makeCard("d05", "m5", 5)
	makeCard("d06", "m6", 6)
	makeCard("d07a", "m7", 7)
	makeCard("d07b", "m7", 8)
	makeCard("d08a", "m8", 9)
	makeCard("d08b", "m8", 10) // newest on longest queue

	sLevel.DealLast = "8"

	// From past m8 (counter 8), the next member below the mean (1) is m1 (count 0 < 1).
	// The newest card from the first longest queue (d07b.w1 on m7) moves to m1.
	// DealLast moves to 9 (past m1).
	sAfterLevel, err := refmodel.Level(sLevel, nil)
	require.NoError(t, err, "Level on uneven 8-member fleet failed: %v", err)
	require.Equal(t, "9", sAfterLevel.DealLast, "DealLast after Level: %q, want 9", sAfterLevel.DealLast)
	w = sAfterLevel.Work["d07b.w1"]
	require.Equal(t, "m1", w.Member, "d07b.w1 after Level: %+v, want on m1 ready", w)
	require.Equal(t, refmodel.FReady, w.Place, "d07b.w1 after Level: %+v, want on m1 ready", w)

	// Verify that if invalid moves are passed (e.g. moving card to m2 instead of m1),
	// Level rejects it with badChoice.
	badMoves := map[string]string{"d07b.w1": "m2"}
	_, err = refmodel.Level(sLevel, badMoves)
	require.Error(t, err, "Level with bad moves succeeded, want rejection")
}
