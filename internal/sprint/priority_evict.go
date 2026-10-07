package sprint

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"time"
)

// A blocker evicts (docs/SPEC-SPRINT.md section 1, "A blocker evicts"; tla/Priority.tla; the
// owner, 2026-10-06: a blocker "can even take over existing work inside the friend/fleet
// machine and kick it out and start"; "it should try to evict the lowest priority card, and
// then after this, the card that has been running the shortest"; "a blocker can never evict
// another blocker card"). One step of the deal (TickDeal, blockerEvictions), two cases. A
// blocker ready after the deal whose rows (a friend of its tier it may still go to, an up
// member a machine route of its tier serves, its bench) are all at their room (DealAhead
// times the width less the load, a read half a slot: halfLoad) evicts one running work card
// on those rows and is dealt into the room it frees in the same unit (a member's ready,
// which its take takes first, takeOne; a friend's lane). A blocker dealt and ready on a row
// whose lanes are all held evicts one running work card of that row, so the lane that frees
// is its. The pick is the lowest level first, then the shortest running, then work order;
// never a blocker, never a read, never a card a unit of the plan touches. The evicted card is
// withdrawn at a new generation as the hold's take-back withdraws one (withdrawUnit): its
// lane learns of it by the generation (the member reaps the claim that moved, a finish at
// the old generation is stale; friend sync tells a friend on the bus), its primary goes back
// to ready at its own level, the card carries "evicted by <blocker>" (FieldTakenBack) and the
// generation whose branch holds its pushed work (FieldCarryGen; PacketOf starts the next
// generation from that branch), and the happened note NEvicted says it. With nothing to
// evict (every lane a blocker or a read) the blocker waits under one judgment
// (NBlockerWaits), which closes when it is dealt. The reference model (refmodel) runs this
// planner on samples that hold a blocker eviction (the scenario aBlockerEvicts).

const (
	// NEvicted is the happened note of a running card evicted by a blocker, on the evicted
	// primary's timeline.
	NEvicted = "a card evicted by a blocker"
	// NBlockerWaits is the judgment of a ready blocker that every lane of its rows holds a
	// blocker: nothing is evicted, and it waits.
	NBlockerWaits = "a blocker waits"
	// BlockerWaitsWhat opens the judgment's words.
	BlockerWaitsWhat = "a blocker waits: every lane holds a blocker"
	// evictedByPrefix opens the words the evicted card carries (FieldTakenBack) and its note.
	evictedByPrefix = "evicted by "
)

// evictee is a running work card a blocker may evict: its primary's level as a rank
// (priorityRank: the lowest level is the highest rank) and how long it has run.
type evictee struct {
	wc      *Card
	level   string
	rank    int
	running time.Duration
}

// runningFor is how long the work card has run: from the stamp the one deadline counts a
// working card from (WorkDeadline: the attempt's first take), zero when it names none the
// clock can read.
func runningFor(s *Snapshot, wc *Card) time.Duration {
	field, _, _, _ := WorkDeadline(s, wc)
	at, err := time.Parse(time.RFC3339, wc.F(field))
	if err != nil || s.Now.Before(at) {
		return 0
	}
	return s.Now.Sub(at)
}

// evictRow is a row that would take a blocker: a friend's (seat set) or an up member's. room
// says the deal has room on it (DealAhead times its width less its load, a read half a slot:
// halfLoad); busy says its lanes are all held, so a card ready on it waits for a lane.
type evictRow struct {
	row  string
	seat *FriendSeat
	room bool
	busy bool
}

// memberRow is the up member's row as the eviction reads it.
func memberRow(s *Snapshot, m string) evictRow {
	ww, wr := rowWorking(s, m)
	busy := ww+wr >= s.Width(m)
	if s.ReadCardsOn() {
		busy = 2*s.Width(m)-2*ww-wr < 2 // no half slots for a work card (takeOne)
	}
	return evictRow{row: m, room: DealAhead*s.Width(m)-halfLoad(rowLoad(s, m)) > 0, busy: busy}
}

// friendRowOf is the friend's row as the eviction reads it: her room less her load
// (friendLoad), her lanes held when her working cards fill her width.
func friendRowOf(s *Snapshot, f FriendSeat) evictRow {
	room, lanes := friendRoom(f)
	ww, wr := rowWorking(s, FriendRow(f.Name))
	held := ww + wr
	if s.ReadCardsOn() {
		held = halfLoad(ww, wr)
	}
	seat := f
	return evictRow{row: FriendRow(f.Name), seat: &seat, room: room-friendLoad(s, f.Name) > 0, busy: held >= lanes}
}

