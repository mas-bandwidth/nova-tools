package sprint

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// A friend's card is working once she starts it (docs/SPEC-SPRINT.md section 1, a friend's
// card is working once she starts it; the owner, 2026-10-05: "They are not working unless
// work turns from working to done.", and the same morning, on a friends table that showed a
// friend working 8 of 8 while she had started none: "it should just happen mechanically").
// The deal places a friend's card ready on her row, whatever her lanes (friendDealUnit); the
// tick moves it to working on her start receipt, her beat naming it running
// (FriendSeat.Running, friendStarted), and stamps its deadline then (friendTaken). A card
// dealt and not started within the start bound (FriendStartMax), while her beat names no
// job running, is levelled to an eligible friend with an idle lane, never back to her
// (friendUnstartedLevel), one line each.

// PropFriendStartMax is the work table's property: the start bound, how long a card dealt
// to a friend may wait ready on her row unstarted, while her beat names no job running,
// before the tick levels it to another friend, a duration.
const PropFriendStartMax = "friend_start_max"

// FriendStartMaxDefault is the start bound when the coordinator set none.
const FriendStartMaxDefault = 20 * time.Minute

// FriendStartMax is the start bound: the sprint's setting, else FriendStartMaxDefault.
func (s *Snapshot) FriendStartMax() time.Duration {
	if s.Work != nil {
		if v, ok := s.Work.Prop(PropFriendStartMax); ok {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				return d
			}
		}
	}
	return FriendStartMaxDefault
}

// friendStartUnits is the tick's start moves: every card ready on a friend's row that she
// has started (friendStarted: her beat names it running, or it carries a progress stamp)
// goes to working, taken now and carrying her deadline from now (friendTaken). A start is
// her fact, not the deal's, so her lanes do not gate it. It answers the units and how many
// go into working on each friend's row.
func friendStartUnits(s *Snapshot, seats []FriendSeat) (units []Unit, started map[string]int) {
	started = map[string]int{}
	for _, f := range seats {
		row := FriendRow(f.Name)
		ready := append([]*Card(nil), s.Fleet.Cell(row, Ready)...)
		SortCards(ready)
		for _, c := range ready {
			if !friendStarted(s, f, c) {
				continue
			}
			set, unset := friendTaken(s, c, f.Name)
			units = append(units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, row, Working, set, unset...))},
				Moved: fmt.Sprintf("%s %s:ready -> working (started: her beat names it running)", c.ID, row)})
			started[f.Name]++
		}
	}
	return units, started
}

// friendUnstartedLevel moves each card ready on an up friend's row that she has not started
// (friendStarted, nor named in skip: a card the tick moved already) and that was dealt to her
// more than the start bound ago in running time (since, the tick's clock), while her beat
// names no job running, to another friend up with an idle lane (her width less the cards on
// her row, ready and working, and those this tick places there: placed) below her room,
// whose tiers hold its tier, that it has not left, and who is not herself stuck so; the
// friend preferredFriend picks. A hard pin (WHO: only friend) stays. It goes ready at its
// next generation, she joins the friends it has left, and its line says why. At most limit
// cards move (0: no bound).
func friendUnstartedLevel(s *Snapshot, seats []FriendSeat, since func(string) (time.Duration, bool), skip map[string]bool, placed map[string]int, limit int) Plan {
	var p Plan
	bound := s.FriendStartMax()
	up := map[string]FriendSeat{}
	var names []string
	for _, f := range seats {
		if f.Status == Up {
			up[f.Name] = f
			names = append(names, f.Name)
		}
	}
	slices.Sort(names)
	// stale is each friend's cards past the bound: hers to give, and she takes none
	stale := map[string][]*Card{}
	for _, n := range names {
		f := up[n]
		if len(f.Running) > 0 {
			continue
		}
		for _, c := range s.Fleet.Cell(FriendRow(n), Ready) {
			pr := s.Work.Placed(c.F("primary"))
			if skip[c.ID] || pr == nil || OnlyFriend(pr) || friendStarted(s, f, c) {
				continue
			}
			// its deal onto her row, as the one deadline reads it (WorkDeadline: a ready
			// card's own stamp)
			if _, _, _, own := WorkDeadline(s, c); own != "" {
				if d, ok := since(c.F(own)); ok && d > bound {
					stale[n] = append(stale[n], c)
				}
			}
		}
		SortCards(stale[n])
	}
	lanes, free := map[string]int{}, map[string]int{}
	for _, n := range names {
		room, width := friendRoom(up[n])
		lanes[n] = width - friendLoad(s, n) - placed[n]
		free[n] = room - friendLoad(s, n) - placed[n]
	}
	moved := 0
	for _, giver := range names {
		for _, c := range stale[giver] {
			if limit > 0 && moved >= limit {
				return p
			}
			tier, left := cardTierOf(s.Work.Placed(c.F("primary"))), friendsLeft(c)
			var may []string
			for _, n := range names {
				if n != giver && len(stale[n]) == 0 && lanes[n] > 0 && free[n] > 0 && !slices.Contains(left, n) && friendTakes(up[n], tier) {
					may = append(may, n)
				}
			}
			to := preferredFriend(may, lanes, free)
			if to == "" {
				continue // no friend with an idle lane may take it: it stays, and the deadline rule holds it
			}
			lanes[to]--
			free[to]--
			moved++
			row := FriendRow(to)
			set := nextGen(c, row, s.Now)
			set[FieldFriendsLeft] = strings.Join(append(left, giver), ",")
			if !s.Fleet.HasRow(row) && !slices.Contains(p.Rows, RowAdd{Fleet, row}) {
				p.Rows = append(p.Rows, RowAdd{Fleet, row})
			}
			p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, row, Ready, set, FieldFriendDeadline))},
				Moved: fmt.Sprintf("%s %s:ready -> %s:ready gen=%d (not started %s after its deal, and friend %s names no job running)", c.ID, c.Row, row, c.Int("gen")+1, bound, giver)})
		}
	}
	return p
}
