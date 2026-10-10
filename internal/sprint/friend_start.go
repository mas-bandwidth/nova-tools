package sprint

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
)

// A friend's card is working once she starts it (docs/SPEC-SPRINT.md section 1, a friend's
// card is working once she starts it; the owner, 2026-10-05: "They are not working unless
// work turns from working to done.", and the same morning, on a friends table that showed a
// friend working 8 of 8 while she had started none: "it should just happen mechanically").
// The deal places a friend's card ready on her row, whatever her lanes (friendDealUnit); it
// moves to working on her start receipt, her beat naming it running (the tick's
// friendStartUnits: FriendSeat.Running, friendStarted) or friend sync reading her job begun
// (FriendStart), which stamps its deadline then and its generation started
// (friendStartSet). A card working on her row with no start of hers goes back ready
// (friendUnstartedWorking). A card dealt and not started within the start bound
// (FriendStartMax), while she runs no job, is levelled to an eligible friend with an idle
// lane, never back to her (friendUnstartedLevel), one line each.

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

// FieldStarted is the generation of a friend's work card she has started, set by her start
// receipt (friendStartSet) and by nothing else: a card working on her row whose
// FieldStarted is not its generation was moved there by no start of hers (her finish's
// next, a take-back's next, a take), and the tick puts it back ready (friendUnstartedWorking).
const FieldStarted = "started"

// startedNow says the friend's work card carries her start at its generation (FieldStarted).
func startedNow(c *Card) bool {
	return c.F(FieldStarted) != "" && c.F(FieldStarted) == cmp.Or(c.F("gen"), "1")
}

// friendStartSet is the fields of a friend's card at her start now: taken now, her deadline
// from now (friendTaken), its generation started (FieldStarted), and, on a card no friend has
// started at any generation, its first take now (a first take a finish's or a take-back's
// next stamped is no start).
func friendStartSet(s *Snapshot, c *Card, name string) (set map[string]string, unset []string) {
	set, unset = friendTaken(s, c, name)
	if c.F(FieldStarted) == "" {
		set["first_taken"] = stamp(s.Now)
	}
	set[FieldStarted] = cmp.Or(c.F("gen"), "1")
	return set, unset
}

// friendStartUnits is the tick's start moves: every card ready on a friend's row that she
// has started (friendStarted: her beat names it running, or it carries a progress stamp)
// goes to working, taken now and carrying her deadline from now (friendStartSet). A start is
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
			set, unset := friendStartSet(s, c, f.Name)
			units = append(units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, row, Working, set, unset...))},
				Moved: fmt.Sprintf("%s %s:ready -> working (started: her beat names it running)", c.ID, row)})
			started[f.Name]++
		}
	}
	return units, started
}

// friendUnstartedWorking is the tick's check that working on a friend's row means started
// (docs/SPEC-SPRINT.md section 1, a friend's card is working once she starts it): a work card
// working on her row with no start of hers at its generation (startedNow) was moved there by
// her finish's next or a take-back's next (friendNext, FriendTake) or a take, none of which
// is her start. One she has started by the tick's own read (friendStarted: her beat names it
// running, or its progress stamp) is stamped started now, its deadline from now; any other
// goes back ready on her row, untaken, its deadline unset and its start bound from now
// (untaken_since), one line each, and friend sync's start receipt (FriendStart) or her beat
// moves it to working again when she begins it.
func friendUnstartedWorking(s *Snapshot, seats []FriendSeat) []Unit {
	var units []Unit
	for _, f := range seats {
		row := FriendRow(f.Name)
		working := append([]*Card(nil), s.Fleet.Cell(row, Working)...)
		SortCards(working)
		for _, c := range working {
			if c.F("kind") != "work" || startedNow(c) {
				continue
			}
			if friendStarted(s, f, c) {
				set, unset := friendStartSet(s, c, f.Name)
				units = append(units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, setEntry(c, set, unset...))},
					Moved: fmt.Sprintf("%s %s:working started (her beat names it running)", c.ID, row)})
				continue
			}
			unset := []string{"taken", FieldFriendDeadline}
			if c.F(FieldStarted) == "" {
				unset = append(unset, "first_taken") // no friend has started it: its first take was no start
			}
			units = append(units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, row, Ready, map[string]string{"untaken_since": stamp(s.Now)}, unset...))},
				Moved: fmt.Sprintf("%s %s:working -> ready (working with no start of hers: it waits ready until she starts it)", c.ID, row)})
		}
	}
	return units
}

