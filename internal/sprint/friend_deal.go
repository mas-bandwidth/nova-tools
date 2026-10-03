package sprint

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// A friend's card (the owner, 2026-10-03: "Could we try expressing the work left for
// nova-tools-1.1.0 into cards, and doing it via the sprint, but doing parts on friends
// where we would normally do friend work."; docs/SPEC-SPRINT.md section 1, a friend's
// card). A brief whose header carries `WHO: friend` (any friend) or `WHO: friend <name>`
// (cardhdr.ReadWho) is dealt by the tick to a friend instead of a machine: its work card
// is placed on the friend's own fleet row, FriendRow(<name>), staged in ready reserve
// (the server keeps each eligible friend up to width active working cards plus width
// ready-to-pull reserve; Glenn, 2026-10-03 ~13:30). Her child agent takes it
// (nova-sprint friend take <card>), moving ready -> working up to her active width,
// and it is finished from her outbox by friend sync. The friend's row is a fleet row no
// machine can be: its name holds a dot, which no member name holds (ValidID), so no fleet
// verb names it, and the fleet's members (Members) leave it out: presence, rebalance,
// level, sync or deal of the machines never touches it. A friend who goes quiet keeps her
// card while her deadline holds it, while an explicit coordinator hold (friend down)
// atomically returns her ready reserve and working cards to the ready pool without
// penalty (FriendHold), fencing any stale reports.

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
// works at once, her friends row's), her status (FriendStatus: up, held or down), and her
// tiers (her nova-config friend row's, copied by friend sync: the tiers she can do).
type FriendSeat struct {
	Name   string
	Width  int
	Status string
	Tiers  []string
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

// friendReserveRoom is the room a friend has in her ready-to-pull reserve on her fleet row:
// her width minus the cards currently staged in ready on her row. The server keeps each
// eligible friend up to width active plus width ready-to-pull reserve (Glenn, 2026-10-03 ~13:30).
func friendReserveRoom(s *Snapshot, f FriendSeat) int {
	row := FriendRow(f.Name)
	return max(f.Width-s.Fleet.Count(row, Ready), 0)
}

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
// when it names one, in name order; none means no friend can.
func FriendTakers(c *Card, seats []FriendSeat) []string {
	return FriendTakersOf(c.F(FieldWho), FriendTier(c), seats)
}

// FriendTakersOf is FriendTakers of a card by its who (FieldWho) and tier.
// Recovered tier contract: a friend whose row carries no tiers takes no card until
// sync writes them (requires slices.Contains, no empty fallback).
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

// noneIfEmpty is the list, or the one word none when it is empty.
func noneIfEmpty(l []string) []string {
	if len(l) == 0 {
		return []string{"none"}
	}
	return l
}

// FriendDeal deals the friends' cards (in the order given, the deal's stream turns) to
// the friends up who can take them (FriendTakers: her tiers include the card's tier),
// keeping each friend up to width active working cards plus width ready-to-pull reserve
// (Glenn, 2026-10-03 ~13:30: "The server should keep each eligible friend up to width active
// PLUS width ready-to-pull reserve, refill on claim/completion without a coordinator nudge"):
// a card naming a friend goes to her while she is up, below her ready reserve width and able,
// and waits ready otherwise; a card for any friend goes to the friend up who can take it with
// the most free ready reserve room, the first by name among equals, as the machines' rule
// fills the member with room. Each is its next attempt's work card, created on the friend's row
// in ready at generation 1 (dealt now: its deadline is the dealt bound until she takes it,
// friend take), carrying the primary's fix, finding, why and friend_width as a machine's deal
// does; its primary moves ready -> working. The friend's row is declared by the plan the first
// time she is dealt to.
func FriendDeal(s *Snapshot, cards []*Card, seats []FriendSeat) Plan {
	var p Plan
	free := map[string]int{}
	seatsMap := map[string]FriendSeat{}
	for _, f := range seats {
		seatsMap[f.Name] = f
		if f.Status == Up {
			free[f.Name] = friendReserveRoom(s, f)
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
			continue // no friend who can take it is up with reserve room: it waits ready
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
		width := seatsMap[name].Width
		p.Units = append(p.Units, friendDealUnit(s, c, card, row, width))
	}
	return Lawful(p)
}

// friendDealUnit is one friend's card dealt: its work card on her row in ready, and its
// primary ready -> working on it.
func friendDealUnit(s *Snapshot, c *Card, card, row string, width int) Unit {
	attempt := c.Int("attempt") + 1
	now := stamp(s.Now)
	fields := map[string]string{
		"kind":         "work",
		"primary":      c.ID,
		"stream":       c.Row,
		"attempt":      itoa(attempt),
		"gen":          "1",
		"member":       row,
		"dealt":        now,
		"first_dealt":  now,
		"friend_width": itoa(width),
	}
	for _, k := range []string{"fix", "finding", "why"} {
		if v := c.F(k); v != "" {
			fields[k] = v
		}
	}
	return Unit{Key: c.ID, Stream: c.Row, Changes: []Change{
		change(Fleet, createEntry(card, row, Ready, c.Score, fields)),
		change(Work, moveEntry(c, c.Row, Working, map[string]string{"attempt": itoa(attempt), "work": card}, "result")),
	}, Moved: fmt.Sprintf("%s work %s -> working card=%s member=%s (a friend's card, ready in reserve: friend sync delivers it to her inbox, and she takes it)", c.ID, c.Col, card, row)}
}

// friendTake is a take by id on a friend's row (friend take; Take with As a friend's
// row): each card named moves ready -> working on her row, its take stamped (taken,
// first_taken, taken_by her row), refused when:
// - not named by id
// - card does not exist on her row or is not placed
// - wrong generation (liveGen)
// - taken already (c.Col == Working)
// - not in ready (c.Col != Ready)
// - past dealt deadline (WorkDeadline)
// - friend has no presence record
// - friend is held or down (status != Up)
// - active working cards already at or above her width
// - friend's allowed tiers do not include the card's tier
func friendTake(s *Snapshot, r TakeReq, name string) Plan {
	var p Plan
	if !named(r.Sel) {
		p.refuse(r.As, "a friend takes a card by id: nova-sprint friend take <card>")
		return p
	}
	seat, hasPresence := s.FriendSeat(name)
	if !hasPresence {
		for _, id := range r.Sel.IDs {
			p.refuse(id, fmt.Sprintf("friend %s has no presence record", name))
		}
		return p
	}
	if seat.Status != Up {
		for _, id := range r.Sel.IDs {
			p.refuse(id, fmt.Sprintf("friend %s is %s", name, seat.Status))
		}
		return p
	}
	working := s.Fleet.Count(r.As, Working)
	for _, id := range r.Sel.IDs {
		c := s.Fleet.Card(id)
		if c == nil || !c.Placed() || c.Row != r.As {
			where := "no such card"
			if c != nil {
				where = "it is " + placeWord(c)
			}
			p.refuse(id, "not dealt to friend "+name+" ("+where+")")
			continue
		}
		if why := liveGen("take", c, r.Gens); why != "" {
			p.refuse(id, why)
			continue
		}
		if c.Col == Working {
			p.refuse(id, "taken already at "+orDash(c.F("taken"))+": a card is taken once")
			continue
		}
		if c.Col != Ready {
			p.refuse(id, "not in "+r.As+" ready (it is "+placeWord(c)+")")
			continue
		}
		field, limit, word, _ := WorkDeadline(s, c)
		if at, err := time.Parse(time.RFC3339, c.F(field)); err == nil && s.Now.Sub(at) > limit {
			p.refuse(id, fmt.Sprintf("past its deadline: %s %s, over its bound %s (%s); the coordinator decides it (its late judgment)", strings.TrimPrefix(field, "first_"), c.F(field), limit, word))
			continue
		}
		width := seat.Width
		if width <= 0 {
			width = c.Int("friend_width")
		}
		if width <= 0 {
			width = s.Width(r.As)
		}
		if width <= 0 {
			width = 8
		}
		if working >= width {
			p.refuse(id, fmt.Sprintf("friend %s is at active width (%d working); wait for an active card to finish", name, width))
			continue
		}
		tier := FriendTier(c)
		if !slices.Contains(seat.Tiers, tier) {
			p.refuse(id, fmt.Sprintf("friend %s cannot do tier %s (allowed: %s)", name, tier, strings.Join(noneIfEmpty(seat.Tiers), ",")))
			continue
		}
		set := takenStamps(c, s.Now)
		set["taken_by"] = r.As
		working++
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, c.Row, Working, set, "untaken_since"))},
			Moved: fmt.Sprintf("%s fleet ready -> working friend=%s gen=%s", c.ID, name, c.F("gen"))})
	}
	return p
}

// FriendHold is the coordinator holding a friend (friend down): each card staged
// in ready reserve or working on her row moves to Withdrawn at a new generation
// (fencing any old assignment reports from her), its primary returned to ready
// with its brief, history, paid usage and attempted fixes preserved without penalty;
// her row is untouched if it holds none.
func FriendHold(s *Snapshot, friend, who string) Plan {
	var p Plan
	row := FriendRow(friend)
	if !s.Fleet.HasRow(row) {
		return p
	}
	cards := append(append([]*Card{}, s.Fleet.Cell(row, Ready)...), s.Fleet.Cell(row, Working)...)
	SortCards(cards)
	for _, c := range cards {
		p.Units = append(p.Units, withdrawCard(s, c, false, NWithdrawn, who, "friend "+friend+" held"))
	}
	return Lawful(p)
}
