package sprint

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/config"
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
// FriendRow(<name>), with an only. prefix for a hard pin.
func WhoOfBrief(brief string) string {
	w, why := cardhdr.ReadWho(brief)
	switch {
	case why != "" || !w.Friend:
		return ""
	case w.Name == "":
		return WhoFriend
	}
	if w.Only {
		return "only." + FriendRow(w.Name)
	}
	return FriendRow(w.Name)
}

// FriendCard says the primary names a friend, and names that friend ("" for any friend).
// A hard pin (only.friend.<name>) names her too.
func FriendCard(c *Card) (name string, ok bool) {
	w := strings.TrimPrefix(c.F(FieldWho), "only.")
	if w == WhoFriend {
		return "", true
	}
	return FriendOfRow(w)
}

// OnlyFriend is the explicit hard pin (docs/SPEC-SPRINT.md, WHO preference).
func OnlyFriend(c *Card) bool { return strings.HasPrefix(c.F(FieldWho), "only.friend.") }

// friendCardWhy is why the machines' deal leaves a hard-pinned card: the tick deals it
// to that friend, never to a machine or to another friend.
const friendCardWhy = "a friend's card (its brief says WHO: only friend): the tick deals it to a friend up with room, never to a machine"

// FriendSeat is one friend as the tick deals to her: her name, her width (the jobs she
// works at once, her friends row's), her status (FriendStatus: up, held or down), her
// class (the tiers her nova-config row says she can do, sorted and comma joined: friend
// level evens a class), her delivery mode (Mode: batch or one-shot, default batch), her
// tiers (config.friends tiers: flash, frontier, pro; FriendDeal does not read them; a
// frontier read does, friend_read.go), and Dir, her working directory when the ask
// writes the read brief itself (empty: friend sync writes it).
type FriendSeat struct {
	Name   string
	Width  int
	Status string
	Class  string
	Mode   string
	Tiers  []string
	Dir    string
}

// FriendMode returns the friend's delivery mode (config.FriendModeBatch or
// config.FriendModeOneShot), defaulting to config.FriendModeBatch if unset or unknown.
func (s *Snapshot) FriendMode(name string) string {
	if s == nil {
		return config.FriendModeBatch
	}
	for _, f := range s.Friends {
		if f.Name == name {
			if f.Mode != "" {
				return f.Mode
			}
			return config.FriendModeBatch
		}
	}
	return config.FriendModeBatch
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

// FriendDeal offers ready cards to the preferred friend, then to friends whose
// class covers the card's tier (docs/SPEC-SPRINT.md, WHO preference;
// tla/WhoPreference.tla checks the selection, not the room). In batch mode (the
// default) a friend's room is DealAhead times her width and her lanes are her
// width; in one-shot mode her room is 1 and her lanes are 1. A card not selected
// stays for the fleet unless it says WHO: only friend. Each selected card is its
// next attempt's work card on her row, working while she has a lane free (its
// deadline is the working one, including friend_deadline) and ready behind
// otherwise. The friend's row is declared the first time she is dealt to.
func FriendDeal(s *Snapshot, cards []*Card, seats []FriendSeat) Plan {
	var p Plan
	free, lanes := map[string]int{}, map[string]int{}
	for _, f := range seats {
		if f.Status == Up {
			room, width := DealAhead*f.Width, f.Width
			if f.Mode == config.FriendModeOneShot {
				room, width = 1, 1
			}
			free[f.Name] = room - friendLoad(s, f.Name)
			lanes[f.Name] = width - s.Fleet.Count(FriendRow(f.Name), Working)
		}
	}
	declared := map[string]bool{}
	up := s.UpMembers()
	for _, c := range cards {
		if c.Col != Ready || IsSentinel(c) {
			continue
		}
		// a card taken back from a friend (friend take) is placed again, never on her (FriendTake)
		wc := s.Fleet.Placed(WorkCardID(c.ID, c.Int("attempt")))
		if wc != nil && wc.Col != Withdrawn {
			wc = nil
		}
		// A failed attempt's bound and escalation stay with the existing dealer.
		if wc != nil && redealBound(wc) {
			continue
		}
		if held, _ := AtStagingBound(s, c, up); held != nil {
			continue
		}
		not := ""
		if wc != nil {
			not, _ = FriendOfRow(wc.F(FieldTakenFrom))
		}
		name := preferredFriend(c, seats, free, not)
		if name == "" {
			continue
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

// preferredFriend is the one preference ordering (docs/SPEC-SPRINT.md, WHO
// preference): the named friend first, even when her class does not cover the
// tier, then tier-covering friends by free room and name. No result leaves the
// card for the fleet unless it explicitly says only. An empty class covers nothing.
func preferredFriend(c *Card, seats []FriendSeat, free map[string]int, not string) string {
	preferred, _ := FriendCard(c)
	best := ""
	for _, f := range seats {
		if f.Status != Up || free[f.Name] <= 0 || f.Name == not {
			continue
		}
		if f.Name == preferred {
			return f.Name
		}
		if OnlyFriend(c) || !slices.Contains(strings.Split(f.Class, ","), cardTierOf(c)) {
			continue
		}
		if best == "" || free[f.Name] > free[best] || (free[f.Name] == free[best] && f.Name < best) {
			best = f.Name
		}
	}
	return best
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
	// a card the fleet held before carries its fleet route; a friend runs her own model, so
	// the route comes off as a first deal to her writes none (2026-10-04: a resting route
	// kept on a friend's card withdrew it every tick, and each deal again was a new inbox copy)
	set, unset := nextGen(wc, row, s.Now), []string{"withdrawn", FieldTakenBack, FieldTakenFrom,
		FieldRoute, FieldModel, FieldTokens, FieldUSD, FieldHarness, FieldDeadline}
	if wc.F(FieldTakeEnded) != "" {
		set["redeals"] = itoa(wc.Int("redeals") + 1)
		unset = append(unset, FieldTakeEnded, FieldProviderError, FieldDecided, FieldDecidedUsed)
	}
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
