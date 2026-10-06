package sprint

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

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
// class (the tiers her nova-config row says she can do, sorted and comma joined), her
// delivery mode (Mode: batch or one-shot, default batch), her tiers (config.friends
// tiers: flash, frontier, pro; when given, the deal and the level read them before her
// class: friendTiers), Dir, her working directory when the ask writes the read brief
// itself (empty: friend sync writes it), and Running, the cards her last beat names
// running (FriendReport.Running: work card ids, job names or primaries), which the tick's
// level never moves (friendStarted).
type FriendSeat struct {
	Name    string
	Width   int
	Status  string
	Class   string
	Mode    string
	Tiers   []string
	Dir     string
	Running []string
	// Why is why her Status is not up, as FriendDownWhy says it (held, or the session
	// evidence she lacks), "" while she is up: the words a take refused for her names.
	Why string
	// Capped and Started are her lanes under the start window (StartedSeats): while
	// Capped, the deal and the level fill her row to Started lanes, the cards she has
	// started, never to her Width, which stays the ceiling.
	Capped  bool
	Started int
	// Roles is her nova-config row's roles (config.FriendRoles); a friend whose roles
	// are reader-first (readerFirst) is dealt reads before work.
	Roles []string
}

// FieldFriendsLeft is the friends a friend's work card has left, comma joined: each the
// level moved it off (FriendLevel), and the one the coordinator took it back from
// (FieldTakenFrom) once it is dealt again. Neither the deal nor the level places it on any
// of them again (docs/SPEC-SPRINT.md section 1, friend-deal-idle-lanes-first.w1), with one
// exception: a card withdrawn off a friend held or down that no other friend up may take
// is dealt back to a friend the level moved it off (never the one it was withdrawn or
// taken back from), rather than stranded ready (friendDeal, withdrawnFrom).
const FieldFriendsLeft = "friends_left"

// friendTiers is the tiers the friend can do: her Tiers, else her class's.
func friendTiers(f FriendSeat) []string {
	if len(f.Tiers) > 0 {
		return f.Tiers
	}
	return Split(f.Class)
}

// friendTakes says the friend may be given a card of the tier: it is one of her tiers
// (friendTiers), never her class as a whole. A friend whose row names no tier takes none,
// and every friend deal and move is gated on it, whatever the card's WHO line, so a
// frontier card never reaches a friend without frontier.
func friendTakes(f FriendSeat, tier string) bool {
	return slices.Contains(friendTiers(f), tier)
}

// friendsLeft is the friends the work card has left (FieldFriendsLeft), with the one it
// was taken back from while it is withdrawn.
func friendsLeft(wc *Card) []string {
	if wc == nil {
		return nil
	}
	left := Split(wc.F(FieldFriendsLeft))
	if from, ok := FriendOfRow(wc.F(FieldTakenFrom)); ok && !slices.Contains(left, from) {
		left = append(left, from)
	}
	return left
}

// withdrawnFrom is the friends a withdrawn work card must never go back to: the friend
// whose row it was withdrawn on, and the one the coordinator took it back from
// (FieldTakenFrom), never the friends the level moved it off.
func withdrawnFrom(wc *Card) []string {
	var out []string
	if f, ok := FriendOfRow(wc.Row); ok {
		out = append(out, f)
	}
	if f, ok := FriendOfRow(wc.F(FieldTakenFrom)); ok && !slices.Contains(out, f) {
		out = append(out, f)
	}
	return out
}

// friendStarted says the friend has started the work card, by the store's own data: her
// beat names it running (the card, its job or its primary), or it carries a progress stamp
// (FieldProgress). The tick's level never moves a started card.
func friendStarted(s *Snapshot, f FriendSeat, c *Card) bool {
	if c.F(FieldProgress) != "" {
		return true
	}
	job := StoredID(c.ID, s.Epoch)
	if g := c.Int("gen"); g > 1 {
		job += ".g" + itoa(g)
	}
	for _, r := range f.Running {
		if r == c.ID || r == job || (r != "" && r == c.F("primary")) {
			return true
		}
	}
	return false
}

