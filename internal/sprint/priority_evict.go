package sprint

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"time"
)

// A blocker evicts (docs/SPEC-SPRINT.md section 1, "Priority"; tla/Priority.tla; the owner,
// 2026-10-06: a blocker "can even take over existing work inside the friend/fleet machine
// and kick it out and start"; "it should try to evict the lowest priority card, and then
// after this, the card that has been running the shortest"; "a blocker can never evict
// another blocker card"). When a blocker is ready after the deal and every row that would
// take it (a friend of its tier, an up member a route of its tier serves, its bench) is at
// its room, the deal evicts one running work card on those rows: the lowest level first,
// then among equals the one running the shortest, never a blocker. The evicted card is
// withdrawn at a new generation as the hold's take-back withdraws one (withdrawUnit): its
// lane learns of it by the generation (the member reaps the claim that moved, a finish at the
// old generation is stale), its primary goes back to ready at its own level, the card carries
// "evicted by <blocker>" (FieldTakenBack) and the generation whose branch holds its pushed
// work (FieldCarryGen, the hold's carry), and the happened note NEvicted says it. The room it
// frees takes the blocker on the tick that follows (the eviction is due work, so the next
// tick runs at once), the blocker first by the ladder (ladderOrder, friendDealByLadder), and
// a member's take takes it first (takeOne). With every lane of those rows holding a blocker,
// nothing is evicted and the blocker waits under one judgment (NBlockerWaits), which closes
// when it is dealt. The reference model (refmodel) runs this planner as the tick does.

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

// blockerRows is the rows that would take the ready blocker c, by its tier, its WHO line and
// its bench: the friends of its tier it may still go to (friendsFor, never one it left), and
// the up members when a machine route serves its tier (routeOf, onlyBench). room says one of
// them has room, so the deal places it without an eviction.
func blockerRows(s *Snapshot, c *Card, up []string) (rows []string, room bool) {
	tier := s.dealTierOf(escalating(s, c))
	wc := s.Fleet.Placed(WorkCardID(c.ID, c.Int("attempt")))
	if wc != nil && wc.Col != Withdrawn {
		wc = nil
	}
	left := friendsLeft(wc)
	pinned, pinnedCard := FriendCard(c)
	for _, f := range s.friendsFor(c, tier) {
		if slices.Contains(left, f.Name) || (pinnedCard && pinned != "" && f.Name != pinned) {
			continue
		}
		r, _ := friendRoom(f)
		if r-friendLoad(s, f.Name) > 0 {
			room = true
		}
		rows = append(rows, FriendRow(f.Name))
	}
	if !OnlyFriend(c) {
		if _, _, why, byFriend := s.routeOf(escalating(s, c), nil, nil); why == "" && !byFriend {
			for _, m := range onlyBench(up, Bench(c)) {
				if DealAhead*s.Width(m)-s.Fleet.Count(m, Ready)-s.Fleet.Count(m, Working) > 0 {
					room = true
				}
				rows = append(rows, m)
			}
		}
	}
	return rows, room
}

// evictionPick is the running work card the blocker evicts from the rows: the lowest level
// first (its primary's level as it stands, else the level its deal wrote on it), then the
// shortest running, then work order; never a blocker, never a card chosen already in this
// tick or that a unit of the plan touches already (taken). blockers is how many lanes hold a
// blocker.
func evictionPick(s *Snapshot, rows []string, taken map[string]bool) (pick *evictee, blockers int) {
	var cands []evictee
	for _, row := range rows {
		for _, wc := range s.Fleet.Cell(row, Working) {
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
		return nil, blockers
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
	return &cands[0], blockers
}

// blockerEvictions is the deal's eviction step (TickDeal): for each ready blocker the plan
// placed nowhere, whose rows are all at their room, one running card is evicted (one unit
// each, withdrawUnit), or, with every lane holding a blocker, one judgment (NBlockerWaits).
// placed is the primaries the deal placed in this tick, and touched every fleet card a unit of
// its plan changes (a start receipt, a level): one unit a card in one plan, so none is evicted.
func blockerEvictions(s *Snapshot, placed, touched map[string]bool, up []string, who string) (units []Unit, conds []cond) {
	taken := map[string]bool{}
	for id := range touched {
		taken[id] = true
	}
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
		rows, room := blockerRows(s, c, up)
		if len(rows) == 0 || room {
			continue // no row would take it (its own judgments say why), or one has room: the deal's
		}
		pick, blockers := evictionPick(s, rows, taken)
		if pick == nil {
			if blockers > 0 {
				conds = append(conds, cond{typ: NBlockerWaits, stream: c.Row, primaries: []string{c.ID},
					what: fmt.Sprintf("%s; %s (blocker) waits ready: %d lanes it could take hold blockers, and a blocker never evicts a blocker", BlockerWaitsWhat, c.ID, blockers)})
			}
			continue
		}
		taken[pick.wc.ID] = true
		pr := s.Work.Placed(pick.wc.F("primary"))
		prID, prLevel := pick.wc.F("primary"), pick.level
		if pr != nil {
			prID = pr.ID
		}
		gen := pick.wc.Int("gen")
		what := fmt.Sprintf("%s%s: %s (%s, running %s) gives its lane on %s to the blocker; %s goes back to ready at %s; its branch at gen %d holds any work it pushed",
			evictedByPrefix, c.ID, pick.wc.ID, pick.level, TookText(pick.running), pick.wc.Row, prID, prLevel, gen)
		set := map[string]string{FieldTakenBack: evictedByPrefix + c.ID, FieldCarryGen: strconv.Itoa(gen), "untaken_since": stamp(s.Now)}
		units = append(units, withdrawUnit(s, pick.wc, set, []string{"first_taken"}, NEvicted, who, what))
	}
	return units, conds
}
