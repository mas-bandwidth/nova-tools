package sprint

import (
	"fmt"
	"time"
)

// A blocker evicts a running card (docs/SPEC-SPRINT.md section 1, Priority; the owner,
// 2026-10-06: a blocker "can even take over existing work inside the friend/fleet machine
// and kick it out and start"; "when a blocker evicts a card... it should try to evict the
// lowest priority card, and then after this, the card that has been running the shortest"; "a
// blocker can never evict another blocker card"). When a blocker is ready and the row the
// deal would place it on has no room, the deal evicts one running card there: the lowest
// level first, then among equals the one running the shortest, never another blocker. The
// evicted card's work card is withdrawn at its next generation (its pushed head stays on it,
// so the next deal starts from it, the carry a rework uses), its primary goes back to ready
// at its level, and the record names the blocker (FieldEvictedBy).

const (
	// NBlockedEvicted is the happened note of a running card evicted to make room for a
	// blocker, on the evicted primary's timeline: "<id> evicted by <blocker id>".
	NBlockedEvicted = "evicted by a blocker"
	// FieldEvictedBy is the blocker's id on an evicted work card until its next deal.
	FieldEvictedBy = "evicted_by"
	// blockerWaitWhy is why a blocker is not dealt when every running card on the row that
	// would take it is itself a blocker: a blocker never evicts a blocker, so it waits ready.
	blockerWaitWhy = "a blocker waits: every lane holds a blocker"
)

// isBlocker says the primary's level is blocker (set by hand; never computed).
func isBlocker(c *Card) bool { return c != nil && c.F(FieldPriority) == PriorityBlocker }

// evictRank is the running work card's level for eviction ordering: its primary's, else the
// level its deal wrote on it (priorityOnWork).
func evictRank(s *Snapshot, wc *Card) int {
	if pr := s.Work.Placed(wc.F("primary")); pr != nil {
		l, _ := CardPriority(pr)
		return priorityRank(l)
	}
	return priorityRank(QueuePriority(wc))
}

// evictStart is when the running card began: its taken stamp, else its dealt (a card still
// ready is never evictable; a taken card's is set). The one running the shortest is the
// latest of these.
func evictStart(wc *Card) time.Time {
	if t := stampAt(wc, "taken"); !t.IsZero() {
		return t
	}
	return stampAt(wc, "dealt")
}

// evictable says the running card may be evicted for a blocker: it is work (never a read),
// and its primary is not itself a blocker (a blocker never evicts a blocker, and a blocker's
// own cards are blocker).
func evictable(s *Snapshot, wc *Card) bool {
	if wc.F("kind") == "read" {
		return false
	}
	if pr := s.Work.Placed(wc.F("primary")); pr != nil && isBlocker(pr) {
		return false
	}
	return true
}

// evictBefore says a should be evicted before b: the lowest level first, then the one
// running the shortest (the latest start).
func evictBefore(s *Snapshot, a, b *Card) bool {
	if ar, br := evictRank(s, a), evictRank(s, b); ar != br {
		return ar > br
	}
	return evictStart(a).After(evictStart(b))
}

// evictRunning is the running work card the deal evicts from the row to place a blocker
// there: the lowest level among the row's working cards first, then the one running the
// shortest, never a blocker. nil when every running card is a blocker (nothing is evicted):
// the blocker waits ready with a judgment.
func evictRunning(s *Snapshot, row string) *Card {
	var evict *Card
	for _, wc := range s.Fleet.Cell(row, Working) {
		if !evictable(s, wc) {
			continue
		}
		if evict == nil || evictBefore(s, wc, evict) {
			evict = wc
		}
	}
	return evict
}

// evictUnit is the unit that evicts the running work card for the blocker: withdrawn at its
// next generation with the pushed work carried on it, its primary back to ready at its
// level, and a happened note naming the blocker.
func evictUnit(s *Snapshot, blocker, evict *Card) Unit {
	set := map[string]string{FieldEvictedBy: blocker.ID}
	what := fmt.Sprintf("%s evicted by %s (a blocker)", evict.ID, blocker.ID)
	return withdrawUnit(s, evict, set, nil, NBlockedEvicted, "the tick", what)
}
