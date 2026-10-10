package sprint

import (
	"cmp"
	"slices"
	"strings"
)

type WaitingCard struct {
	ID     string `json:"id"`
	Stream string `json:"stream"`
	Reason string `json:"reason"`
	Head   string `json:"head"`
	Length int    `json:"length"`
}

type WaitingView struct {
	Cards  []WaitingCard  `json:"cards"`
	Counts map[string]int `json:"counts"`
}

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
		reason, head, length := classifyCard(s, c)
		cards = append(cards, WaitingCard{
			ID:     c.ID,
			Stream: c.Row,
			Reason: reason,
			Head:   head,
			Length: length,
		})
		counts[reason]++
	}

	return WaitingView{Cards: cards, Counts: counts}
}

func classifyCard(s *Snapshot, c *Card) (reason, head string, length int) {
	if IsHeld(c) {
		return "held", c.ID, 0
	}

	behind := PositionWaits(s, c, nil)
	if len(behind) > 0 {
		for _, bID := range behind {
			bc := s.Work.Card(bID)
			if bc != nil && IsSentinel(bc) && bc.F("reached") == "" {
				h, l := followChain(s, c)
				return "behind sentinel " + bID + " not released", h, l
			}
		}
	}

	needs, _ := NeedsOf(s, c.ID)
	for _, n := range needs {
		nc := s.Work.Card(n.ID)
		if nc == nil || nc.Col == "dropped" || n.State == "off the table (dropped)" || n.State == "off the table (-)" {
			return "needs " + n.ID + " missing", n.ID, 1
		}
		if nc.Placed() {
			if nc.Col == string(Landed) {
				return "needs " + n.ID + " landed and tidied", n.ID, 1
			}
			if nc.Col != string(Waiting) {
				return "needs " + n.ID + " in " + nc.Col, nc.ID, 1
			}
		}
	}

	h, l := followChain(s, c)
	if strings.Contains(h, "cycle") {
		return h, h, l
	}

	return "every need has landed or was waived, and nothing moves it", h, l
}

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