// friendRoom is the friend's room and her lanes: DealAhead times her width and her width
// in batch mode, 1 and 1 in one-shot mode (docs/SPEC-SPRINT.md section 1, "A friend's card").
// In batch mode her width is her started lanes while the start window caps her (Capped:
// StartedSeats, cappedLanes), never more than her width.
func friendRoom(f FriendSeat) (room, width int) {
	if f.Mode == config.FriendModeOneShot {
		return 1, 1
	}
	width = f.Width
	if f.Capped {
		width = cappedLanes(f.Started, f.Width)
	}
	return DealAhead * width, width
}

// cappedLanes is a capped friend's lanes: the cards she has started, never more than her
// width, and never fewer than one while her width is above zero, so a friend who started
// none is still dealt a lane to start in and her started lanes can rise again (a card in
// it she does not start goes back at the window, as every other).
func cappedLanes(started, width int) int {
	lanes := min(started, width)
	if lanes < 1 && width > 0 {
		lanes = 1
	}
	return lanes
}

// preferredFriend is the friend of names a card goes to (docs/SPEC-SPRINT.md section 1,
// friend-deal-idle-lanes-first.w1): a friend with an idle lane (lanes > 0) before every
// friend with none, the most idle lanes first, then the most room free, then the first by
// name; "" when names is empty. The caller gives only the friends the card may go to.
func preferredFriend(names []string, lanes, free map[string]int) string {
	best := ""
	for _, f := range names {
		switch {
		case best == "":
			best = f
		case max(lanes[f], 0) != max(lanes[best], 0):
			if lanes[f] > lanes[best] {
				best = f
			}
		case free[f] != free[best]:
			if free[f] > free[best] {
				best = f
			}
		case f < best:
			best = f
		}
	}
	return best
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

// friendDeal is the tick's friend deal (TickDeal), run before the machines' deal: friends
// first (docs/SPEC-SPRINT.md, WHO preference; tla/WhoPreference.tla checks the selection,
// not the room). It offers every ready card given (in the order given, the deal's stream
// turns) to the friends up, each within her room, DealAhead times her width, as the
// machines' deal fills a member (the owner, 2026-10-04: "Do it just like the fleet, you keep
// people busy by having 2X width queued up in ready per-friend"). A card whose WHO line
// names a friend goes to her first while she is up, below her room, not one it has left,
// and her tiers hold its tier; else (or with no WHO line, or WHO: friend) it goes to a
// friend up whose tiers hold its tier (friendTakes, every friend deal's gate; a card with
// no tier is the dealer's default, flash: cardTierOf), below her room, and never one it has
// left (friendsLeft), chosen by preferredFriend: an idle lane first, the most idle lanes,
// then the most room, then by name (docs/SPEC-SPRINT.md section 1,
// friend-deal-idle-lanes-first.w1). A card no friend takes stays for the fleet's deal,
// unless it says WHO: only friend <name> (OnlyFriend), the one hard pin: it waits ready for
// her, and so does one whose friend's tiers do not hold its tier. A withdrawn attempt at its
// redeal bound at its ceiling or its attempt cap (AtRedealBound), or refused at staging by
// every member up, stays with the machines' deal and its judgment; one at its redeal bound
// below its ceiling is offered at the tier it escalates to (escalating), as the machines
// would escalate it, and a friend's deal of it is a new attempt on that tier, the bound
// attempt retired (friendEscalateUnit). A friend at or over her room is never
// dealt. In batch mode (the default), a friend's room is DealAhead times her width and
// her lanes are her width; in one-shot mode (docs/SPEC-SPRINT.md section 1, "A friend's
// card"), a friend's room is 1 and her lanes are 1: she gets one card at a time, straight
// into working, and the next only after the last one finished.
// Each is its next attempt's work card, created on the friend's row at generation 1, in
// working while she has a lane free (her width less her working cards; dealt and taken now:
// its deadline is the working one) and ready behind them otherwise (her finish takes the next:
// Finish), carrying the primary's fix, finding and why as a machine's deal does; its primary
// moves ready -> working. The friend's row is declared by the plan the first time she is dealt to.
// It answers the cards it places on each friend's row and how many of them go into
// working: the tick levels the friends after it. A named pin (WHO: friend <name>,
// not a hard pin) placed on a different friend's row carries a judgment on that
// unit (pinIgnoredNote): why she did not take it, and whose row holds the card.
// The pass keeps that judgment (pinConds) until the card is back on her row or
// leaves ready and working.
func friendDeal(s *Snapshot, cards []*Card, seats []FriendSeat) (p Plan, dealt, dealtWorking map[string]int) {
	free, lanes, seat := map[string]int{}, map[string]int{}, map[string]FriendSeat{}
	dealt, dealtWorking = map[string]int{}, map[string]int{}
	var up []string
	for _, f := range seats {
		if f.Status == Up {
			room, width := friendRoom(f)
			free[f.Name] = room - friendLoad(s, f.Name)
			lanes[f.Name] = width - s.Fleet.Count(FriendRow(f.Name), Working)
			seat[f.Name] = f
			up = append(up, f.Name)
		}
	}
	slices.Sort(up)
	// a reader-first friend is dealt reads before work: the frontier reads waiting that she
	// may be asked keep her room and her lanes for the readers' ask, which runs after the
	// deal in the tick (readsFirst)
	for _, name := range up {
		if n := readsFirst(s, seat[name]); n > 0 {
			free[name] -= n
			lanes[name] -= n
		}
	}
	// in batch mode the deal takes her ready cards first: a lane free on her row (a width
	// raised, a level or a take-back that left her working fewer than her width) is filled
	// from her oldest ready card before any new card is dealt to it, so a card on her row
	// never sits ready while she has a lane free (docs/SPEC-SPRINT.md section 1, a friend
	// takes her own ready cards; tla/FriendReadyTake.tla, FilledAfterTick); in one-shot
	// mode her daemon or her session takes it
	for _, name := range up {
		if seat[name].Mode == config.FriendModeOneShot || lanes[name] <= 0 {
			continue
		}
		row := FriendRow(name)
		ready := append([]*Card(nil), s.Fleet.Cell(row, Ready)...)
		SortCards(ready)
		for _, c := range ready[:min(lanes[name], len(ready))] {
			set, unset := friendTaken(s, c, name)
			p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{change(Fleet, moveEntry(c, row, Working, set, unset...))},
				Moved: fmt.Sprintf("%s %s:ready -> working (taken by the deal: a lane of hers was free)", c.ID, row)})
			lanes[name]--
			dealtWorking[name]++
		}
	}
	members := s.UpMembers()
	declared := map[string]bool{}
	for _, c := range cards {
		if c.Col != Ready || IsSentinel(c) {
			continue
		}
		// a card taken back from a friend (friend take) is placed again, never on her (FriendTake)
		wc := s.Fleet.Placed(WorkCardID(c.ID, c.Int("attempt")))
		if wc != nil && wc.Col != Withdrawn {
			wc = nil
		}
		// a failed attempt's judgment (at its ceiling or its attempt cap), and a staging
		// refusal by every member up, stay with the machines' deal; an attempt at its
		// redeal bound below its ceiling is offered at the tier it escalates to
		if AtRedealBound(s, c) != nil {
			continue
		}
		if held, _ := AtStagingBound(s, c, members); held != nil {
			continue
		}
		escalated := wc != nil && redealBound(wc)
		tier := cardTierOf(escalating(s, c))
		left := friendsLeft(wc)
		pinned, pinnedCard := FriendCard(c)
		leftAtPin := slices.Clone(left)
		name := pinned
		if !pinnedCard {
			name = ""
		}
		if name != "" && (free[name] <= 0 || slices.Contains(left, name) || !friendTakes(seat[name], tier)) {
			name = "" // the friend it names is not up with room, it has left her, or not her tier
		}
		if name == "" && !OnlyFriend(c) {
			var may []string
			for _, f := range up {
				if free[f] > 0 && !slices.Contains(left, f) && friendTakes(seat[f], tier) {
					may = append(may, f)
				}
			}
			if len(may) == 0 && wc != nil {
				// a card withdrawn off a friend held or down (or taken back) whom no friend
				// it has not left may take: the friends the level moved it off may have it
				// back, so it is not stranded ready while one is up with room (the owner's
				// rule: a held or down friend's cards go to the up friends' ready queues);
				// never the friend it was withdrawn from or taken back from
				gone := withdrawnFrom(wc)
				for _, f := range up {
					if free[f] > 0 && !slices.Contains(gone, f) && friendTakes(seat[f], tier) {
						may = append(may, f)
					}
				}
				left = slices.DeleteFunc(slices.Clone(left), func(f string) bool { return !slices.Contains(gone, f) })
			}
			name = preferredFriend(may, lanes, free)
		}
		if name == "" || slices.Contains(left, name) || free[name] <= 0 {
			continue // no friend it may go to is up with room: the fleet's, or (only) it waits ready
		}
		card := WorkCardID(c.ID, c.Int("attempt")+1)
		if (wc == nil || escalated) && s.Fleet.Card(card) != nil {
			p.refuse(c.ID, "work card "+card+" exists already")
			continue
		}
		free[name]--
		dealt[name]++
		col := Ready
		if lanes[name] > 0 {
			lanes[name]--
			dealtWorking[name]++
			col = Working
		}
		row := FriendRow(name)
		if !s.Fleet.HasRow(row) && !declared[row] {
			p.Rows = append(p.Rows, RowAdd{Fleet, row})
			declared[row] = true
		}
		var u Unit
		switch {
		case escalated:
			u = friendEscalateUnit(s, c, wc, card, row, col, tier)
		case wc != nil:
			u = friendRedealUnit(s, c, wc, row, col)
		default:
			u = friendDealUnit(s, c, card, row, col, nil)
		}
		if pinnedCard && pinned != "" && !OnlyFriend(c) && name != pinned {
			placedID := card
			if wc != nil && !escalated {
				placedID = wc.ID
			}
			u.Notes = append(u.Notes, pinIgnoredNote(s, c, placedID, pinned, pinSkipWhy(seats, pinned, leftAtPin, tier, free), row, col))
		}
		p.Units = append(p.Units, u)
	}
	return Lawful(p), dealt, dealtWorking
}

