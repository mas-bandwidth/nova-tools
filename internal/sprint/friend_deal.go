package sprint

import (
	"fmt"
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
// works at once, her friends row's), her status (FriendStatus: up, held or down), her
// tier (her nova-config friend row's tier or highest tier among tiers, the tier she can
// do) and her mode (her delivery mode: batch or one-shot).
type FriendSeat struct {
	Name   string
	Width  int
	Status string
	Tier   string
	Tiers  []string
	Mode   string
}

// FriendTier returns the friend's model tier (docs/SPEC-SPRINT.md section 1): Tier if set,
// else the highest tier among Tiers, or "" when none is specified.
func (f FriendSeat) FriendTier() string {
	if f.Tier != "" {
		return f.Tier
	}
	best := ""
	for _, t := range f.Tiers {
		if tierRank(t) > tierRank(best) {
			best = t
		}
	}
	return best
}

// FriendCardTier returns the tier required by a friend's card (docs/SPEC-SPRINT.md section 1):
// the tier the coordinator pinned (rework --tier), else the tier its brief's line 1 names,
// flash when it names none (ceilingTier), never the flash-first ladder's.
func FriendCardTier(c *Card) string {
	m, _ := cardhdr.ReadModel(c.F("brief"))
	return ceilingTier(c, m)
}

func tierRank(tier string) int {
	switch strings.ToLower(tier) {
	case cardhdr.RouteFlash:
		return 1
	case cardhdr.RoutePro:
		return 2
	case cardhdr.RouteFrontier:
		return 3
	default:
		return 0
	}
}

// FriendCanDo reports whether a friend with friendTier can take a card of cardTier
// (docs/SPEC-SPRINT.md section 1): a friend with a tier gets only cards at or below it.
// A friend with no tier (empty) has no tier restriction.
func FriendCanDo(friendTier, cardTier string) bool {
	if friendTier == "" {
		return true
	}
	fRank := tierRank(friendTier)
	cRank := tierRank(cardTier)
	if fRank == 0 {
		return true
	}
	return cRank <= fRank
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
// the friends up, each within her room and tier (docs/SPEC-SPRINT.md section 1):
// a friend with a tier gets only cards at or below it; a one-shot friend gets one card
// at a time (capacity of 1, whatever her width); a batch friend gets up to her width.
// A card naming a friend goes to her while she is up, within her room and able, and waits
// ready otherwise; a card for any friend goes to the friend up who can do the card with
// the most free width, the first by name among equals, as the machines' rule fills the
// member with room. Each is its next attempt's work card, created on the friend's row in
// working at generation 1 (dealt and taken now: its deadline is the working one), carrying
// the primary's fix, finding and why as a machine's deal does; its primary moves ready -> working.
// The friend's row is declared by the plan the first time she is dealt to.
func FriendDeal(s *Snapshot, cards []*Card, seats []FriendSeat) Plan {
	var p Plan
	free := map[string]int{}
	var up []string
	seatByName := make(map[string]FriendSeat, len(seats))
	for _, f := range seats {
		seatByName[f.Name] = f
		if f.Status == Up {
			cap := f.Width
			if f.Mode == "one-shot" {
				cap = 1
			}
			free[f.Name] = cap - friendLoad(s, f.Name)
			up = append(up, f.Name)
		}
	}
	slices.Sort(up)
	declared := map[string]bool{}
	for _, c := range cards {
		name, ok := FriendCard(c)
		if !ok || c.Col != Ready || IsSentinel(c) {
			continue
		}
		cardTier := FriendCardTier(c)
		if name == "" {
			for _, f := range up {
				st := seatByName[f]
				if FriendCanDo(st.FriendTier(), cardTier) && free[f] > 0 && (name == "" || free[f] > free[name]) {
					name = f
				}
			}
		} else {
			st, hasSeat := seatByName[name]
			if !hasSeat || !FriendCanDo(st.FriendTier(), cardTier) || free[name] <= 0 {
				continue // named friend cannot take this tier, is not up, or has no room: it waits ready
			}
		}
		if name == "" || free[name] <= 0 {
			continue // no friend it may go to is up with room: it waits ready
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
