package sprint

import (
	"fmt"
	"maps"
	"path"
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
// class (the tiers her nova-config row says she can do, sorted and comma joined: friend
// level evens a class), her tiers (config.friends tiers: flash, frontier, pro; FriendDeal
// does not read them; a frontier read does, friend_read.go), and Dir, her working
// directory when the ask writes the read brief itself (empty: friend sync writes it).
type FriendSeat struct {
	Name   string
	Width  int
	Status string
	Class  string
	Tiers  []string
	Dir    string
	// Streams and Kinds are her restriction (config.friends streams and kinds): glob
	// patterns over stream names and card KIND values; empty is no restriction.
	Streams []string
	Kinds   []string
}

// FriendRestrictionWhy is why the friend is never dealt a card of this stream and kind,
// "" when her row allows it: her streams are glob patterns over stream names and her
// kinds are card KIND values, each empty meaning no restriction (docs/SPEC-SPRINT.md
// section 1, a friend's card). The deal, add and brief all ask it, so a card is refused
// at the door by the rule that would leave it undealable.
func FriendRestrictionWhy(f FriendSeat, stream, kind string) string {
	if len(f.Streams) > 0 && !slices.ContainsFunc(f.Streams, func(g string) bool { ok, _ := path.Match(g, stream); return ok }) {
		return fmt.Sprintf("friend %s works only streams %s, and the card's stream is %s", f.Name, strings.Join(f.Streams, ","), stream)
	}
	if len(f.Kinds) > 0 && !slices.Contains(f.Kinds, kind) {
		k := kind
		if k == "" {
			k = "(none)"
		}
		return fmt.Sprintf("friend %s works only kinds %s, and the card's kind %s is outside them", f.Name, strings.Join(f.Kinds, ","), k)
	}
	return ""
}

// BriefKind is the KIND a brief's header gives its card, "" when it has none: the first
// KIND line of the header block under line 1.
func BriefKind(brief string) string {
	_, rest, _ := strings.Cut(brief, "\n")
	for rest != "" {
		var l string
		l, rest, _ = strings.Cut(rest, "\n")
		k, v, ok := cardhdr.KeyValue(l)
		if !ok {
			break
		}
		if strings.EqualFold(k, "kind") {
			return v
		}
	}
	return ""
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
// the friends up, each within her room, DealAhead times her width, as the machines'
// deal fills a member (the owner, 2026-10-04: "Do it just like the fleet, you keep
// people busy by having 2X width queued up in ready per-friend"): a card naming a
// friend goes to her while she is up and below her room, and waits ready otherwise; a
// card for any friend goes to the friend up with the most room free, the first by name
// among equals; never a friend whose restriction (FriendRestrictionWhy) leaves the card
// out, so the card waits ready when no friend within her restriction is up with room. Each is its next attempt's work card, created on the friend's row at
// generation 1, in working while she has a lane free (her width less her working cards;
// dealt and taken now: its deadline is the working one) and ready behind them otherwise
// (her finish takes the next: Finish), carrying the primary's fix, finding and why as a
// machine's deal does; its primary moves ready -> working. The friend's row is declared
// by the plan the first time she is dealt to.
func FriendDeal(s *Snapshot, cards []*Card, seats []FriendSeat) Plan {
	var p Plan
	free, lanes := map[string]int{}, map[string]int{}
	var up []string
	seat := map[string]FriendSeat{}
	for _, f := range seats {
		seat[f.Name] = f
		if f.Status == Up {
			free[f.Name] = DealAhead*f.Width - friendLoad(s, f.Name)
			lanes[f.Name] = f.Width - s.Fleet.Count(FriendRow(f.Name), Working)
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
		// a card taken back from a friend (friend take) is placed again, never on her (FriendTake)
		wc := s.Fleet.Placed(WorkCardID(c.ID, c.Int("attempt")))
		if wc != nil && wc.Col != Withdrawn {
			wc = nil
		}
		not := ""
		if wc != nil {
			not, _ = FriendOfRow(wc.F(FieldTakenFrom))
		}
		kind := BriefKind(c.F("brief"))
		if name == "" {
			for _, f := range up {
				if f != not && free[f] > 0 && FriendRestrictionWhy(seat[f], c.Row, kind) == "" && (name == "" || free[f] > free[name]) {
					name = f
				}
			}
		}
		if name == "" || name == not || free[name] <= 0 || FriendRestrictionWhy(seat[name], c.Row, kind) != "" {
			continue // no friend it may go to is up with room and within her restriction: it waits ready
		}
		card := WorkCardID(c.ID, c.Int("attempt")+1)
		if wc == nil && s.Fleet.Card(card) != nil {
			p.refuse(c.ID, "work card "+card+" exists already")
			continue
		}
		free[name]--
		col := Ready
		if lanes[name] > 0 {
			lanes[name]--
			col = Working
		}
		row := FriendRow(name)
		if !s.Fleet.HasRow(row) && !declared[row] {
			p.Rows = append(p.Rows, RowAdd{Fleet, row})
			declared[row] = true
		}
		if wc != nil {
			p.Units = append(p.Units, friendRedealUnit(s, c, wc, row, col))
			continue
		}
		p.Units = append(p.Units, friendDealUnit(s, c, card, row, col))
	}
	return Lawful(p)
}

// friendDealUnit is one friend's card dealt: its work card on her row, in working (taken
// now) or ready behind her working cards, and its primary ready -> working on it.
func friendDealUnit(s *Snapshot, c *Card, card, row, col string) Unit {
	attempt := c.Int("attempt") + 1
	now := stamp(s.Now)
	fields := map[string]string{"kind": "work", "primary": c.ID, "stream": c.Row, "attempt": itoa(attempt), "gen": "1", "member": row,
		"dealt": now, "first_dealt": now}
	if col == Working {
		fields["taken"], fields["first_taken"] = now, now
	} else {
		fields["untaken_since"] = now
	}
	for _, k := range []string{"fix", "finding", "why"} {
		if v := c.F(k); v != "" {
			fields[k] = v
		}
	}
	if col == Working {
		name, _ := FriendOfRow(row)
		dl, _ := friendDeadline(s, name)
		maps.Copy(fields, dl)
	}
	return Unit{Key: c.ID, Stream: c.Row, Changes: []Change{
		change(Fleet, createEntry(card, row, col, c.Score, fields)),
		change(Work, moveEntry(c, c.Row, Working, map[string]string{"attempt": itoa(attempt), "work": card}, "result")),
	}, Moved: fmt.Sprintf("%s work %s -> working card=%s member=%s %s (a friend's card: friend sync delivers it to her inbox)", c.ID, c.Col, card, row, col)}
}

// friendRedealUnit is a friend's card taken back (FriendTake) placed again: the same work
// card, withdrawn, on her row at its next generation (its own branch, and its own job:
// friendJobOf), in working (taken now) or ready behind her working cards, and its primary
// ready -> working on it, its attempt as it was: a take-back is no attempt and spends no
// bound.
func friendRedealUnit(s *Snapshot, c, wc *Card, row, col string) Unit {
	set, unset := nextGen(wc, row, s.Now), []string{"withdrawn", FieldTakenBack, FieldTakenFrom}
	if col == Working {
		name, _ := FriendOfRow(row)
		tset, tunset := friendTaken(s, wc, name)
		maps.Copy(set, tset)
		delete(set, "untaken_since")
		unset = append(unset, tunset...)
	} else {
		unset = append(unset, FieldFriendDeadline) // set when she takes it
	}
	return Unit{Key: c.ID, Stream: c.Row, Changes: []Change{
		change(Fleet, moveEntry(wc, row, col, set, unset...)),
		change(Work, moveEntry(c, c.Row, Working, map[string]string{"work": wc.ID}, "result")),
	}, Moved: fmt.Sprintf("%s work %s -> working card=%s member=%s gen=%d %s (taken back, dealt again: friend sync delivers it to her inbox)", c.ID, c.Col, wc.ID, row, wc.Int("gen")+1, col)}
}