// friendEscalateUnit is a withdrawn attempt at its redeal bound below its ceiling dealt to
// a friend, as the machines' deal escalates it (escalate): a new attempt's work card on her
// row (friendDealUnit), its primary on the tier it escalates to (FieldTierNow), and the
// bound attempt's work card retired, its record kept.
func friendEscalateUnit(s *Snapshot, c, prev *Card, card, row, col, tier string) Unit {
	from, _ := CardTiers(c)
	u := friendDealUnit(s, c, card, row, col, map[string]string{FieldTierNow: tier})
	u.Changes = append([]Change{change(Fleet, removeEntry(prev, map[string]string{"retired": stamp(s.Now), "retired_by": "escalation"}))}, u.Changes...)
	u.Moved += fmt.Sprintf("; escalated %s -> %s: %s at its redeal bound", from, tier, prev.ID)
	return u
}

// FriendDeal is the tick's friend deal alone (friendDeal), its plan without the counts
// the level reads.
func FriendDeal(s *Snapshot, cards []*Card, seats []FriendSeat) Plan {
	p, _, _ := friendDeal(s, cards, seats)
	return p
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
	if left := friendsLeft(wc); len(left) > 0 {
		set[FieldFriendsLeft] = strings.Join(left, ",") // the friend it was taken from, kept past the take
	}
	if wc.F(FieldTakeEnded) != "" {
		// a machine's attempt whose take ended: the friend's deal is its next redeal, counted
		// against the bound as the machines' deal counts it
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

// The start window (docs/SPEC-SPRINT.md section 1, a friend is dealt what her session
// starts; deal-by-started-lanes-not-width.w2). The night of 2026-10-05 a friend at width 8
// in batch mode held seven heavy cards for six hours and started none, while she did every
// read and audit she was handed at once: her row was filled to her width, and her width was
// not what her session starts. A work card in a lane of hers (working on her row) that her
// beat does not name running (friendStarted) within the start window of its take goes back
// to the pool: it is withdrawn, taken from her (FieldTakenFrom), so the deal places it again
// on another friend or a machine, with the note "not started by <friend> in <window>; back
// to the pool". From then her row's effective width for the next deals and levels is the
// cards she has started (her started lanes, PropFriendLanes), rising again as she starts
// more, until it reaches her width and the cap comes off: her width is the ceiling, never
// the target. A hard pin to her (WHO: only friend) stays: the pool for it is her alone.
// While capped her row holds no more than her room: the ready cards past it she has not
// started, past the window of their deal, go back too, newest first. Her lanes are never
// fewer than one (cappedLanes), so a friend who started none can start again. One-shot
// mode is one card at a time already and is not capped. The model is
// tla/FriendStartWindow.tla (MCFriendStartWindow and its five reversed witnesses).

// PropFriendStartWindow is the work table's property: how long a card in a lane of a
// friend's may go unstarted before it goes back to the pool, a duration.
const PropFriendStartWindow = "friend_start_window"

// FriendStartWindowDefault is the start window when the coordinator set none.
const FriendStartWindowDefault = 20 * time.Minute

// FriendStartWindow is the start window: the sprint's setting, else
// FriendStartWindowDefault.
func (s *Snapshot) FriendStartWindow() time.Duration {
	if s.Work != nil {
		if v, ok := s.Work.Prop(PropFriendStartWindow); ok {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				return d
			}
		}
	}
	return FriendStartWindowDefault
}

// PropFriendLanes is the fleet property of a friend's started lanes while the start
// window caps her (a whole number below her width); absent or empty while she is not
// capped.
func PropFriendLanes(friend string) string { return "friend_lanes." + friend }

// PartFriendStart is the tick's start window part (TickFriendStart).
const PartFriendStart = "friend start"

// TickFriendStartParts is the tick's start window part, run once a tick after the start
// and before the tables' updates, so the pump's deal sees the cards it returned ready.
var TickFriendStartParts = []TickPartDef{{PartFriendStart, TickFriendStart}}

// friendStarts is the friend's start window read at s.Now: the work cards in her lanes
// that go back to the pool now (unstarted), and her started lanes (lanes, capped): the
// cards on her row she has started when any goes back, else her property while it holds
// fewer than she has started now, rising to that; capped is false at her width. While
// capped, unstarted also holds the ready cards past her room she has not started, past the
// window of their deal, newest first.
func friendStarts(s *Snapshot, f FriendSeat) (unstarted []*Card, lanes int, capped bool) {
	if f.Status != Up || f.Mode == config.FriendModeOneShot || s.Fleet == nil {
		return nil, 0, false
	}
	row, window, started := FriendRow(f.Name), s.FriendStartWindow(), 0
	for _, col := range []string{Ready, Working} {
		for _, c := range s.Fleet.Cell(row, col) {
			if friendStarted(s, f, c) {
				started++
				continue
			}
			if col != Working || c.F("kind") != "work" {
				continue
			}
			if pr := s.Work.Placed(c.F("primary")); pr != nil && OnlyFriend(pr) {
				continue // a hard pin is hers alone
			}
			if t := stampAt(c, "taken"); !t.IsZero() && !s.Now.Before(t.Add(window)) {
				unstarted = append(unstarted, c)
			}
		}
	}
	lanes, capped = f.Width, false
	if v, ok := s.Fleet.Prop(PropFriendLanes(f.Name)); ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			lanes, capped = max(n, started), true
		}
	}
	if len(unstarted) > 0 {
		lanes, capped = started, true
	}
	if lanes >= f.Width {
		SortCards(unstarted)
		return unstarted, f.Width, false
	}
	// capped, her row holds no more than her room: the ready cards she has not started
	// past the window of their deal, newest first, go back with the working ones, so the
	// next deals are bounded by what she starts and not by what her width once dealt her
	room := DealAhead * cappedLanes(lanes, f.Width)
	over := friendLoad(s, f.Name) - len(unstarted) - room
	ready := s.Fleet.Cell(row, Ready)
	for i := len(ready) - 1; i >= 0 && over > 0; i-- {
		c := ready[i]
		if c.F("kind") != "work" || friendStarted(s, f, c) {
			continue
		}
		if pr := s.Work.Placed(c.F("primary")); pr != nil && OnlyFriend(pr) {
			continue
		}
		if t := stampAt(c, "dealt"); !t.IsZero() && !s.Now.Before(t.Add(window)) {
			unstarted = append(unstarted, c)
			over--
		}
	}
	SortCards(unstarted)
	return unstarted, lanes, capped
}