// FriendStartReq is friend sync's start receipt: the friend, the cards on her row she has
// begun (by id, each at the generation sync read: Gens), and why sync reads each as begun.
type FriendStartReq struct {
	Friend string
	IDs    []string
	Gens   map[string]int
	Why    map[string]string
}

// FriendStart is friend sync's start receipt (docs/SPEC-SPRINT.md section 1, a friend's card
// is working once she starts it): each card named, ready on her row, goes to working, taken
// now, its deadline from now (friendStartSet); one working on her row with no start of hers
// (startedNow) is stamped started now, where it is. A card she started already changes
// nothing; one not on her row, not ready or working, or at another generation is refused,
// one refusal each. A start is her fact: her lanes and her status do not gate it.
func FriendStart(s *Snapshot, r FriendStartReq) Plan {
	var p Plan
	row := FriendRow(r.Friend)
	for _, id := range r.IDs {
		c := s.Fleet.Placed(id)
		switch {
		case c == nil || c.Row != row:
			p.refuse(id, fmt.Sprintf("%s is not on friend %s's row", id, r.Friend))
			continue
		case c.Col != Ready && c.Col != Working:
			p.refuse(id, fmt.Sprintf("%s is %s on friend %s's row, not ready or working", id, c.Col, r.Friend))
			continue
		case r.Gens[id] != max(c.Int("gen"), 1):
			p.refuse(id, fmt.Sprintf("%s is at generation %d, not %d: a start of another job", id, max(c.Int("gen"), 1), r.Gens[id]))
			continue
		case c.Col == Working && startedNow(c):
			continue
		}
		set, unset := friendStartSet(s, c, r.Friend)
		why := cmp.Or(r.Why[id], "friend sync read it begun")
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, row, Working, set, unset...))},
			Moved: fmt.Sprintf("%s %s:%s -> working (started: %s)", c.ID, row, c.Col, why)})
	}
	return p
}

// friendUnstartedLevel moves each card ready on an up friend's row that she has not started
// (friendStarted, nor named in skip: a card the tick moved already) and that was dealt to her,
// or put back ready on her row, more than the start bound ago in running time (since, the
// tick's clock), while she runs no job (her beat names none, and no card she started is
// working on her row) and her daemon has seen no write of hers within the bound (Active), to another friend up with an idle lane (her width less the cards on
// her row, ready and working, and those this tick places there: placed) below her room,
// whose tiers hold its tier, whose work restriction holds its primary's stream and KIND
// (friendRestrictionAllows), that it has not left, and who is not herself stuck so; the
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
		if len(f.Running) > 0 || slices.ContainsFunc(s.Fleet.Cell(FriendRow(n), Working), startedNow) {
			continue // she is running a job: her beat names one, or a card she started is working
		}
		if !f.Active.IsZero() && s.Now.Sub(f.Active) < bound {
			continue // her daemon saw a write of hers within the bound: at work on a start not yet read
		}
		for _, c := range s.Fleet.Cell(FriendRow(n), Ready) {
			pr := s.Work.Placed(c.F("primary"))
			if skip[c.ID] || pr == nil || pinnedTo(pr, n) || friendStarted(s, f, c) || isRead(c) {
				continue
			}
			// its deal onto her row (WorkDeadline: a ready card's own stamp), or its return to
			// ready since (untaken_since: a take-back, or friendUnstartedWorking), the later
			if field, _, _, own := WorkDeadline(s, c); own != "" {
				at := max(c.F(own), c.F(field))
				if d, ok := since(at); ok && d > bound {
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
			pr := s.Work.Placed(c.F("primary"))
			tier, left := cardTierOf(pr), friendsLeft(c)
			var may []string
			for _, n := range names {
				if n != giver && len(stale[n]) == 0 && lanes[n] > 0 && free[n] > 0 && !slices.Contains(left, n) && friendTakes(s, up[n], tier) && friendRestrictionAllows(up[n], pr) {
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