// blockerRows is the rows that would take the ready blocker c, by its tier, its WHO line and
// its bench: the friends of its tier it may still go to (friendsFor, never one it left), and
// the up members when a machine route serves its tier (routeOf, onlyBench).
func blockerRows(s *Snapshot, c *Card, up []string) []evictRow {
	tier := s.dealTierOf(escalating(s, c))
	wc := s.Fleet.Placed(WorkCardID(c.ID, c.Int("attempt")))
	if wc != nil && wc.Col != Withdrawn {
		wc = nil
	}
	left := friendsLeft(wc)
	pinned, pinnedCard := FriendCard(c)
	var rows []evictRow
	for _, f := range s.friendsFor(c, tier) {
		if slices.Contains(left, f.Name) || (pinnedCard && pinned != "" && f.Name != pinned) {
			continue
		}
		rows = append(rows, friendRowOf(s, f))
	}
	if !OnlyFriend(c) {
		if _, _, why, byFriend := s.routeOf(escalating(s, c), nil, nil); why == "" && !byFriend {
			for _, m := range onlyBench(up, Bench(c)) {
				rows = append(rows, memberRow(s, m))
			}
		}
	}
	return rows
}

// evictionPick is the running work card the blocker evicts from the rows: the lowest level
// first (its primary's level as it stands, else the level its deal wrote on it), then the
// shortest running, then work order; never a blocker, never a read card, never a card chosen
// already in this tick or that a unit of the plan touches already (taken). blockers and
// reads are how many lanes hold one.
func evictionPick(s *Snapshot, rows []string, taken map[string]bool) (pick *evictee, blockers, reads int) {
	var cands []evictee
	for _, row := range rows {
		for _, wc := range s.Fleet.Cell(row, Working) {
			if isRead(wc) {
				reads++
				continue
			}
			if wc.F("kind") != "work" || taken[wc.ID] {
				continue
			}
			level := QueuePriority(wc)
			if pr := s.Work.Placed(wc.F("primary")); pr != nil {
				level, _ = CardPriority(pr)
			}
			if level == PriorityBlocker {
				blockers++
				continue
			}
			cands = append(cands, evictee{wc: wc, level: level, rank: priorityRank(level), running: runningFor(s, wc)})
		}
	}
	if len(cands) == 0 {
		return nil, blockers, reads
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.rank != b.rank {
			return a.rank > b.rank // the lowest level first
		}
		if a.running != b.running {
			return a.running < b.running // then the shortest running
		}
		return a.wc.ID < b.wc.ID
	})
	return &cands[0], blockers, reads
}

// blockerWaits is the judgment of a blocker nothing can be evicted for: every lane it could
// take holds a blocker, or a read.
func blockerWaits(c *Card, blockers, reads int) cond {
	what := fmt.Sprintf("%s; %s (blocker) waits ready: %d lanes it could take hold blockers, and a blocker never evicts a blocker", BlockerWaitsWhat, c.ID, blockers)
	if reads > 0 {
		what = fmt.Sprintf("%s or a read; %s (blocker) waits ready: of the lanes it could take %d hold blockers and %d hold reads, and a blocker evicts neither", BlockerWaitsWhat, c.ID, blockers, reads)
	}
	return cond{typ: NBlockerWaits, stream: c.Row, primaries: []string{c.ID}, what: what}
}

// evictUnit withdraws the pick for the blocker c (withdrawUnit): its primary back to ready at
// its level, the card marked "evicted by <blocker>" with the generation whose branch holds its
// pushed work (FieldCarryGen; PacketOf starts the next generation from it), and the note.
func evictUnit(s *Snapshot, c *Card, pick *evictee, who string) Unit {
	pr := s.Work.Placed(pick.wc.F("primary"))
	prID, prLevel := pick.wc.F("primary"), pick.level
	if pr != nil {
		prID = pr.ID
	}
	gen := pick.wc.Int("gen")
	what := fmt.Sprintf("%s%s: %s (%s, running %s) gives its lane on %s to the blocker; %s goes back to ready at %s; its branch at gen %d holds the work it pushed, and the next generation starts from it",
		evictedByPrefix, c.ID, pick.wc.ID, pick.level, TookText(pick.running), pick.wc.Row, prID, prLevel, gen)
	set := map[string]string{FieldTakenBack: evictedByPrefix + c.ID, FieldCarryGen: strconv.Itoa(gen), "untaken_since": stamp(s.Now)}
	return withdrawUnit(s, pick.wc, set, []string{"first_taken"}, NEvicted, who, what)
}

// intoFriendLane moves the blocker's work card wc, ready on the friend's row, into her lane
// the eviction frees, in the same unit (as her finish's next and a take-back's next are
// moved, friendNext, FriendTake): taken now, her deadline from now; her start confirms it,
// and working with no start of hers goes back ready (friendUnstartedWorking).
func intoFriendLane(s *Snapshot, u *Unit, wc *Card, name string) {
	set, unset := friendTaken(s, wc, name)
	u.Changes = append(u.Changes, change(Fleet, moveEntry(wc, FriendRow(name), Working, set, unset...)))
	u.Moved += fmt.Sprintf("; %s ready -> working (the blocker takes the lane)", wc.ID)
}

