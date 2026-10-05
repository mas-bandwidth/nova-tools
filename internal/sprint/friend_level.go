package sprint

import (
	"cmp"
	"fmt"
	"maps"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/config"
)

// The friends' level (docs/SPEC-SPRINT.md section 1, friend level; the owner, 2026-10-04:
// "What else is like this? Missing verbs we need for friends, that machines already have").
// fleet level evens the members' ready queues (level); friend level evens the friends'
// the same way, within each class (the tiers her nova-config row says she can do: a card
// moves only between friends who can do the same work). Only a card for any friend
// (WHO: friend) that is ready on her row behind her working cards and that she has not
// started moves: a card naming her stays hers, and a working card is hers to finish or the
// coordinator's to take back (FriendTake).

// FriendLevelReq is friend level: the friends as the deal sees them (FriendSeat, only
// those up level), the cards they have started as the caller read them (a push on the
// branch, a beat naming it running: those stay), and who asks.
type FriendLevelReq struct {
	Seats   []FriendSeat
	Started map[string]string
	Who     string
}

// FriendLevel evens the ready queues of the friends up within each class, as level evens
// the members': a friend's backlog is the cards on her row, ready and working, less her
// width; while the largest backlog of a friend with a card that may move and the smallest
// of the friends below their room (DealAhead times their width in batch mode; 1 in one-shot
// mode, docs/SPEC-SPRINT.md section 1, "A friend's card") differ by more than one,
// the newest card that may move of the largest goes to the smallest (the first by name
// among equals), at its next generation (its own branch and job), into working when she
// has a lane free and ready behind her working cards otherwise. Each move lowers the sum
// of squared backlogs and a card moves at most once, so it ends. The first unit's line
// says where the cards went: "moved=N to <friend>(n),... from <friend>(n),...".
func FriendLevel(s *Snapshot, r FriendLevelReq) Plan {
	return friendLevel(s, r, nil, nil)
}

// friendLevel is FriendLevel after a deal not yet applied (docs/SPEC-SPRINT.md section 1,
// friend-deal-most-room-now.w1): dealt is the cards the deal places on each friend's row, and
// dealtWorking those of them that go into working; they count against her room and her
// lanes, and none of them moves.
func friendLevel(s *Snapshot, r FriendLevelReq, dealt, dealtWorking map[string]int) Plan {
	var p Plan
	classes := map[string][]FriendSeat{}
	for _, f := range r.Seats {
		if f.Status == Up {
			classes[f.Class] = append(classes[f.Class], f)
		}
	}
	to, from, declared := map[string]int{}, map[string]int{}, map[string]bool{}
	for _, class := range slices.Sorted(maps.Keys(classes)) {
		seats := classes[class]
		slices.SortFunc(seats, func(a, b FriendSeat) int { return cmp.Compare(a.Name, b.Name) })
		held, working, width, queues := map[string]int{}, map[string]int{}, map[string]int{}, map[string][]*Card{}
		backlog := func(f string) int { return held[f] - width[f] }
		for _, f := range seats {
			w := f.Width
			if f.Mode == config.FriendModeOneShot {
				w = 1
			}
			width[f.Name] = w
			row := FriendRow(f.Name)
			held[f.Name], working[f.Name] = friendLoad(s, f.Name)+dealt[f.Name], s.Fleet.Count(row, Working)+dealtWorking[f.Name]
			for _, c := range s.Fleet.Cell(row, Ready) {
				if pr := s.Work.Placed(c.F("primary")); pr != nil && pr.F(FieldWho) == WhoFriend && r.Started[c.ID] == "" {
					queues[f.Name] = append(queues[f.Name], c)
				}
			}
			SortCards(queues[f.Name])
		}
		for {
			long, short := "", ""
			for _, f := range seats {
				if len(queues[f.Name]) > 0 && (long == "" || backlog(f.Name) > backlog(long)) {
					long = f.Name
				}
				room := DealAhead * f.Width
				if f.Mode == config.FriendModeOneShot {
					room = 1
				}
				if held[f.Name] < room && (short == "" || backlog(f.Name) < backlog(short)) {
					short = f.Name
				}
			}
			if long == "" || short == "" || backlog(long)-backlog(short) <= 1 {
				break
			}
			q := queues[long]
			c := q[len(q)-1]
			queues[long] = q[:len(q)-1]
			held[long]--
			held[short]++
			to[short]++
			from[long]++
			col := Ready
			set, unset := nextGen(c, FriendRow(short), s.Now), []string{FieldFriendDeadline}
			if working[short] < width[short] {
				working[short]++
				col = Working
				tset, tunset := friendTaken(s, c, short)
				maps.Copy(set, tset)
				delete(set, "untaken_since")
				unset = tunset
			}
			if row := FriendRow(short); !s.Fleet.HasRow(row) && !declared[row] {
				p.Rows = append(p.Rows, RowAdd{Fleet, row}) // her row, the first time a card is placed on it
				declared[row] = true
			}
			p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, FriendRow(short), col, set, unset...))},
				Moved: fmt.Sprintf("%s %s:ready -> %s:%s gen=%d", c.ID, c.Row, FriendRow(short), col, c.Int("gen")+1)})
		}
	}
	if len(p.Units) > 0 {
		n := 0
		for _, k := range to {
			n += k
		}
		p.Units[0].Moved += fmt.Sprintf("; moved=%d to %s from %s", n, countsByMember(to), countsByMember(from))
	}
	return p
}

// FriendStartedOf is the cards on a friend's row, ready or working, that she has started as
// the store knows it (docs/SPEC-SPRINT.md section 1, friend-deal-most-room-now.w1), each
// with its why: her beat names it running (running, by its id, its job or its primary), or
// it carries a progress stamp (FieldProgress). The tick levels with it; it reads no branch.
func FriendStartedOf(s *Snapshot, friend string, running []string) map[string]string {
	out := map[string]string{}
	row := FriendRow(friend)
	for _, c := range append(s.Fleet.Cell(row, Working), s.Fleet.Cell(row, Ready)...) {
		job := StoredID(c.ID, s.Epoch)
		if g := c.Int("gen"); g > 1 {
			job += ".g" + itoa(g)
		}
		switch {
		case slices.Contains(running, c.ID) || slices.Contains(running, job) || (c.F("primary") != "" && slices.Contains(running, c.F("primary"))):
			out[c.ID] = "her beat names it running"
		case c.F(FieldProgress) != "":
			out[c.ID] = "a progress stamp at " + c.F(FieldProgress)
		}
	}
	return out
}
