package sprint

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// The friends' level (docs/SPEC-SPRINT.md section 1, friend level and
// friend-deal-idle-lanes-first.w1; the owner, 2026-10-04: "What else is like this? Missing
// verbs we need for friends, that machines already have", and 2026-10-05: "This should
// not require you to remember, it should just happen mechanically."). fleet level evens
// the members' ready queues (level); friend level evens the friends', and every tick runs
// it after its deal (TickDeal), so no verb is needed. A card moves only to a friend whose
// tiers hold its tier (friendTakes), never by class, and never to a friend it has left
// (friendsLeft). Every card but a hard pin (WHO: only friend, OnlyFriend) that is ready on
// her row behind her working cards and that she has not started moves: a card with no WHO
// line, WHO: friend, or one preferring her (WHO is a preference, friends first); a hard pin
// stays hers, and a working card is hers to finish or the coordinator's to take back
// (FriendTake).

// FriendLevelPerTick is the most cards the tick's level moves in one tick.
const FriendLevelPerTick = 4

// FriendLevelReq is friend level: the friends as the deal sees them (FriendSeat, only
// those up level), the cards they have started as the caller read them (a push on the
// branch, a beat naming it running: those stay, as does every card friendStarted says
// started), who asks, and Max, the most cards moved (0: no bound; the tick's is
// FriendLevelPerTick).
type FriendLevelReq struct {
	Seats   []FriendSeat
	Started map[string]string
	Who     string
	Max     int
}

// FriendLevel evens the ready queues of the friends up, as level evens the members': a
// friend's backlog is the cards on her row, ready and working, less her width (her lanes);
// her room is DealAhead times her width (1 and 1 in one-shot mode, docs/SPEC-SPRINT.md
// section 1, "A friend's card"). A card that may move goes, newest first, from a friend
// with no idle lane to one with an idle lane, and otherwise from a larger backlog to one
// smaller by more than one; its friend is the one preferredFriend picks among those below
// their room that may take it, and the friends with no idle lane give first, the largest
// backlog first. It goes at its next generation (its own branch and job), into working
// when she has a lane free and ready behind her working cards otherwise. Each move lowers
// the idle lanes, or keeps them and lowers the sum of squared backlogs, and a card moves at
// most once, so it ends. Each move is one line; the first unit's line also says where the
// cards went: "moved=N to <friend>(n),... from <friend>(n),...".
func FriendLevel(s *Snapshot, r FriendLevelReq) Plan {
	return friendLevel(s, r, nil, nil)
}

// friendLevel is FriendLevel after a deal not yet applied: dealt is the cards the deal
// places on each friend's row, and dealtWorking those of them that go into working; they
// count against her room and her lanes, and none of them moves.
func friendLevel(s *Snapshot, r FriendLevelReq, dealt, dealtWorking map[string]int) Plan {
	var p Plan
	var seats []FriendSeat
	for _, f := range r.Seats {
		if f.Status == Up {
			seats = append(seats, f)
		}
	}
	slices.SortFunc(seats, func(a, b FriendSeat) int { return cmp.Compare(a.Name, b.Name) })
	held, working, width, room, queues := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}, map[string][]*Card{}
	backlog := func(f string) int { return held[f] - width[f] }
	lanes := func(f string) int { return width[f] - working[f] }
	for _, f := range seats {
		room[f.Name], width[f.Name] = friendRoom(f)
		row := FriendRow(f.Name)
		held[f.Name], working[f.Name] = friendLoad(s, f.Name)+dealt[f.Name], s.Fleet.Count(row, Working)+dealtWorking[f.Name]
		for _, c := range s.Fleet.Cell(row, Ready) {
			if pr := s.Work.Placed(c.F("primary")); pr != nil && !OnlyFriend(pr) && r.Started[c.ID] == "" && !friendStarted(s, f, c) {
				queues[f.Name] = append(queues[f.Name], c)
			}
		}
		SortCards(queues[f.Name])
	}
	// to is where the card goes, "" when nowhere: below her room, of its tier, not a
	// friend it left, and an idle lane for a giver with none or an even smaller backlog
	to := func(giver string, c *Card) string {
		tier, left := cardTierOf(s.Work.Placed(c.F("primary"))), friendsLeft(c)
		free, idle := map[string]int{}, map[string]int{}
		var may []string
		for _, f := range seats {
			n := f.Name
			if n == giver || held[n] >= room[n] || slices.Contains(left, n) || !friendTakes(f, tier) {
				continue
			}
			if (lanes(giver) <= 0 && lanes(n) > 0) || backlog(giver)-backlog(n) > 1 {
				free[n], idle[n] = room[n]-held[n], lanes(n)
				may = append(may, n)
			}
		}
		return preferredFriend(may, idle, free)
	}
	gives, got, moved := map[string]int{}, map[string]int{}, 0
	for r.Max == 0 || moved < r.Max {
		givers := slices.Clone(seats)
		slices.SortStableFunc(givers, func(a, b FriendSeat) int {
			if x, y := lanes(a.Name) > 0, lanes(b.Name) > 0; x != y {
				return cmp.Compare(boolInt(x), boolInt(y))
			}
			return cmp.Compare(backlog(b.Name), backlog(a.Name))
		})
		long, short, at := "", "", -1
		for _, f := range givers {
			q := queues[f.Name]
			for i := len(q) - 1; i >= 0 && short == ""; i-- {
				if t := to(f.Name, q[i]); t != "" {
					long, short, at = f.Name, t, i
				}
			}
			if short != "" {
				break
			}
		}
		if short == "" {
			break
		}
		c := queues[long][at]
		queues[long] = slices.Delete(queues[long], at, at+1)
		why := fmt.Sprintf("her backlog %d is larger than friend %s's %d by more than one", backlog(long), short, backlog(short))
		if lanes(long) <= 0 && lanes(short) > 0 {
			why = fmt.Sprintf("she has no idle lane and friend %s has one (the friends' level)", short)
		}
		held[long]--
		held[short]++
		got[short]++
		gives[long]++
		moved++
		col := Ready
		set, unset := nextGen(c, FriendRow(short), s.Now), []string{FieldFriendDeadline}
		set[FieldFriendsLeft] = strings.Join(append(friendsLeft(c), long), ",")
		if working[short] < width[short] {
			working[short]++
			col = Working
			tset, tunset := friendTaken(s, c, short)
			maps.Copy(set, tset)
			delete(set, "untaken_since")
			unset = tunset
		}
		if row := FriendRow(short); !s.Fleet.HasRow(row) && !slices.Contains(p.Rows, RowAdd{Fleet, row}) {
			p.Rows = append(p.Rows, RowAdd{Fleet, row}) // her row, the first time a card is placed on it
		}
		u := Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, FriendRow(short), col, set, unset...))},
			Moved: fmt.Sprintf("%s %s:ready -> %s:%s gen=%d", c.ID, c.Row, FriendRow(short), col, c.Int("gen")+1)}
		if pr := s.Work.Placed(c.F("primary")); pr != nil {
			if pinned, _ := FriendCard(pr); pinned == long {
				// the card prefers the friend it leaves: never silent (NPinRotated)
				n := pinRotated(s, pr, c.ID, long, why, FriendRow(short))
				if r.Who != "" {
					n.Who = r.Who
				}
				u.Notes = append(u.Notes, n)
			}
		}
		p.Units = append(p.Units, u)
	}
	if len(p.Units) > 0 {
		p.Units[0].Moved += fmt.Sprintf("; moved=%d to %s from %s", moved, countsByMember(got), countsByMember(gives))
	}
	return p
}

// boolInt is 1 for true and 0 for false.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
