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
// her row behind the cards her lanes hold and that she has not started moves: a card with no WHO
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
	// Taken is the tick's friend deal's units (friendDealPass): a ready card it moved into
	// working on her start this tick does not move.
	Taken []Unit `json:"-"`
	// Moved is the cards the tick moved already (friendUnstartedLevel): none moves twice.
	Moved map[string]bool `json:"-"`
}

// FriendLevel evens the ready queues of the friends up, as level evens the members': a
// friend's backlog is the cards on her row, ready and working, less her width (her lanes);
// her room is DealAhead times her width (in one-shot mode too, docs/SPEC-SPRINT.md
// section 1, "A friend's card"). A card that may move goes, newest first, from a friend
// with no idle lane to one with an idle lane, and otherwise from a larger backlog to one
// smaller by more than one; its friend is the one preferredFriend picks among those below
// their room that may take it, and the friends with no idle lane give first, the largest
// backlog first. A lane is idle while no card on her row holds it, and her oldest
// unstarted cards hold her free lanes and do not move here (the start bound moves them:
// friendUnstartedLevel). It goes at its next generation (its own branch and job), ready on
// her row until she starts it (docs/SPEC-SPRINT.md section 1, a friend's card is working
// once she starts it). Each move lowers
// the idle lanes, or keeps them and lowers the sum of squared backlogs, and a card moves at
// most once, so it ends. Each move is one line; the first unit's line also says where the
// cards went: "moved=N to <friend>(n),... from <friend>(n),...".
func FriendLevel(s *Snapshot, r FriendLevelReq) Plan {
	return friendLevel(s, r, nil, nil)
}

// friendLevel is FriendLevel after a deal not yet applied: dealt is the cards the deal
// places on each friend's row (and less those the tick moves off it), and dealtWorking
// the cards on her row the tick moves into working on her start; they count against her
// room and her lanes, and none of them moves.
func friendLevel(s *Snapshot, r FriendLevelReq, dealt, dealtWorking map[string]int) Plan {
	var p Plan
	var seats []FriendSeat
	for _, f := range r.Seats {
		if friendDealable(s, f) {
			seats = append(seats, f)
		}
	}
	slices.SortFunc(seats, func(a, b FriendSeat) int { return cmp.Compare(a.Name, b.Name) })
	held, width, room, queues := map[string]int{}, map[string]int{}, map[string]int{}, map[string][]*Card{}
	backlog := func(f string) int { return held[f] - width[f] }
	// a lane is idle while no card on her row holds it: a card dealt to her holds a lane
	// from its deal, started or not (friendDealUnit), as a working card did when the deal
	// placed one straight into working
	lanes := func(f string) int { return width[f] - held[f] }
	for _, f := range seats {
		room[f.Name], width[f.Name] = friendRoom(f)
		row := FriendRow(f.Name)
		held[f.Name] = friendLoad(s, f.Name) + dealt[f.Name]
		var unstarted []*Card
		for _, c := range s.Fleet.Cell(row, Ready) {
			if (dealtWorking[f.Name] > 0 && unitPromoted(r.Taken, c.ID)) || r.Moved[c.ID] || isRead(c) {
				continue // started this tick (friendStartUnits), or moved already this tick
			}
			if r.Started[c.ID] == "" && !friendStarted(s, f, c) {
				unstarted = append(unstarted, c)
			}
		}
		// the oldest unstarted cards hold her free lanes and wait for her start; the
		// friends' level moves only the ones behind them (the start bound moves the others:
		// friendUnstartedLevel)
		SortCards(unstarted)
		inLanes := max(0, width[f.Name]-s.Fleet.Count(row, Working)-dealtWorking[f.Name])
		for _, c := range unstarted[min(inLanes, len(unstarted)):] {
			if pr := s.Work.Placed(c.F("primary")); pr != nil && !OnlyFriend(pr) {
				queues[f.Name] = append(queues[f.Name], c)
			}
		}
	}
	// to is where the card goes, "" when nowhere: below her room, of its tier, not a
	// friend it left, and an idle lane for a giver with none or an even smaller backlog
	to := func(giver string, c *Card) string {
		pr := s.Work.Placed(c.F("primary"))
		tier, left := s.DealTier(pr), friendsLeft(c)
		free, idle := map[string]int{}, map[string]int{}
		var may []string
		for _, f := range seats {
			n := f.Name
			if n == giver || held[n] >= room[n] || slices.Contains(left, n) || !friendTakes(s, f, tier) || !friendRestrictionAllows(f, pr) {
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
		held[long]--
		held[short]++
		got[short]++
		gives[long]++
		moved++
		// ready on her row, as every friend's card is until she starts it (friend_start.go)
		col := Ready
		set, unset := nextGen(c, FriendRow(short), s.Now), []string{FieldFriendDeadline}
		set[FieldFriendsLeft] = strings.Join(append(friendsLeft(c), long), ",")
		if row := FriendRow(short); !s.Fleet.HasRow(row) && !slices.Contains(p.Rows, RowAdd{Fleet, row}) {
			p.Rows = append(p.Rows, RowAdd{Fleet, row}) // her row, the first time a card is placed on it
		}
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, FriendRow(short), col, set, unset...))},
			Moved: fmt.Sprintf("%s %s:ready -> %s:%s gen=%d", c.ID, c.Row, FriendRow(short), col, c.Int("gen")+1)})
	}
	if len(p.Units) > 0 {
		p.Units[0].Moved += fmt.Sprintf("; moved=%d to %s from %s", moved, countsByMember(got), countsByMember(gives))
	}
	// then a card re-tiered to a tier its holder does not serve is taken back, to be dealt
	// again to a friend that serves it (retierTakeBacks; docs/SPEC-SPRINT.md section 1, "One
	// tier for every friend decision"); none the level or the tick moved or started
	skip := maps.Clone(r.Moved)
	if skip == nil {
		skip = map[string]bool{}
	}
	for _, u := range append(slices.Clone(p.Units), r.Taken...) {
		skip[u.Key] = true
	}
	tp := retierTakeBacks(s, seats, r.Who, r.Started, skip)
	p.Units, p.Refused = append(p.Units, tp.Units...), append(p.Refused, tp.Refused...)
	return p
}

// boolInt is 1 for true and 0 for false.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
