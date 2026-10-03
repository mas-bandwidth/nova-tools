package sprint

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// A friend's card (the owner, 2026-10-03: "Could we try expressing the work left for
// nova-tools-1.1.0 into cards, and doing it via the sprint, but doing parts on friends
// where we would normally do friend work."; docs/SPEC-SPRINT.md section 1, a friend's
// card). A brief whose header carries `WHO: friend` (any friend) or `WHO: friend <name>`
// (cardhdr.ReadWho) is dealt by the tick to a friend instead of a machine: its work card
// is placed on the friend's own fleet row, FriendRow(<name>), straight into working
// (nothing takes it: friend sync delivers it into her inbox), within her width, and is
// finished from her outbox by friend sync. The friend's row is a fleet row no machine
// can be: its name holds a dot, which no member name holds (ValidID), so no fleet verb
// names it, and the fleet's members (Members) leave it out: no presence, rebalance,
// level, sync or deal of the machines touches it, and a friend who goes quiet keeps her
// card (no take-back) while the deadline rule holds it as it holds any work card.

// FieldWho is a primary's worker as its brief's WHO line names it, written by add and
// brief with the brief: WhoFriend for any friend, FriendRow(<name>) for one; absent on a
// machine's card.
const FieldWho = "who"

// WhoFriend is the who of a card dealt to any friend.
const WhoFriend = "friend"

// friendRowPrefix begins every friend's fleet row: a dot, so no machine's name is one.
const friendRowPrefix = "friend."

// FriendRow is the fleet row, and the member, a friend's cards are dealt to.
func FriendRow(name string) string { return friendRowPrefix + name }

// FriendOfRow is the friend whose row it is; false for a machine's row.
func FriendOfRow(row string) (string, bool) {
	name, ok := strings.CutPrefix(row, friendRowPrefix)
	return name, ok && name != ""
}

// IsFriendRow says the fleet row is a friend's.
func IsFriendRow(row string) bool {
	_, ok := FriendOfRow(row)
	return ok
}

// WhoOfBrief is the who a brief gives its card (FieldWho): "" when its header names no
// friend (or its WHO line does not read: add refuses that brief), else WhoFriend or
// FriendRow(<name>).
func WhoOfBrief(brief string) string {
	w, why := cardhdr.ReadWho(brief)
	switch {
	case why != "" || !w.Friend:
		return ""
	case w.Name == "":
		return WhoFriend
	}
	return FriendRow(w.Name)
}

// FriendCard says the primary is a friend's card, and names the friend its WHO line
// names ("" for any friend).
func FriendCard(c *Card) (name string, ok bool) {
	w := c.F(FieldWho)
	if w == WhoFriend {
		return "", true
	}
	return FriendOfRow(w)
}

// friendCardWhy is why the machines' deal leaves a friend's card: the tick deals it to a
// friend.
const friendCardWhy = "a friend's card (its brief says WHO: friend): the tick deals it to a friend up with room, never to a machine"

// FriendSeat is one friend as the tick deals to her: her name, her width (the jobs she
// works at once, her friends row's), her status (FriendStatus: up, held or down) and her
// tiers (her nova-config friend row's, copied by friend sync: the tiers she can do).
type FriendSeat struct {
	Name   string
	Width  int
	Status string
	Tiers  []string
}

// A friend's card goes only to a friend who can do it (the owner, 2026-10-03 ~12:42 PM ET:
// "Now remember that some friends have weaker models. Freddy in particular is more like
// flash."; ~12:45 PM ET: "There is a responsibility to categorize cards for friends so they
// match to the set of friends who can do them, default all."): the card's tier (FriendTier)
// is its category, and the friends who can take it (FriendTakers) are every friend whose
// tiers include it, or for a card naming one, she alone when hers do. A friend whose row
// carries no tiers (a roster written before friend sync copied them) takes no card until
// the next sync writes them.

// FriendTier is the tier a friend's card needs: the tier the coordinator pinned (rework
// --tier), else the tier its brief's line 1 names, flash when it names none (add refuses a
// friend's card with none), never the flash-first ladder's (FieldTierNow): her own model
// runs it.
func FriendTier(c *Card) string {
	m, _ := cardhdr.ReadModel(c.F("brief"))
	return ceilingTier(c, m)
}

// FriendTakers is the names of the friends who can take the friend's card c, whatever
// their status: those whose tiers include its tier (FriendTier), only the friend it names
// when it names one, in name order; none means no friend can, and it waits under the
// tick's judgment of that tier (TickDeal, FriendTierSubject).
func FriendTakers(c *Card, seats []FriendSeat) []string {
	return FriendTakersOf(c.F(FieldWho), FriendTier(c), seats)
}

// FriendTakersOf is FriendTakers of a card by its who (FieldWho) and tier, as the where
// record carries them.
func FriendTakersOf(who, tier string, seats []FriendSeat) []string {
	name, one := FriendOfRow(who)
	if !one {
		name = "" // WhoFriend: any friend
	}
	var out []string
	for _, f := range seats {
		if (name == "" || f.Name == name) && slices.Contains(f.Tiers, tier) {
			out = append(out, f.Name)
		}
	}
	slices.Sort(out)
	return out
}

// FriendTierSubject is the subject the tick's judgment of a friend's tier is filed under:
// a tier's subject (TierSubject), friend- before the tier, so it is never the routes'.
func FriendTierSubject(tier string) string { return TierSubject(WhoFriend + "-" + tier) }

