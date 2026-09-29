package sprint

// Sel is the set a verb acts on: ids, or a stream, or the first n, in work
// order. Only restricts the step to the keys an earlier attempt of the same
// invocation chose, so a step retried on fresh state never picks new cards.
type Sel struct {
	IDs    []string
	Stream string
	Limit  int
	Only   []string
}

// pick applies a selection. all is every candidate in work order; eligible
// says why a card cannot take the move ("" when it can); byID looks a named
// card up. Named cards that cannot move are refused with the reason; cards
// found by stream or limit are only the eligible ones.
func pick(p *Plan, sel Sel, all []*Card, streamOf func(*Card) string, eligible func(*Card) string, byID func(string) *Card) []*Card {
	ids := sel.IDs
	if sel.Only != nil {
		ids = sel.Only
	}
	if len(ids) > 0 || sel.Only != nil {
		var out []*Card
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				p.refuse(id, "named twice")
				continue
			}
			seen[id] = true
			c := byID(id)
			if c == nil {
				p.refuse(id, "no such card on the table")
				continue
			}
			if why := eligible(c); why != "" {
				p.refuse(id, why)
				continue
			}
			out = append(out, c)
		}
		if sel.Limit > 0 && len(out) > sel.Limit {
			out = out[:sel.Limit]
		}
		return out
	}
	var out []*Card
	for _, c := range all {
		if sel.Stream != "" && streamOf(c) != sel.Stream {
			continue
		}
		if eligible(c) != "" {
			continue
		}
		out = append(out, c)
		if sel.Limit > 0 && len(out) == sel.Limit {
			break
		}
	}
	return out
}

func rowOf(c *Card) string    { return c.Row }
func fieldStream(c *Card) string { return c.F("stream") }

// primaryCard looks a primary up by id: placed or kept.
func (s *Snapshot) primaryCard(id string) *Card { return s.Work.Card(id) }

func inState(c *Card, want State) string {
	if !c.Placed() {
		return "not on the table (" + orDash(c.F("outcome")) + ")"
	}
	if c.Col != want {
		return "not " + want + " (it is " + c.Col + ")"
	}
	return ""
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
