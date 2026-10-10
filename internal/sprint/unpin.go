package sprint

import (
	"fmt"
	"slices"
	"strings"
)

// UnpinReq selects unstarted cards whose stored WHO preference is removed.
type UnpinReq struct {
	IDs                 []string
	Stream, Reason, Who string
}

// Unpin removes the stored WHO value without editing the brief, including on a
// running machine (docs/SPEC-SPRINT.md, WHO preference). Each refusal is local
// to its card; each successful change and its audit note commit together.
func Unpin(s *Snapshot, r UnpinReq) Plan {
	var p Plan
	if strings.TrimSpace(r.Reason) == "" || (len(r.IDs) == 0) == (r.Stream == "") {
		p.refuse("unpin", "wants ids or --stream <s>, and --reason <text>; run: nova-sprint help unpin")
		return p
	}
	ids := slices.Clone(r.IDs)
	if r.Stream != "" {
		if !s.Work.HasRow(r.Stream) {
			p.refuse(r.Stream, "no such stream; run: nova-sprint where")
			return p
		}
		for _, c := range s.Work.Cards() {
			if c.Row == r.Stream && !IsSentinel(c) {
				ids = append(ids, c.ID)
			}
		}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if why := unpinWhy(s, id); why != "" {
			p.refuse(id, why+"; inspect with nova-sprint card "+id+", or add a new unpinned card")
			continue
		}
		c := s.Work.Placed(id)
		if c.F(FieldWho) == "" {
			p.Said = append(p.Said, id+" already unpinned; no change")
			continue
		}
		name, _ := FriendCard(c)
		line := "WHO: friend"
		if OnlyFriend(c) {
			line = "WHO: only friend"
		}
		if name != "" {
			line += " " + name
		}
		n := happened("WHO unpinned", c.Row, s.Now, c.ID)
		n.Who, n.What = r.Who, fmt.Sprintf("%s dropped %s: %s", c.ID, line, r.Reason)
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row,
			Changes: []Change{change(Work, setEntry(c, nil, FieldWho))}, Notes: []Note{n},
			Moved: n.What})
	}
	return p
}

// unpinWhy admits the unstarted work returned by friend take as well as
// never-dealt primaries (docs/SPEC-SPRINT.md, WHO preference). The take-back
// mark records the friend's external-start check; an ended take or an older
// attempt is not evidence of unstarted work.
func unpinWhy(s *Snapshot, id string) string {
	c := s.Work.Placed(id)
	if c != nil && !IsSentinel(c) && c.Col == Ready && c.Int("attempt") == 1 && s.Fleet != nil {
		wc := s.Fleet.Placed(WorkCardID(id, 1))
		if wc != nil && IsFriendRow(wc.Row) && IsWithdrawn(wc.Col) && wc.F(FieldTakenBack) != "" &&
			wc.F(FieldTakeEnded) == "" && wc.Int("redeals") == 0 {
			return ""
		}
	}
	return unstarted(s, id, "its WHO pin")
}