// friendTierDecisions are the decisions of that judgment: no route serves a friend, so
// none is offered; her tiers are nova-config's, and a card's tier is the coordinator's.
var friendTierDecisions = []string{"look at the card", "drop", "wait"}

// friendTierConds is the tick's judgment of the friends' cards ready that no friend can
// take (FriendTakers), one per tier, of the kind NNoRoute (a tier nothing serves), closed
// when a friend can take them or none waits; seats are the friends as the tick read them.
func friendTierConds(cards []*Card, seats []FriendSeat) []cond {
	unfilled := map[string][]string{}
	for _, c := range cards {
		if c.Col == Ready && !IsSentinel(c) && len(FriendTakers(c, seats)) == 0 {
			unfilled[FriendTier(c)] = append(unfilled[FriendTier(c)], c.ID)
		}
	}
	var friends []string
	for _, f := range seats {
		friends = append(friends, f.Name+" ("+strings.Join(noneIfEmpty(f.Tiers), ",")+")")
	}
	slices.Sort(friends)
	var out []cond
	for _, tier := range slices.Sorted(maps.Keys(unfilled)) {
		ids := unfilled[tier]
		out = append(out, cond{typ: NNoRoute, stream: FriendTierSubject(tier), streamLevel: true, primaries: ids, decisions: friendTierDecisions,
			what: fmt.Sprintf("%d friend's cards of tier %s wait and no friend who may take them can do tier %s (%s); friends: %s; give a friend the tier (nova-config friend set <friend> --tiers ..., then nova-sprint friend sync), name a friend whose tiers include it, or rework --tier",
				len(ids), tier, tier, Preview(ids, ", "), strings.Join(noneIfEmpty(friends), ", "))})
	}
	return out
}

// noneIfEmpty is the list, or the one word none when it is empty.
func noneIfEmpty(l []string) []string {
	if len(l) == 0 {
		return []string{"none"}
	}
	return l
}

// Members is the fleet's machines: its rows but the friends' (FriendRow), in row order.
func (s *Snapshot) Members() []string {
	var out []string
	for _, m := range s.Fleet.Rows() {
		if !IsFriendRow(m) {
			out = append(out, m)
		}
	}
	return out
}

// friendLoad is the cards a friend holds: ready and working on her row.
func friendLoad(s *Snapshot, name string) int {
	row := FriendRow(name)
	return s.Fleet.Count(row, Ready) + s.Fleet.Count(row, Working)
}

// FriendDeal deals the friends' cards (in the order given, the deal's stream turns) to
// the friends up who can take them (FriendTakers: her tiers include the card's tier), each
// within her width: a card naming a friend goes to her while she is up, below her width
// and able, and waits ready otherwise; a card for any friend goes to the friend up who can
// take it with the most free width, the first by name among equals, as the machines' rule
// fills the member with room. Each is its next attempt's work card, created on the
// friend's row in working at generation 1 (dealt and taken now: its deadline is the
// working one), carrying the primary's fix, finding and why as a machine's deal does; its
// primary moves ready -> working. The friend's row is declared by the plan the first
// time she is dealt to.
func FriendDeal(s *Snapshot, cards []*Card, seats []FriendSeat) Plan {
	var p Plan
	free := map[string]int{}
	for _, f := range seats {
		if f.Status == Up {
			free[f.Name] = f.Width - friendLoad(s, f.Name)
		}
	}
	declared := map[string]bool{}
	for _, c := range cards {
		if _, ok := FriendCard(c); !ok || c.Col != Ready || IsSentinel(c) {
			continue
		}
		name := ""
		for _, f := range FriendTakers(c, seats) { // in name order: the first among equals
			if free[f] > 0 && (name == "" || free[f] > free[name]) {
				name = f
			}
		}
		if name == "" {
			continue // no friend who can take it is up with room: it waits ready
		}
		card := WorkCardID(c.ID, c.Int("attempt")+1)
		if s.Fleet.Card(card) != nil {
			p.refuse(c.ID, "work card "+card+" exists already")
			continue
		}
		free[name]--
		row := FriendRow(name)
		if !s.Fleet.HasRow(row) && !declared[row] {
			p.Rows = append(p.Rows, RowAdd{Fleet, row})
			declared[row] = true
		}
		p.Units = append(p.Units, friendDealUnit(s, c, card, row))
	}
	return Lawful(p)
}

// friendDealUnit is one friend's card dealt: its work card on her row in working, and its
// primary ready -> working on it.
func friendDealUnit(s *Snapshot, c *Card, card, row string) Unit {
	attempt := c.Int("attempt") + 1
	now := stamp(s.Now)
	fields := map[string]string{"kind": "work", "primary": c.ID, "stream": c.Row, "attempt": itoa(attempt), "gen": "1", "member": row,
		"dealt": now, "first_dealt": now, "taken": now, "first_taken": now}
	for _, k := range []string{"fix", "finding", "why"} {
		if v := c.F(k); v != "" {
			fields[k] = v
		}
	}
	return Unit{Key: c.ID, Stream: c.Row, Changes: []Change{
		change(Fleet, createEntry(card, row, Working, c.Score, fields)),
		change(Work, moveEntry(c, c.Row, Working, map[string]string{"attempt": itoa(attempt), "work": card}, "result")),
	}, Moved: fmt.Sprintf("%s work %s -> working card=%s member=%s (a friend's card: friend sync delivers it to her inbox)", c.ID, c.Col, card, row)}
}