// StartedSeats is the friends with each one's started lanes (friendStarts) on her seat:
// the tick reads them once with its friends, so its deal and its level fill her row to
// what she starts.
func StartedSeats(s *Snapshot, seats []FriendSeat) []FriendSeat {
	if s == nil || len(seats) == 0 {
		return seats
	}
	out := slices.Clone(seats)
	for i := range out {
		_, out[i].Started, out[i].Capped = friendStarts(s, out[i])
	}
	return out
}

// TickFriendStart is the start window as a part of the tick (PartFriendStart): each friend
// up's unstarted cards back to the pool, one unit each, her next deal's room lowered with
// them, and her started lanes written (PropFriendLanes), cleared when they reach her width.
func TickFriendStart(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	if s.Fleet == nil || s.Work == nil {
		return p, 0
	}
	window := shortWindow(s.FriendStartWindow())
	seats := slices.Clone(r.Friends)
	slices.SortFunc(seats, func(a, b FriendSeat) int { return strings.Compare(a.Name, b.Name) })
	for _, f := range seats {
		if f.Status != Up {
			continue
		}
		unstarted, lanes, capped := friendStarts(s, f)
		row := FriendRow(f.Name)
		why := fmt.Sprintf("not started by %s in %s; back to the pool", f.Name, window)
		for _, c := range unstarted {
			set := map[string]string{FieldTakenBack: why, FieldTakenFrom: row, "untaken_since": stamp(s.Now)}
			p.Units = append(p.Units, withdrawUnit(s, c, set, []string{"first_taken", FieldFriendDeadline}, NTakenBack, r.who(), why))
		}
		name := PropFriendLanes(f.Name)
		was, had := s.Fleet.Prop(name)
		value := ""
		if capped {
			value = itoa(lanes)
		}
		if (had && was != value) || (!had && value != "") {
			p.Props = append(p.Props, PropWrite{Table: Fleet, Name: name, Value: value, Was: was, WasAbsent: !had})
		}
	}
	return p, 0
}

