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

// FriendDeal deals the friends' cards (in the order given, the deal's stream turns) to
// the friends up, each within her room, DealAhead times her width, as the machines'
// deal fills a member (the owner, 2026-10-04: "Do it just like the fleet, you keep
// people busy by having 2X width queued up in ready per-friend"): a card naming a
// friend goes to her while she is up and below her room, and waits ready otherwise; a
// card for any friend goes to the friend up with the most room free, the first by name
// among equals. In batch mode (the default), a friend's room is DealAhead times her width
// and her lanes are her width; in one-shot mode (docs/SPEC-SPRINT.md section 1, "A friend's card"),
// a friend's room is 1 and her lanes are 1: she gets one card at a time, straight into
// working, and the next only after the last one finished.
// Each is its next attempt's work card, created on the friend's row at generation 1, in
// working while she has a lane free (her width less her working cards; dealt and taken now:
// its deadline is the working one) and ready behind them otherwise (her finish takes the next:
// Finish), carrying the primary's fix, finding and why as a machine's deal does; its primary
// moves ready -> working. The friend's row is declared by the plan the first time she is dealt to.
func FriendDeal(s *Snapshot, cards []*Card, seats []FriendSeat) Plan {
	var p Plan
	free, lanes := map[string]int{}, map[string]int{}
	var up []string
	for _, f := range seats {
		if f.Status == Up {
			room, width := DealAhead*f.Width, f.Width
			if f.Mode == config.FriendModeOneShot {
				room, width = 1, 1
			}
			free[f.Name] = room - friendLoad(s, f.Name)
			lanes[f.Name] = width - s.Fleet.Count(FriendRow(f.Name), Working)
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
		if name == "" {
			for _, f := range up {
				if f != not && free[f] > 0 && (name == "" || free[f] > free[name]) {
					name = f
				}
			}
		}
		if name == "" || name == not || free[name] <= 0 {
			continue // no friend it may go to is up with room: it waits ready
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
		p.Units = append(p.Units, friendDealUnit(s, c, card, row, col, nil))
	}
	return Lawful(p)
}

// FriendOfClass is the friend the attempt cap's default answer deals a card past the
// cap to (brief_bound.go): one up, of one of the classes named (a friend's class is
// the class on her seat), with room — the most free width, width less the cards she
// holds, the first by name among equals. ok is false with no such friend up with room,
// and the default stays the judgment for the coordinator. The snapshot is unchanged:
// a plan that deals more than one card tracks room itself (friendWithFree).
func FriendOfClass(s *Snapshot, seats []FriendSeat, classes ...string) (string, bool) {
	free := map[string]int{}
	for _, f := range seats {
		if f.Status == Up && slices.Contains(classes, f.Class) {
			free[f.Name] = f.Width - friendLoad(s, f.Name)
		}
	}
	name := friendWithFree(seats, free, classes...)
	return name, name != ""
}

// friendWithFree is the up friend of one of the classes with the most free width in
// free, the first by name among equals; "" when none has room. AttemptCapDeal passes
// the free width it has left in this plan, decremented after each deal.
func friendWithFree(seats []FriendSeat, free map[string]int, classes ...string) string {
	var up []string
	for _, f := range seats {
		if f.Status == Up && slices.Contains(classes, f.Class) {
			up = append(up, f.Name)
		}
	}
	slices.Sort(up)
	name := ""
	for _, n := range up {
		if free[n] > 0 && (name == "" || free[n] > free[name]) {
			name = n
		}
	}
	return name
}

// friendDealUnit is one friend's card dealt: its work card on her row, in working (taken
// now) or ready behind her working cards, and its primary ready -> working on it. set
// rides the primary's move beside the deal's own fields (the attempt cap's default
// answer writes the WHO line and the count reset, brief_bound.go).
func friendDealUnit(s *Snapshot, c *Card, card, row, col string, set map[string]string) Unit {
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
	prim := map[string]string{"attempt": itoa(attempt), "work": card}
	for k, v := range set {
		prim[k] = v
	}
	return Unit{Key: c.ID, Stream: c.Row, Changes: []Change{
		change(Fleet, createEntry(card, row, col, c.Score, fields)),
		change(Work, moveEntry(c, c.Row, Working, prim, "result")),
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
