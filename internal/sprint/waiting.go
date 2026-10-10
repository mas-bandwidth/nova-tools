package sprint

import (
	"cmp"
	"slices"
	"strings"
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
func classifyCard(s *Snapshot, c *Card) (kind, reason, head string, length int) {
	if IsHeld(c) {
		return WaitHeld, "held", c.ID, 0
	}

	behind := PositionWaits(s, c, nil)
	for _, bID := range behind {
		bc := s.Work.Card(bID)
		if bc != nil && IsSentinel(bc) && bc.F("reached") == "" {
			h, l := followChain(s, c)
			return WaitBehind, "behind sentinel " + bID + " not released", h, l
		}
	}

	needs, _ := NeedsOf(s, c.ID)
	for _, n := range needs {
		nc := s.Work.Card(n.ID)
		if nc == nil || nc.Col == "dropped" || n.State == "off the table (dropped)" || n.State == "off the table (-)" {
			return WaitNeedMissing, "needs " + n.ID + " missing", n.ID, 1
		}
		if nc.Placed() {
			if nc.Col == string(Landed) {
				return WaitNeedLanded, "needs " + n.ID + " landed and tidied", n.ID, 1
			}
			if nc.Col != string(Waiting) {
				return WaitNeedColumn, "needs " + n.ID + " in " + nc.Col, nc.ID, 1
			}
		}
	}

	h, l := followChain(s, c)
	if strings.Contains(h, "cycle") {
		return WaitCycle, h, h, l
	}

	return WaitUnmoved, "every need has landed or was waived, and nothing moves it", h, l
}

// followChain follows a waiting card's needs and unreleased sentinels to the
// first card that is not itself waiting, returning that head and how many steps
// it took; a cycle is reported as its reason, never followed.
func followChain(s *Snapshot, start *Card) (string, int) {
	visited := map[string]bool{}
	curr := start
	length := 0

	for curr != nil {
		if visited[curr.ID] {
			return "its needs make a cycle through " + curr.ID, length
		}
		visited[curr.ID] = true

		if !curr.Placed() || curr.Col != string(Waiting) {
			return curr.ID, length
		}
		if IsHeld(curr) {
			return curr.ID, length
		}

		nextID := ""
		behind := PositionWaits(s, curr, nil)
		for _, bID := range behind {
			bc := s.Work.Card(bID)
			if bc != nil && IsSentinel(bc) && bc.F("reached") == "" {
				nextID = bID
				break
			}
		}
		if nextID == "" {
			needs, _ := NeedsOf(s, curr.ID)
			for _, n := range needs {
				nc := s.Work.Card(n.ID)
				if nc != nil && nc.Placed() && nc.Col == string(Waiting) {
					nextID = n.ID
					break
				}
			}
		}

		if nextID == "" {
			return curr.ID, length
		}

		curr = s.Work.Card(nextID)
		length++
	}
	if start != nil {
		return start.ID, length
	}
	return "", length
}