// shortWindow is a duration as a setting says it: 20m, 1h30m, 45s.
func shortWindow(d time.Duration) string {
	t := d.Round(time.Second).String()
	if strings.HasSuffix(t, "m0s") {
		t = strings.TrimSuffix(t, "0s")
	}
	if strings.HasSuffix(t, "h0m") {
		t = strings.TrimSuffix(t, "0m")
	}
	return t
}

// readerFirst says the friend's roles are reader-first: they name reader, and name no
// builder or name reader before it.
func readerFirst(f FriendSeat) bool {
	r, b := slices.Index(f.Roles, "reader"), slices.Index(f.Roles, "builder")
	return r >= 0 && (b < 0 || r < b)
}

// readsFirst is how many frontier reads in review wait that a reader-first friend up of
// frontier tier may be asked (friendReadAsk: not asked at the attempt, and never of her):
// the room and lanes the deal keeps for them; 0 for any other friend.
func readsFirst(s *Snapshot, f FriendSeat) int {
	if !readerFirst(f) || !slices.Contains(friendTiers(f), cardhdr.RouteFrontier) || s.Work == nil || s.Fleet == nil {
		return 0
	}
	n := 0
	for _, pr := range s.Work.Column(Review) {
		if !friendReadCard(s, pr) || IsSentinel(pr) || pr.F("result") == "failed" {
			continue
		}
		attempt := max(pr.Int("attempt"), 1)
		if attemptReadAsked(s, pr, attempt) || s.Fleet.Card(ReadCardID(pr.ID, attempt, f.Name)) != nil {
			continue
		}
		n++
	}
	return n
}