// blockerEvictions is the deal's eviction step (TickDeal), one unit a blocker. A ready
// blocker the plan placed nowhere, whose rows are all at their room, evicts one running card
// and is dealt into the room it frees in the same unit (a member's ready, which its take
// takes first, takeOne; a friend's lane, intoFriendLane). A blocker dealt and ready on a row
// whose lanes are all held evicts one running card of that row, so the lane that frees is
// its (a member's take takes it first; a friend's lane takes it now). With no card to evict
// (every lane a blocker or a read), one judgment (blockerWaits). placed is the primaries the
// deal placed in this tick, and touched every fleet card a unit of its plan changes (a start
// receipt, a level): one unit a card in one plan, so none of those is evicted.
func blockerEvictions(s *Snapshot, placed, touched map[string]bool, up []string, who string) (units []Unit, conds []cond) {
	taken := map[string]bool{}
	for id := range touched {
		taken[id] = true
	}
	isUp := map[string]bool{}
	for _, m := range up {
		isUp[m] = true
	}
	seatOf := map[string]FriendSeat{}
	for _, f := range s.Friends {
		seatOf[FriendRow(f.Name)] = f
	}
	rowNames := func(rows []evictRow) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.row)
		}
		return out
	}
	// the blockers dealt and ready on a row whose lanes are all held: the lane that frees is theirs
	for _, wc := range s.Fleet.Column(Ready) {
		if wc.F("kind") != "work" || taken[wc.ID] {
			continue
		}
		pr := s.Work.Placed(wc.F("primary"))
		if pr == nil || pr.Col != Working || pr.F("work") != wc.ID {
			continue
		}
		if l, _ := CardPriority(pr); l != PriorityBlocker {
			continue
		}
		var row evictRow
		if f, ok := seatOf[wc.Row]; ok {
			if !friendDealable(s, f) {
				continue
			}
			row = friendRowOf(s, f)
		} else if isUp[wc.Row] {
			row = memberRow(s, wc.Row)
		} else {
			continue
		}
		if !row.busy {
			continue // a lane is free: the member's take, or her start, takes it
		}
		pick, blockers, reads := evictionPick(s, []string{row.row}, taken)
		if pick == nil {
			conds = append(conds, blockerWaits(pr, blockers, reads))
			continue
		}
		taken[pick.wc.ID], taken[wc.ID] = true, true
		u := evictUnit(s, pr, pick, who)
		if row.seat != nil {
			intoFriendLane(s, &u, wc, row.seat.Name)
		}
		units = append(units, u)
	}
	// the blockers ready and placed nowhere: no row that would take them has room
	for _, c := range ladderOrder(dealOrder(s, s.Work.Column(Ready))) {
		if cardRank(c) != priorityRank(PriorityBlocker) {
			break // the ladder's order: the blockers come first
		}
		if placed[c.ID] || IsSentinel(c) || StreamHeld(s, c.Row) || AtRedealBound(s, c) != nil {
			continue
		}
		if wc, _ := AtStagingBound(s, c, up); wc != nil {
			continue
		}
		rows := blockerRows(s, c, up)
		if len(rows) == 0 || slices.ContainsFunc(rows, func(r evictRow) bool { return r.room }) {
			continue // no row would take it (its own judgments say why), or one has room: the deal's
		}
		pick, blockers, reads := evictionPick(s, rowNames(rows), taken)
		if pick == nil {
			conds = append(conds, blockerWaits(c, blockers, reads))
			continue
		}
		taken[pick.wc.ID] = true
		u := evictUnit(s, c, pick, who)
		// the blocker into the room the eviction frees, in the same unit
		i := slices.IndexFunc(rows, func(r evictRow) bool { return r.row == pick.wc.Row })
		card := WorkCardID(c.ID, c.Int("attempt")+1)
		if s.Fleet.Card(card) != nil {
			continue // its next attempt's card exists: the deal's refusal says so
		}
		if seat := rows[i].seat; seat != nil {
			du := friendDealUnit(s, c, card, FriendRow(seat.Name), Ready, tierNowSet(c, s.dealTierOf(escalating(s, c))))
			du.Changes[0].Entry.Create.Col = Working // into the lane that frees (intoFriendLane)
			set, _ := friendTaken(s, &Card{Fields: map[string]string{}}, seat.Name)
			maps.Copy(du.Changes[0].Entry.Set, set)
			delete(du.Changes[0].Entry.Set, "untaken_since")
			u.Changes = append(u.Changes, du.Changes...)
			u.Moved += "; " + du.Moved + " (into the lane that frees)"
		} else {
			du, why := deal(s, c, c.F("fix"), pick.wc.Row, map[string]int{}, nil, nil, map[string]string{"finding": c.F("finding"), "why": c.F("why")})
			if why != "" {
				continue // not dealt this tick: nothing is evicted for it either
			}
			u.Changes = append(u.Changes, du.Changes...)
			u.Moved += "; " + du.Moved + " (into the room that frees; its take takes it first)"
		}
		units = append(units, u)
	}
	return units, conds
}
