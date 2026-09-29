package sprint

import "sort"

// Position (docs/SPEC-SPRINT.md, sentinel cards): a sentinel blocks by its
// place in its stream's line. What sorts before it, and what is in flight
// past it, is what it waits for; what waits behind it is every primary of
// the stream that sorts after it and has not started. None of this is
// written on any card: a card's stored needs are only what it names.

// openLine is the row's cards that have not landed, in score order: the
// stream's line as it stands.
func (t *Table) openLine(row string) []*Card {
	t.index()
	if t.lines == nil {
		t.lines = map[string][]*Card{}
	}
	if l, ok := t.lines[row]; ok {
		return l
	}
	var l []*Card
	for _, st := range States {
		if st != Landed {
			l = append(l, t.cells[[2]string{row, string(st)}]...)
		}
	}
	SortCards(l)
	t.lines[row] = l
	return l
}

// StopBefore is the sentinel a primary at score in the stream waits behind:
// the last one before it that has not landed (landing: this step lands it).
// nil when none.
func StopBefore(s *Snapshot, stream string, score float64, landing map[string]bool) *Card {
	line := s.Work.openLine(stream)
	k := sort.Search(len(line), func(i int) bool { return line[i].Score >= score })
	for i := k - 1; i >= 0; i-- {
		if c := line[i]; IsSentinel(c) && !landing[c.ID] {
			return c
		}
	}
	return nil
}

// PositionWaits is what a primary waits for by its place in line: for a
// sentinel, every primary of its stream before it that has not landed (a
// dropped one is off the line); for a waiting primary, the sentinel it waits
// behind. It is the one reading of position: the needs rule, resolve,
// reached, card and check all read it.
func PositionWaits(s *Snapshot, c *Card, landing map[string]bool) []string {
	if c == nil || !c.Placed() || c.Col == string(Landed) || s.Work == nil {
		return nil
	}
	if !IsSentinel(c) {
		if c.Col != string(Waiting) {
			return nil // ready or started: past any stop
		}
		if st := StopBefore(s, c.Row, c.Score, landing); st != nil {
			return []string{st.ID}
		}
		return nil
	}
	var out []string
	for _, x := range s.Work.openLine(c.Row) {
		if x.Score >= c.Score {
			break
		}
		if x.ID != c.ID && !landing[x.ID] {
			out = append(out, x.ID)
		}
	}
	return out
}

// Behind is the waiting primaries whose stop is the sentinel: what its
// release lets go.
func Behind(s *Snapshot, st *Card) []*Card {
	var out []*Card
	for _, x := range s.Work.openLine(st.Row) {
		if x.Score > st.Score && x.Col == string(Waiting) && !IsSentinel(x) {
			if b := StopBefore(s, x.Row, x.Score, nil); b != nil && b.ID == st.ID {
				out = append(out, x)
			}
		}
	}
	return out
}
