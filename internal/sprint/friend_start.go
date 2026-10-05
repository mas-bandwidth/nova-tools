package sprint

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// A friend's card is working only once she starts it (docs/SPEC-SPRINT.md section 1,
// friend-working-means-started.w1; the owner, 2026-10-05: "They are not working unless
// work turns from working to done."). The deal places it ready. Her beat is the start
// the tree already carries (FriendSeat.Running: friend beat --running names the work
// card, its job or its primary; friendStarted also counts a progress stamp). The
// deadline is stamped then (friendTaken), not at the deal. A card dealt and not started
// within FriendStartBound, while she reports no running job, is levelled to an eligible
// friend with an idle lane, never back to her.

const (
	// PropFriendStartBound is the work table's property: how long a card dealt to a
	// friend may sit unstarted while she reports no running job. A duration.
	PropFriendStartBound = "friend_start_bound"
	// FriendStartBoundDefault is that bound when the property is unset.
	FriendStartBoundDefault = 20 * time.Minute
)

// FriendStartBound is how long a friend's card may sit dealt and not started while she
// reports no running job: the sprint's setting, else FriendStartBoundDefault.
func (s *Snapshot) FriendStartBound() time.Duration {
	if s != nil && s.Work != nil {
		if v, ok := s.Work.Prop(PropFriendStartBound); ok {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				return d
			}
		}
	}
	return FriendStartBoundDefault
}

// FriendStart moves a friend's ready cards to working when she has started them
// (friendStarted), and stamps the deadline then (friendTaken).
func FriendStart(s *Snapshot, seats []FriendSeat) Plan {
	return friendStart(s, seats)
}

// friendStart is FriendStart. A card already working is left. One line each.
func friendStart(s *Snapshot, seats []FriendSeat) Plan {
	var p Plan
	if s == nil || s.Fleet == nil {
		return p
	}
	for _, f := range seats {
		if len(f.Running) == 0 {
			continue
		}
		row := FriendRow(f.Name)
		for _, c := range s.Fleet.Cell(row, Ready) {
			if c.F("kind") != "" && c.F("kind") != "work" {
				continue
			}
			if !friendStarted(s, f, c) {
				continue
			}
			set, unset := friendTaken(s, c, f.Name)
			p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{
				change(Fleet, moveEntry(c, row, Working, set, unset...)),
			}, Moved: fmt.Sprintf("%s ready -> working (she started it; her beat names it running)", c.ID)})
		}
	}
	return p
}

// friendUnstarted moves ready cards a friend has held unstarted past FriendStartBound
// while she reports no running job. Each goes to an eligible friend with an idle lane,
// ready, never back to her, one log line. max is how many (0: no bound). skip are cards
// this plan already moves.
func friendUnstarted(s *Snapshot, seats []FriendSeat, skip map[string]bool, max int) Plan {
	var p Plan
	if s == nil || s.Fleet == nil {
		return p
	}
	bound := s.FriendStartBound()
	seat := map[string]FriendSeat{}
	held, working, width, room := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	var up []FriendSeat
	for _, f := range seats {
		if f.Status != Up {
			continue
		}
		seat[f.Name] = f
		room[f.Name], width[f.Name] = friendRoom(f)
		held[f.Name] = friendLoad(s, f.Name)
		working[f.Name] = s.Fleet.Count(FriendRow(f.Name), Working)
		up = append(up, f)
	}
	lanes := func(f string) int { return width[f] - working[f] }
	n := 0
	for _, f := range up {
		if len(f.Running) > 0 {
			continue // she reports a running job: her unstarted cards stay
		}
		row := FriendRow(f.Name)
		cards := append([]*Card(nil), s.Fleet.Cell(row, Ready)...)
		SortCards(cards)
		for _, c := range cards {
			if max > 0 && n >= max {
				return p
			}
			if skip[c.ID] || (c.F("kind") != "" && c.F("kind") != "work") || friendStarted(s, f, c) {
				continue
			}
			if pr := s.Work.Placed(c.F("primary")); pr != nil && OnlyFriend(pr) {
				continue
			}
			at, ok := friendDealtAt(c)
			if !ok || s.Now.Sub(at) < bound {
				continue
			}
			tier := cardTierOf(s.Work.Placed(c.F("primary")))
			left := friendsLeft(c)
			if !slices.Contains(left, f.Name) {
				left = append(left, f.Name)
			}
			free, idle := map[string]int{}, map[string]int{}
			var may []string
			for _, o := range up {
				name := o.Name
				if name == f.Name || held[name] >= room[name] || lanes(name) <= 0 || slices.Contains(left, name) || !friendTakes(seat[name], tier) {
					continue
				}
				free[name], idle[name] = room[name]-held[name], lanes(name)
				may = append(may, name)
			}
			dest := preferredFriend(may, idle, free)
			if dest == "" {
				continue
			}
			set, unset := nextGen(c, FriendRow(dest), s.Now), []string{FieldFriendDeadline}
			set[FieldFriendsLeft] = strings.Join(left, ",")
			if drow := FriendRow(dest); !s.Fleet.HasRow(drow) && !slices.Contains(p.Rows, RowAdd{Fleet, drow}) {
				p.Rows = append(p.Rows, RowAdd{Fleet, drow})
			}
			p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{
				change(Fleet, moveEntry(c, FriendRow(dest), Ready, set, unset...)),
			}, Moved: fmt.Sprintf("friend %s unstarted %s past %s with no running job -> %s ready", f.Name, c.ID, bound, dest)})
			held[f.Name]--
			held[dest]++
			n++
		}
	}
	return p
}

// friendDealtAt is when the card was dealt to the friend who holds it (dealt, else
// untaken_since, else first_dealt).
func friendDealtAt(c *Card) (time.Time, bool) {
	for _, f := range []string{"dealt", "untaken_since", "first_dealt"} {
		if v := c.F(f); v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}
