package sprint

import (
	"cmp"
	"slices"
)

// The kind of a waiting card's block: the summary line counts one per kind,
// whatever id the reason names (docs/SPEC-SPRINT.md section 11, waiting).
const (
	WaitHeld        = "held"                // admitted held (add --held)
	WaitBehind      = "behind-sentinel"     // behind a sentinel not released
	WaitNeedColumn  = "needs-in-column"     // a need in another column
	WaitNeedMissing = "needs-missing"       // a need naming no card
	WaitNeedLanded  = "needs-landed-tidied" // a need landed and not yet tidied
	WaitCycle       = "cycle"               // its needs make a cycle
	WaitUnmoved     = "unmoved"             // every need landed or waived, and nothing moves it
)

// WaitingCard is one waiting primary's block: the reason it waits, the card at
// the head of its chain (the first card that is not itself waiting, or the held
// card or unreleased sentinel at the root) and the chain's length.
type WaitingCard struct {
	ID     string `json:"id"`
	Stream string `json:"stream"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
	Head   string `json:"head"`
	Length int    `json:"length"`
}

// WaitingView is every waiting card's block and the count of each kind, so the
// dashboard and the waiting verb read one classification.
type WaitingView struct {
	Cards  []WaitingCard  `json:"cards"`
	Counts map[string]int `json:"counts"`
}

// ClassifyWaiting is the pure classification of a snapshot's waiting cards: for
// each, why it waits and which card heads its chain. stream, when not empty,
// keeps one stream's. It reads the snapshot and writes nothing.
func ClassifyWaiting(s *Snapshot, stream string) WaitingView {
	var cards []WaitingCard
	counts := map[string]int{}

	waitingCards := s.Work.Column(Waiting)
	slices.SortStableFunc(waitingCards, func(a, b *Card) int {
		return cmp.Or(cmp.Compare(a.Row, b.Row), cmp.Compare(a.ID, b.ID))
	})

	for _, c := range waitingCards {
		if stream != "" && c.Row != stream {
			continue
		}
		kind, reason, head, length := classifyCard(s, c)
		cards = append(cards, WaitingCard{
			ID:     c.ID,
			Stream: c.Row,
			Kind:   kind,
			Reason: reason,
			Head:   head,
			Length: length,
		})
		counts[kind]++
	}

	return WaitingView{Cards: cards, Counts: counts}
}

// classifyCard is one waiting card's kind, reason, chain head and chain length.
// It follows the chain from c to the first card that is not itself waiting (a
// need in another column, a missing id or a landed record) or the held card or
// unreleased sentinel at the root, and reports the block at that root, so a
// card behind a waiting card names the real block and the card at the head of
// its chain rather than a false "unmoved" reason.
func classifyCard(s *Snapshot, c *Card) (kind, reason, head string, length int) {
	visited := map[string]bool{}
	curr := c
	for {
		if IsHeld(curr) {
			return WaitHeld, heldReason(curr), curr.ID, length
		}

		for _, bID := range PositionWaits(s, curr, nil) {
			bc := s.Work.Card(bID)
			if bc != nil && IsSentinel(bc) && bc.F("reached") == "" {
				return WaitBehind, "behind sentinel " + bID + " not released", bID, length + 1
			}
		}

		var next *Card
		needs, _ := NeedsOf(s, curr.ID)
		for _, n := range needs {
			if n.Waived {
				continue // a waived need is satisfied: it blocks nothing and heads no chain
			}
			nc := s.Work.Card(n.ID)
			if nc == nil || nc.Col == "dropped" || n.State == "off the table (dropped)" || n.State == "off the table (-)" {
				return WaitNeedMissing, "needs " + n.ID + " missing", n.ID, length + 1
			}
			if IsSentinel(nc) && nc.F("reached") == "" {
				return WaitBehind, "behind sentinel " + n.ID + " not released", n.ID, length + 1
			}
			if nc.Placed() {
				if nc.Col == string(Landed) {
					return WaitNeedLanded, "needs " + n.ID + " landed and tidied", n.ID, length + 1
				}
				if nc.Col != string(Waiting) {
					return WaitNeedColumn, "needs " + n.ID + " in " + nc.Col, nc.ID, length + 1
				}
				if next == nil {
					next = nc // a need that is itself waiting is one more step
				}
			}
		}

		if next == nil {
			return WaitUnmoved, "every need has landed or was waived, and nothing moves it", curr.ID, length
		}
		if visited[next.ID] {
			return WaitCycle, "its needs make a cycle through " + next.ID, next.ID, length + 1
		}
		visited[curr.ID] = true
		curr = next
		length++
	}
}

// heldReason is why an admitted-held card waits, naming who holds it and the
// reason: the card's held_by and held_reason when it records them, else the
// coordinator's own admit (add --held) until release.
func heldReason(c *Card) string {
	by, why := c.F(FieldHeldBy), c.F(FieldHeldReason)
	switch {
	case by != "" && why != "":
		return "held by " + by + ": " + why
	case by != "":
		return "held by " + by
	case why != "":
		return "held: " + why
	}
	return "held by the coordinator (add --held) until release"
}
