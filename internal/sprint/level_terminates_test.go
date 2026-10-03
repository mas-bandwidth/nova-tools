package sprint

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refusedBy is a work card's fields when the member refused it at staging.
func refusedBy(member string) map[string]string {
	return map[string]string{FieldStagingTake + "1": ProviderTake{Member: member}.String()}
}

// wedgeWorld is the fleet of the wedge of 2026-10-02 (nova-tools#5122, the
// diagnosis's reproduction): four members up in the order A, T, S, U. A (width
// 3) holds 3 working and 3 ready, its newest refused at staging by S; T (3)
// holds 3 working and 2 ready; S (3) holds 3 working and 1 ready, the emptiest
// open member; U (1) holds 5 working and no ready, over its room, raising the
// mean.
func wedgeWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, "reader-a")
	for _, m := range []struct {
		name  string
		width int
	}{{"A", 3}, {"T", 3}, {"S", 3}, {"U", 1}} {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m.name, Width: m.width}))
	}
	score := 1.0
	put := func(member, col string, n int) {
		for i := 0; i < n; i++ {
			putWorkCard(w, fmt.Sprintf("%s-%s-%d", member, col, i), member, col, score, nil)
			score++
		}
	}
	put("A", Working, 3)
	put("A", Ready, 2)
	putWorkCard(w, "A-refused", "A", Ready, 99, refusedBy("S"))
	put("T", Working, 3)
	put("T", Ready, 2)
	put("S", Working, 3)
	put("S", Ready, 1)
	put("U", Working, 5)
	return w
}

// levelHolds says what every level plan must: no card moved twice, no more
// moves than the fleet had ready cards, no card onto a member that refused it
// at staging (tla/Level.tla: MovesBounded, NeverOnARefuser).
func levelHolds(t *testing.T, s *Snapshot, p Plan) {
	t.Helper()
	ready := 0
	for _, m := range s.UpMembers() {
		ready += s.Fleet.Count(m, Ready)
	}
	assert.LessOrEqual(t, len(p.Units), ready, "more moves than ready cards: %v", movedLines(p))
	seen := map[string]bool{}
	for _, u := range p.Units {
		assert.False(t, seen[u.Key], "%s moved twice in one plan: %v", u.Key, movedLines(p))
		seen[u.Key] = true
		for _, ch := range u.Changes {
			if ch.Entry.Move != nil {
				assert.NotContains(t, StagingRefusers(s.Fleet.Card(u.Key)), ch.Entry.Move.Row, "%s moved onto a member that refused it", u.Key)
			}
		}
	}
}

func movedLines(p Plan) []string {
	var out []string
	for _, u := range p.Units {
		out = append(out, u.Moved)
	}
	return out
}

// The wedge's shape under the rule: the newest card of the longest queue has
// no target (its one member two below refused it), so the next older card
// moves to the emptiest member, and the call returns (Zhi's guard at card
// selection, zhi-b7086744a0b9: the gap is tested per card, so a blocked newest
// card never stops an older one).
func TestLevelMovesAnOlderCardWhenTheNewestIsBlocked(t *testing.T) {
	t.Parallel()
	w := wedgeWorld(t)
	p := FleetStep(w.s, FleetReq{Op: "level", Who: MachineActor})
	levelHolds(t, w.s, p)
	assert.Equal(t, []string{"A-ready-1.w1 A:ready -> S:ready gen=3"}, movedLines(p))
	w.must(p)
	assert.Equal(t, "A", w.s.Fleet.Card("A-refused.w1").Row, "the refused card stays")
	assert.Equal(t, "S", w.s.Fleet.Card("A-ready-1.w1").Row)
}

// The ready count ends the loop alone: the rule of the wedge (b7776ca3: the
// card's refusers and the longest avoided, no gap), on the wedge's shape, does
// not move the card back and forth, because a card the call moved is never
// queued again (tla/Level.tla, MCLevelOldRuleNoRequeue). The target counts its
// calls so that a loop that does not end fails here, not in the heap.
func TestLevelEndsUnderTheWedgeRuleWithinTheReadyCount(t *testing.T) {
	t.Parallel()
	w := wedgeWorld(t)
	asked := 0
	wedge := func(r *round, up []string, n, held, widths map[string]int, from string, avoid []string) string {
		asked++
		require.LessOrEqual(t, asked, 100, "the level asked for a target %d times: it does not end", asked)
		return wedgeLevelTo(r, up, n, held, widths, from, avoid)
	}
	var p Plan
	up := w.s.UpMembers()
	levelWith(w.s, &p, up, dealRound(w.s), roundMoves{}, nil, wedge)
	levelHolds(t, w.s, p)
	assert.NotEmpty(t, p.Units)
}

// wedgeLevelTo is round.levelTo as it was at b7776ca3: the longest avoided
// with the card's refusers, any open member at or below the mean a target, no
// gap.
func wedgeLevelTo(r *round, up []string, n, held, widths map[string]int, from string, avoid []string) string {
	avoid = append(append([]string(nil), avoid...), from)
	total := 0
	for _, m := range up {
		total += n[m]
	}
	mean := total / len(up)
	if total < 0 && total%len(up) != 0 {
		mean--
	}
	open := func(x string) bool { return contains(up, x) && held[x] < widths[x] && !contains(avoid, x) }
	to := r.scan(func(x string) bool { return open(x) && n[x] < mean })
	if to == "" {
		to = r.scan(func(x string) bool { return open(x) && n[x] <= mean })
	}
	if to != "" {
		r.moved(to)
	}
	return to
}

// The shape TLC found (tla/Level.tla, MCLevelBrokenRequeue): members up in the
// order c, a, b, widths 3, 2 and 1, the deal's index at a; b holds three ready
// cards, the middle one refused at staging by c. With a moved card queued again
// on its receiver (PR #5127 as it stood), b3 went b -> a, then a -> c: four
// moves for three ready cards, and the second unit of b3 guarded on the place
// b3 had before the first. A card the call moved is not queued again: three
// moves, each card once.
func TestLevelMovesNoCardTwice(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	for _, m := range []struct {
		name  string
		width int
	}{{"c", 3}, {"a", 2}, {"b", 1}} {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m.name, Width: m.width}))
	}
	putWorkCard(w, "b1", "b", Ready, 1, nil)
	putWorkCard(w, "b2", "b", Ready, 2, refusedBy("c"))
	putWorkCard(w, "b3", "b", Ready, 3, nil)
	p := FleetStep(w.s, FleetReq{Op: "level", Who: MachineActor})
	levelHolds(t, w.s, p)
	assert.Equal(t, []string{"b3.w1 b:ready -> a:ready gen=3", "b2.w1 b:ready -> a:ready gen=3", "b1.w1 b:ready -> c:ready gen=3"}, movedLines(p))
	w.must(p)
	assert.Equal(t, map[string]int{"a": 2, "b": 0, "c": 1}, perMember(w.s))
}
