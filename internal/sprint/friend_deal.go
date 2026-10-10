package sprint

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// A friend's card (the owner, 2026-10-03: "Could we try expressing the work left for
// nova-tools-1.1.0 into cards, and doing it via the sprint, but doing parts on friends
// where we would normally do friend work."; docs/SPEC-SPRINT.md section 1, a friend's
// card). A brief whose header carries `WHO: friend` (any friend) or `WHO: friend <name>`
// (cardhdr.ReadWho) is dealt by the tick to a friend instead of a machine: its work card
// is placed on the friend's own fleet row, FriendRow(<name>), ready (friend sync delivers
// it into her inbox) and working once she starts it (friend_start.go), within her room,
// and is finished from her outbox by friend sync. The friend's row is a fleet row no machine
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

// HardPin is the explicit hard pin (docs/SPEC-SPRINT.md, WHO preference): the brief's
// WHO line is `only.friend.<name>` (WHO: friend <name> only, or WHO: only friend <name>).
// It is the one pin never waived: the card waits ready for her alone.
func HardPin(c *Card) bool {
	return strings.HasPrefix(c.F(FieldWho), "only.friend.")
}

// pinWaived says the deal has waived the card's pin (FieldPinWaived): it is dealt as
// unpinned until a deal places it back on her row.
func pinWaived(c *Card) bool { return c.F(FieldPinWaived) != "" }

// OnlyFriend says the card waits for the friend its WHO line names alone, as the store
// holds it now: the hard pin, or a card come back to her (ReworkPinned) whose pin the
// deal has not waived. The pin clock reads through it (pinFateFor, pinHolds): a
// come-back pin is soft, waived once the clock runs.
func OnlyFriend(c *Card) bool {
	return HardPin(c) || (ReworkPinned(c) && !pinWaived(c))
}

// ReworkPinned says the card names a friend (WHO: friend <name>) and has come back by a
// rework, a return or a redo (its reworks or returns counted): its next attempt is hers
// first, never another friend or a machine (the owner, 2026-10-05: a rework of a
// friend's own rating was dealt to another worker, who could not do it as her) — up to
// the pin bound (PinWait, FieldPinSince): past it the pin is waived like any other
// named preference. A take-back alone (friend take) counts neither, so a preference
// taken back from her is offered on.
func ReworkPinned(c *Card) bool {
	if c.Int("reworks") == 0 && c.Int("returns") == 0 {
		return false
	}
	_, named := FriendOfRow(c.F(FieldWho))
	return named
}

// friendCardWhy is why the machines' deal leaves a card for the friend its WHO line
// names: the tick deals it to her up with room, never to a machine, while the hard pin
// or a come-back pin inside its clock holds it (pinHolds).
const friendCardWhy = "a friend's card (its brief says WHO: friend <name> only, or a WHO: friend <name> card come back by a rework waits for her inside the pin bound, nova-sprint set --pin-wait): the tick deals it to that friend up with room, never to a machine"

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
	Name   string
	Width  int
	Status string
	Class  string
	Mode   string
	Tiers  []string
	// Roles is her nova-config row's roles: a read card is dealt only to a friend whose
	// roles name reader (RoleReader, read_cards.go).
	Roles []string
	Dir   string
	// Streams and Kinds are her work restriction (her nova-config row's streams and
	// kinds, carried by friend sync): the stream globs and card KIND values the deal
	// may hand her; empty is no restriction (docs/SPEC-SPRINT.md section 1, a friend's
	// card).
	Streams []string
	Kinds   []string
	Running []string
	// Why is why her Status is not up, as FriendDownWhy says it (held, or the session
	// evidence she lacks), "" while she is up: the words a take refused for her names.
	Why string
	// Active is the newest write under her working directory as her daemon's last beat
	// found it (FriendReport.Active), zero when it said none: a friend who wrote within the
	// start bound is at work, and the tick moves none of her cards for want of a start
	// (friendUnstartedLevel).
	Active time.Time
	// Proof is her session's last proof as her beat carries it (Beat.Proof: a SESSION CHECK
	// it answered, or a bus message of its own), and Finished the store's record of her
	// last finish, working to done; each zero when there is none. The stall ladder reads
	// them as evidence of her work (FriendWorked).
	Proof    time.Time
	Finished time.Time
	// Since is when her status became down or held, when the caller knows it.
	// Zero leaves the pin clock to her proof, her control card, or unknown.
	Since time.Time
	// Answered is when her session last answered the coordinator's wake ping (her
	// FriendHealth observation, up), zero when it has not.
	Answered time.Time
	// ReadsFirst is the room her reads take before any work card in this deal (reads are a
	// card priority, reads_priority.go friendReadsFirst): set by the tick's deal on the
	// seats it deals work to, never read from her row; zero leaves her room as it is.
	ReadsFirst int
	// Beat, Evidence, DaemonOnly and Current are what the status transitions read
	// (StatusTransitions, judgments_status.go): her last beat (zero when she never beat),
	// what her status rests on (FriendEvidence), whether she is down while the coordinator's
	// last observation is her daemon's pong alone (DaemonPong: her daemon answers, her
	// session does not), and the build the server runs, which her daemon's
	// (Beat.Friend.Build) is compared with.
	Beat       Beat
	Evidence   string
	DaemonOnly bool
	Current    string
}

// FieldFriendsLeft is the friends a friend's work card has left, comma joined: each the
// level moved it off (FriendLevel), and the one the coordinator took it back from
// (FieldTakenFrom) once it is dealt again. Neither the deal nor the level places it on any
// of them again (docs/SPEC-SPRINT.md section 1, friend-deal-idle-lanes-first.w1), with one
// exception: a card withdrawn off a friend held or down that no other friend up may take
// is dealt back to a friend the level moved it off (never the one it was withdrawn or
// taken back from), rather than stranded ready (friendDealPass, withdrawnFrom).
const FieldFriendsLeft = "friends_left"

// friendTiers is the tiers the friend can do: her Tiers, else her class's.
func friendTiers(f FriendSeat) []string {
	if len(f.Tiers) > 0 {
		return f.Tiers
	}
	return Split(f.Class)
}

// friendDealable says the deal may fill the friend's row (docs/SPEC-SPRINT.md section 1, a
// friend's card): her status is up by the friends' rule (FriendStatus: her session's
// evidence, never a beat alone; not held, not down), the coordinator's row of
// her (her fleet control card) says neither down nor held, and the stall ladder has not
// marked her down (PropFriendStallDown: released only by her activity). On 2026-10-06 a
// friend whose row read down, her lanes paused and her daemon beating, was dealt 18 cards
// twice; a row that cannot work is filled by no deal. While the friends' work is off
// (FriendsOff, nova-sprint set --friends off) no friend's row is.
func friendDealable(s *Snapshot, f FriendSeat) bool {
	return !s.FriendsOff() && friendCanRead(s, f)
}

// friendCanRead says the friend's row may be dealt a read card: friendDealable but for the
// friends' work switch, which stops her work and never her reads (set --friends off: "her
// reads still flow"; read_cards.go).
func friendCanRead(s *Snapshot, f FriendSeat) bool {
	if f.Status != Up {
		return false
	}
	if s == nil || s.Fleet == nil {
		return true
	}
	row := FriendRow(f.Name)
	if ctl := s.MemberCtl(row); ctl != nil {
		if st := ctl.F("status"); st == Down || st == Held || ctl.F("held") != "" {
			return false
		}
	}
	if down, _ := s.Fleet.Prop(PropFriendStallDown(f.Name)); down != "" {
		return false
	}
	return true
}

// friendTakes says the friend may be given a card of the tier: it is one of her tiers
// (friendTiers), never her class as a whole, and the friends' tiers hold it (FriendsTake,
// set --friends-tiers). A friend whose row names no tier takes none, and every friend
// deal and move is gated on it, whatever the card's WHO line, so a frontier card never
// reaches a friend without frontier.
func friendTakes(s *Snapshot, f FriendSeat, tier string) bool {
	return slices.Contains(friendTiers(f), tier) && s.FriendsTake(tier)
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

// friendsFor is the friends up (friendDealable) whose tiers hold the tier who may still be
// dealt the primary c: never one its withdrawn attempt was withdrawn or taken back from
// (withdrawnFrom), as the friends' deal places it (friendDealPass). None, while friends
// alone serve the tier (tierServed), is a card no worker is left for: the tick's judgment
// of the tier names it (TickDeal).
func (s *Snapshot) friendsFor(c *Card, tier string) []FriendSeat {
	var gone []string
	if wc := s.Fleet.Placed(WorkCardID(c.ID, c.Int("attempt"))); wc != nil && wc.Col == Withdrawn {
		gone = withdrawnFrom(wc)
	}
	var out []FriendSeat
	for _, f := range s.Friends {
		if friendDealable(s, f) && friendTakes(s, f, tier) && !slices.Contains(gone, f.Name) {
			out = append(out, f)
		}
	}
	return out
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

// laneRunsIt is the friend whose last beat names the primary c running, by its own id, its
// current attempt's work card, or the withdrawn work card wc and its job; "" when no beat
// does. The deal never places a card on a second row while a lane runs it
// (docs/SPEC-SPRINT.md section 1, one lane per card): a card taken back or handed back
// while her lane still runs it waits ready until her beat stops naming it.
func laneRunsIt(s *Snapshot, seats []FriendSeat, c, wc *Card) string {
	names := []string{c.ID, WorkCardID(c.ID, c.Int("attempt"))}
	if wc != nil {
		job := StoredID(wc.ID, s.Epoch)
		if g := wc.Int("gen"); g > 1 {
			job += ".g" + itoa(g)
		}
		names = append(names, wc.ID, job)
	}
	for _, f := range seats {
		for _, r := range f.Running {
			if r != "" && slices.Contains(names, r) {
				return f.Name
			}
		}
	}
	return ""
}

// friendRoom is the friend's room and her lanes: DealAhead times her width and her width
// in batch mode, 1 and 1 in one-shot mode (docs/SPEC-SPRINT.md section 1, "A friend's card"),
// the room less what her reads take first in this deal (FriendSeat.ReadsFirst).
func friendRoom(f FriendSeat) (room, width int) {
	if f.Mode == config.FriendModeOneShot {
		return 1 - f.ReadsFirst, 1
	}
	return DealAhead*f.Width - f.ReadsFirst, f.Width
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

// friendLoad is the cards a friend holds: ready and working on her row, her work cards and
// her reads together. One width bounds her row (docs/SPEC-SPRINT.md section 1, a friend's
// card; the owner's rule): her reads hold her lanes and her room as her work does, and a
// one-shot friend holds one card at a time, read or work.
//
// While read cards are on (read_cards.go) a read holds half a slot of her width, as a
// member's does (halfLoad): her row holds her width of work or twice it of reads.
func friendLoad(s *Snapshot, name string) int {
	row := FriendRow(name)
	if s.ReadCardsOn() {
		return halfLoad(rowLoad(s, row))
	}
	return s.Fleet.Count(row, Ready) + s.Fleet.Count(row, Working)
}

// friendDealPass is the tick's friend deal (TickDeal), run before the machines' deal: friends
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
// unless it says WHO: friend <name> only (HardPin, the one hard pin): it waits ready for
// her, and so does one whose friend's tiers do not hold its tier. A card come back to the
// friend its WHO line names (ReworkPinned) is a soft pin with the clock: it waits ready
// for her inside the pin bound (pin_since, PinWait) and is waived past it, dealt on as
// unpinned. A card a friend's beat
// names running (laneRunsIt) is placed on no row while it does. A withdrawn attempt at its
// redeal bound at its ceiling or its attempt cap (AtRedealBound), or refused at staging by
// every member up, stays with the machines' deal and its judgment; one at its redeal bound
// below its ceiling is offered at the tier it escalates to (escalating), as the machines
// would escalate it, and a friend's deal of it is a new attempt on that tier, the bound
// attempt retired (friendEscalateUnit). A friend at or over her room is never
// dealt. In batch mode (the default), a friend's room is DealAhead times her width and
// her lanes are her width; in one-shot mode (docs/SPEC-SPRINT.md section 1, "A friend's
// card"), a friend's room is 1 and her lanes are 1: she gets one card at a time, and the
// next only after the last one finished. A lane of hers is idle while no card on her row
// holds it, started or not.
// Each is its next attempt's work card, created on the friend's row at generation 1, ready
// whatever her lanes, untaken and with no deadline running (docs/SPEC-SPRINT.md section 1,
// a friend's card is working once she starts it), carrying the primary's fix, finding and
// why as a machine's deal does; its primary moves ready -> working. Before it deals, the
// pass moves each card on a friend's row that she has started into working, its deadline
// from then (friendStartUnits). The friend's row is declared by the plan the first time she
// is dealt to. It answers the cards it places on each friend's row and how many cards on it
// go into working on her start: the tick levels the friends after it. A named pin (WHO: friend <name>,
// not a hard pin) placed on a different friend's row carries a judgment on that
// unit (pinIgnoredNote): why she did not take it, and whose row holds the card.
// The pass keeps that judgment (pinConds) until the card is back on her row or
// leaves ready and working.
// With reclaim set it also reclaims the fleet's dealt-ahead cards (friendReclaim): a deal in
// passes (a pass of the cards above reads, then the reads, then the rest) reclaims once, in
// its last pass, after the reads.
func friendDealPass(s *Snapshot, cards []*Card, seats []FriendSeat, reclaim bool) (p Plan, dealt, dealtWorking map[string]int) {
	free, lanes, seat := map[string]int{}, map[string]int{}, map[string]FriendSeat{}
	dealt, dealtWorking = map[string]int{}, map[string]int{}
	fullAtStart := map[string]bool{}
	var up []string
	for _, f := range seats {
		if friendDealable(s, f) {
			room, width := friendRoom(f)
			free[f.Name] = room - friendLoad(s, f.Name)
			if room > 0 && free[f.Name] <= 0 {
				fullAtStart[f.Name] = true // full before this pass, not by a card it places
			}
			// a lane is idle while no card on her row holds it, started or not
			lanes[f.Name] = width - friendLoad(s, f.Name)
			seat[f.Name] = f
			up = append(up, f.Name)
		}
	}
	slices.Sort(up)
	// her start receipts first: a card ready on her row that she has started goes to working,
	// its deadline from now (friendStartUnits; docs/SPEC-SPRINT.md section 1, a friend's card
	// is working once she starts it), in batch mode and in one-shot mode alike
	// and working on her row means started: a card her finish's next or a take-back's next
	// moved there with no start of hers goes back ready (friendUnstartedWorking)
	p.Units = append(p.Units, friendUnstartedWorking(s, seats)...)
	starts, started := friendStartUnits(s, seats)
	p.Units = append(p.Units, starts...)
	maps.Copy(dealtWorking, started)
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
		if laneRunsIt(s, seats, c, wc) != "" {
			continue // one live lane per card: no second row while her lane runs it
		}
		escalated := wc != nil && redealBound(wc)
		tier := s.DealTier(escalating(s, c))
		left := friendsLeft(wc)
		pinned, pinnedCard := FriendCard(c)
		leftAtPin := slices.Clone(left)
		name := pinned
		if !pinnedCard {
			name = ""
		}
		structural := func(n string) bool {
			return free[n] <= 0 || slices.Contains(left, n) || !friendTakes(s, seat[n], tier) || !friendRestrictionAllows(seat[n], c)
		}
		var fate pinFate
		hard := HardPin(c)
		if name != "" && !hard && len(seats) > 0 {
			fate = pinFateFor(s, c, seats, name, fullAtStart)
			switch {
			case fate.hold:
				if fate.start {
					// a pin whose friend is held or full (or a come-back pin down)
					// and the sprint cannot time: start the clock on the card and
					// keep it for her this tick
					p.Units = append(p.Units, pinClockUnit(s, c, name, fate.why))
				}
				continue // inside the pin wait: she keeps it, the fleet does not
			case fate.waive:
				name = "" // past the bound, or the sprint cannot time it: dealt on, who stays
			case structural(name):
				if ReworkPinned(c) {
					// a come-back pin she can never honour (left her, tiers, restriction,
					// no room at all) is waived at once, with the reason, not held for a
					// clock that cannot run
					fate = pinFate{why: pinSkipWhy(s, seats, name, left, tier, free), waive: true}
				}
				name = "" // wrong tier, left her, filled during this pass, or no room at all
			}
		} else if name != "" && structural(name) {
			name = "" // a hard pin, or a deal with no roster: the old rule, no clock
		}
		if name == "" && (!OnlyFriend(c) || fate.waive) {
			var may []string
			for _, f := range up {
				if free[f] > 0 && !slices.Contains(left, f) && friendTakes(s, seat[f], tier) && friendRestrictionAllows(seat[f], c) {
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
					if free[f] > 0 && !slices.Contains(gone, f) && friendTakes(s, seat[f], tier) && friendRestrictionAllows(seat[f], c) {
						may = append(may, f)
					}
				}
				left = slices.DeleteFunc(slices.Clone(left), func(f string) bool { return !slices.Contains(gone, f) })
			}
			name = preferredFriend(may, lanes, free)
		}
		if name == "" || slices.Contains(left, name) || free[name] <= 0 || !friendRestrictionAllows(seat[name], c) {
			if name == "" && fate.waive && ReworkPinned(c) && c.F(FieldPinSince) == "" {
				// a come-back pin she can never honour and no friend may take: waive it on
				// the card now, so the machines' deal takes it (pinHolds), never a hole
				u := Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, setEntry(c, nil))}}
				u.Moved = pinWaivedLine(pinned, fate.d, fate.why, "the fleet")
				stampPinWaived(&u, c, s.Now)
				p.Units = append(p.Units, u)
			}
			continue // no friend it may go to is up with room: the fleet's, or (only) it waits ready
		}
		card := WorkCardID(c.ID, c.Int("attempt")+1)
		if (wc == nil || escalated) && s.Fleet.Card(card) != nil {
			p.refuse(c.ID, "work card "+card+" exists already")
			continue
		}
		free[name]--
		dealt[name]++
		lanes[name]--
		row := FriendRow(name)
		if !s.Fleet.HasRow(row) && !declared[row] {
			p.Rows = append(p.Rows, RowAdd{Fleet, row})
			declared[row] = true
		}
		var u Unit
		switch {
		case escalated:
			u = friendEscalateUnit(s, c, wc, card, row, tier)
		case wc != nil:
			u = friendRedealUnit(s, c, wc, row, tierNowSet(c, tier))
		default:
			u = friendDealUnit(s, c, card, row, Ready, tierNowSet(c, tier))
		}
		if pinnedCard && pinned != "" && !OnlyFriend(c) && name != pinned {
			placedID := card
			if wc != nil && !escalated {
				placedID = wc.ID
			}
			u.Notes = append(u.Notes, pinIgnoredNote(s, c, placedID, pinned, pinSkipWhy(s, seats, pinned, leftAtPin, tier, free), row, Ready))
		}
		if fate.waive && name != pinned {
			u.Moved += "; " + pinWaivedLine(pinned, fate.d, fate.why, row)
			stampPinWaived(&u, c, s.Now)
		} else if name == pinned {
			clearPinWaived(&u, c)
		}
		p.Units = append(p.Units, u)
	}
	// then her idle lanes take what the fleet dealt ahead and no lane has taken yet
	if reclaim {
		p.Units = append(p.Units, friendReclaim(s, seats, up, seat, free, lanes, dealt, declared, &p)...)
	}
	return Lawful(p), dealt, dealtWorking
}

// FriendsDealtFleet is each friend's count of the fleet's cards she holds (where --json
// dealt_fleet; docs/SPEC-SPRINT.md section 1, a friend's card): the work cards on her row,
// ready and working, whose primary carries no WHO line (a bare WHO: friend, a named and a
// hard pin are friends' cards). A friend with none is absent.
func FriendsDealtFleet(s *Snapshot) map[string]int {
	out := map[string]int{}
	if s == nil || s.Fleet == nil || s.Work == nil {
		return out
	}
	for _, row := range s.Fleet.Rows() {
		name, ok := FriendOfRow(row)
		if !ok {
			continue
		}
		for _, col := range []string{Ready, Working} {
			for _, wc := range s.Fleet.Cell(row, col) {
				if wc.F("kind") != "work" {
					continue
				}
				// a WHO line of any friend, the bare WHO: friend too, makes it a friend's card
				if pr := s.Work.Card(wc.F("primary")); pr != nil && pr.F(FieldWho) == "" {
					out[name]++
				}
			}
		}
	}
	return out
}

// tierNowSet is the primary's tier field a friend's first deal of it writes: the tier the
// deal drew (dealTierOf), as a machine's deal writes it, so its reads and its escalation
// go by the tier she ran it on; none for a pinned tier, whose ceiling is its tier.
func tierNowSet(c *Card, tier string) map[string]string {
	m, _ := cardhdr.ReadModel(c.F("brief"))
	if tier == "" || pinnedTier(c, m) || c.F(FieldTierNow) != "" {
		return nil
	}
	return map[string]string{FieldTierNow: tier}
}

// friendReclaim is the friends' reclaim of the fleet's dealt-ahead cards (docs/SPEC-SPRINT.md
// section 1, a friend's card): a work card the machines' deal placed ready on a machine row
// ahead of its lanes, which no lane has taken (it is still ready there), goes to a friend up
// with an idle lane and room whose tiers hold the tier it was dealt on (dealTierOf), never
// one it has left, its friend chosen as the deal chooses (preferredFriend: the most idle
// lanes, then the most room, then by name; the friend its WHO line names first). It moves at
// its next generation, its fleet route off (a friend runs her own model), ready on her row
// until she starts it; its primary stays working on it, the tier it was dealt on written
// when it names none (tierNowSet). The friend deal runs before the machines' deal each
// tick, so a ready card goes to a friend first and a card dealt ahead to a machine comes
// back to her while her lanes are idle; a machine's take of the old generation is refused.
// It is bounded each tick: a friend reclaims at most her idle lanes, and a machine gives at
// most half its dealt-ahead queue (rounded down), the machines taking turns in row order,
// so no friend drains the fleet's queues in one tick. A held stream, a bench card, a hard
// pin and a card whose route rests (cardRest: the same plan withdraws it,
// restWithdrawals, and a card moved to two places refuses the whole tick) stay where they
// are. A reclaimed card she does not start goes the way of any card of hers: the start
// bound's level or a take-back, after which the machines' deal may place it again.
func friendReclaim(s *Snapshot, seats []FriendSeat, up []string, seat map[string]FriendSeat, free, lanes, dealt map[string]int, declared map[string]bool, p *Plan) []Unit {
	idle := false
	for _, f := range up {
		idle = idle || (lanes[f] > 0 && free[f] > 0)
	}
	if !idle {
		return nil
	}
	var machines []string
	queue, quota := map[string][]*Card{}, map[string]int{}
	for _, m := range s.Members() {
		var prims []*Card
		byPrimary := map[string]*Card{}
		ahead := 0
		for _, wc := range s.Fleet.Cell(m, Ready) {
			if wc.F("kind") != "work" {
				continue // ready on a machine row: no lane has taken it (a take moves it to working)
			}
			ahead++
			pr := s.Work.Placed(wc.F("primary"))
			if pr == nil || pr.Col != Working || pr.F("work") != wc.ID || IsSentinel(pr) || OnlyFriend(pr) ||
				StreamHeld(s, pr.Row) || len(Bench(pr)) > 0 || laneRunsIt(s, seats, pr, wc) != "" {
				continue
			}
			if _, rests := cardRest(s, wc); rests {
				continue // its route rests: the same plan withdraws it (restWithdrawals)
			}
			byPrimary[pr.ID] = wc
			prims = append(prims, pr)
		}
		if quota[m] = ahead / 2; quota[m] == 0 || len(prims) == 0 {
			continue
		}
		for _, pr := range dealOrder(s, prims) {
			queue[m] = append(queue[m], byPrimary[pr.ID])
		}
		machines = append(machines, m)
	}
	var units []Unit
	// one card from each machine in turn, each machine's in the deal order, until no friend
	// may take a card any machine has left to give
	for moved := true; moved; {
		moved = false
		for _, m := range machines {
			for quota[m] > 0 && len(queue[m]) > 0 {
				wc := queue[m][0]
				queue[m] = queue[m][1:]
				u, ok := reclaimUnit(s, wc, up, seat, free, lanes, dealt, declared, p)
				if ok {
					units = append(units, u)
					quota[m]--
					moved = true
					break
				}
			}
		}
	}
	return units
}

// reclaimUnit is the dealt-ahead work card wc moved to the friend the deal would choose for
// it (friendReclaim), her room, lanes and count taken; false when no friend up may take it.
func reclaimUnit(s *Snapshot, wc *Card, up []string, seat map[string]FriendSeat, free, lanes, dealt map[string]int, declared map[string]bool, p *Plan) (Unit, bool) {
	pr := s.Work.Placed(wc.F("primary"))
	tier, left := s.DealTier(pr), friendsLeft(wc)
	var may []string
	for _, f := range up {
		if lanes[f] > 0 && free[f] > 0 && !slices.Contains(left, f) && friendTakes(s, seat[f], tier) && friendRestrictionAllows(seat[f], pr) {
			may = append(may, f)
		}
	}
	name := preferredFriend(may, lanes, free)
	if pinned, ok := FriendCard(pr); ok && pinned != "" && slices.Contains(may, pinned) {
		name = pinned
	}
	if name == "" {
		return Unit{}, false
	}
	free[name]--
	lanes[name]--
	dealt[name]++
	row := FriendRow(name)
	if !s.Fleet.HasRow(row) && !declared[row] {
		p.Rows = append(p.Rows, RowAdd{Fleet, row})
		declared[row] = true
	}
	set := nextGen(wc, row, s.Now)
	unset := []string{FieldRoute, FieldModel, FieldTokens, FieldUSD, FieldHarness, FieldDeadline, FieldFriendDeadline}
	changes := []Change{change(Fleet, moveEntry(wc, row, Ready, set, unset...))}
	prim := tierNowSet(pr, tier)
	var clear []string
	if pinned, ok := FriendCard(pr); ok && pinned == name && pr.Has(FieldPinWaived) {
		clear = []string{FieldPinWaived, FieldPinSince} // back on the preferred friend: the waiver and the clock end
	}
	if prim != nil || len(clear) > 0 {
		changes = append(changes, change(Work, setEntry(pr, prim, clear...)))
	}
	return Unit{Key: pr.ID, Stream: pr.Row, Changes: changes,
		Moved: fmt.Sprintf("%s %s:ready -> %s:%s gen=%d (reclaimed: dealt ahead to %s and untaken; her idle lane takes it, friend sync delivers it to her inbox)", wc.ID, wc.Row, row, Ready, wc.Int("gen")+1, wc.Row)}, true
}

// friendEscalateUnit is a withdrawn attempt at its redeal bound below its ceiling dealt to
// a friend, as the machines' deal escalates it (escalate): a new attempt's work card on her
// row (friendDealUnit), its primary on the tier it escalates to (FieldTierNow), and the
// bound attempt's work card retired, its record kept.
func friendEscalateUnit(s *Snapshot, c, prev *Card, card, row, tier string) Unit {
	from, _ := CardTiers(c)
	u := friendDealUnit(s, c, card, row, Ready, map[string]string{FieldTierNow: tier})
	u.Changes = append([]Change{change(Fleet, removeEntry(prev, map[string]string{"retired": stamp(s.Now), "retired_by": "escalation"}))}, u.Changes...)
	u.Moved += fmt.Sprintf("; escalated %s -> %s: %s at its redeal bound", from, tier, prev.ID)
	return u
}

// friendWithFree is the up friend of one of the classes with the most free width in
// free, the first by name among equals, whose work restriction the card c is within;
// "" when none has room. AttemptCapDeal passes the free width it has left in this plan,
// decremented after each deal.
func friendWithFree(seats []FriendSeat, free map[string]int, c *Card, classes ...string) string {
	var up []string
	for _, f := range seats {
		if f.Status == Up && slices.Contains(classes, f.Class) && friendRestrictionAllows(f, c) {
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

// friendDealUnit is one friend's card dealt: its work card on her row, ready whatever her
// lanes, untaken and with no deadline running until she starts it (friendStartUnits;
// docs/SPEC-SPRINT.md section 1, a friend's card is working once she starts it), and its
// primary ready -> working on it. The column argument is not read: every friend's deal is
// ready (the attempt cap's deal, brief_bound.go, still passes one). set rides the primary's
// move beside the deal's own fields (the attempt cap's default answer writes the WHO line
// and the count reset, brief_bound.go).
func friendDealUnit(s *Snapshot, c *Card, card, row, _ string, set map[string]string) Unit {
	attempt := c.Int("attempt") + 1
	now := stamp(s.Now)
	fields := map[string]string{"kind": "work", "primary": c.ID, "stream": c.Row, "attempt": itoa(attempt), "gen": "1", "member": row,
		"dealt": now, "first_dealt": now, "untaken_since": now}
	for _, k := range []string{"fix", "finding", "why"} {
		if v := c.F(k); v != "" {
			fields[k] = v
		}
	}
	priorityOnWork(fields, c)
	prim := map[string]string{"attempt": itoa(attempt), "work": card}
	for k, v := range set {
		prim[k] = v
	}
	return Unit{Key: c.ID, Stream: c.Row, Changes: []Change{
		change(Fleet, createEntry(card, row, Ready, c.Score, fields)),
		change(Work, moveEntry(c, c.Row, Working, prim, "result")),
	}, Moved: fmt.Sprintf("%s work %s -> working card=%s member=%s %s (a friend's card: friend sync delivers it to her inbox)", c.ID, c.Col, card, row, Ready)}
}

// friendRedealUnit is a friend's card taken back (FriendTake) placed again: the same work
// card, withdrawn, on her row at its next generation (its own branch, and its own job:
// friendJobOf), ready until she starts it (friendStartUnits), and its primary ready ->
// working on it, its attempt as it was: a take-back is no attempt and spends no bound.
func friendRedealUnit(s *Snapshot, c, wc *Card, row string, set map[string]string) Unit {
	// a card the fleet held before carries its fleet route; a friend runs her own model, so
	// the route comes off as a first deal to her writes none (2026-10-04: a resting route
	// kept on a friend's card withdrew it every tick, and each deal again was a new inbox copy)
	// set rides the primary's move: the tier the deal drew when it names none (tierNowSet)
	prim := map[string]string{"work": wc.ID}
	maps.Copy(prim, set)
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
	unset = append(unset, FieldFriendDeadline) // set when she starts it
	return Unit{Key: c.ID, Stream: c.Row, Changes: []Change{
		change(Fleet, moveEntry(wc, row, Ready, set, unset...)),
		change(Work, moveEntry(c, c.Row, Working, prim, "result")),
	}, Moved: fmt.Sprintf("%s work %s -> working card=%s member=%s gen=%d %s (taken back, dealt again: friend sync delivers it to her inbox)", c.ID, c.Col, wc.ID, row, wc.Int("gen")+1, Ready)}
}

// FriendRestrictionWhy applies one friend's configured stream globs and KIND values to a
// card's stream and kind: "" when the card is within the restriction (either list empty is
// no restriction, Split reads the comma lists nova-config carries), else the sentence the
// add, the brief and the deal refuse or skip with (docs/SPEC-SPRINT.md section 1, a
// friend's card).
func FriendRestrictionWhy(streams, kinds []string, stream, kind string) string {
	if len(streams) > 0 {
		matched := false
		for _, glob := range streams {
			if ok, err := path.Match(strings.TrimSpace(glob), stream); err == nil && ok {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Sprintf("stream %q is outside this friend's streams restriction (%s)", stream, strings.Join(streams, ","))
		}
	}
	if len(kinds) > 0 {
		for _, allowed := range kinds {
			if strings.TrimSpace(allowed) == kind {
				return ""
			}
		}
		return fmt.Sprintf("KIND %q is outside this friend's kinds restriction (%s)", kind, strings.Join(kinds, ","))
	}
	return ""
}

// friendRestrictionAllows says the deal may hand the card c to the seat: its stream and
// KIND are within her configured restriction (FriendRestrictionWhy). A nil card is no
// restriction to judge.
func friendRestrictionAllows(seat FriendSeat, c *Card) bool {
	if c == nil {
		return true
	}
	return FriendRestrictionWhy(seat.Streams, seat.Kinds, c.F("stream"), BriefKind(c.F("brief"))) == ""
}

// BriefKind reads the task kind from a card brief's typed header (the KIND: line the card
// lint reads): "" when it names none, and a KIND: line in the body grants nothing.
func BriefKind(brief string) string {
	value, ok := swarm.CardHeaderValue([]byte(brief), "KIND")
	if !ok {
		return ""
	}
	return value
}

// FieldPinWaived is the stamp on a primary whose named WHO preference was waived
// after the pin wait (or because the sprint could not time the absence). FieldWho
// stays, so she is still preferred. FieldPinSince is an optional card-local clock
// the deal reads and does not write: a test, or a later pass, may set it.
const (
	FieldPinWaived = "pin_waived"
	FieldPinSince  = "pin_since"
)

// pinFate is what the pin clock says about one named soft pin.
type pinFate struct {
	why   string
	d     time.Duration
	hold  bool
	waive bool
	// start says the deal must stamp the clock (FieldPinSince): a pin whose friend is
	// held or full (or a come-back pin down) with an age the sprint cannot tell. It
	// waits this tick and the bound runs from the stamp.
	start bool
}

// pinWaits says the tick must leave this ready card for its preferred friend:
// she is down, held, or already full, and that has lasted no longer than PinWait.
// An empty roster is no clock (the deal verb with no friends). A hard pin waits
// by OnlyFriend, not by this.
func pinWaits(s *Snapshot, c *Card, seats []FriendSeat) bool {
	name, ok := FriendCard(c)
	if !ok || name == "" {
		return false
	}
	return pinFateFor(s, c, seats, name, pinFullAtStart(s, seats)).hold
}

// pinClockRun says the pin clock (FieldPinSince) has run its bound: the deal first
// found her unable PinWait or more ago.
func pinClockRun(s *Snapshot, c *Card) bool {
	if c == nil {
		return false
	}
	t, ok := parseStamp(c.F(FieldPinSince))
	return ok && !s.Now.Before(t.Add(s.PinWait()))
}

// pinHolds says the machines' deal and the tick leave this card to the friend its WHO
// line names now: any named pin inside its clock (pinWaits), a hard pin always, and a
// card come back to her (ReworkPinned) whose started clock has not run. The clock's
// field is the record, so the first tick after a come-back holds even when the machines'
// deal runs with no roster; once the clock runs, or the deal waives the pin, the card is
// dealt as unpinned.
func pinHolds(s *Snapshot, c *Card, seats []FriendSeat) bool {
	if HardPin(c) {
		return true
	}
	if pinWaits(s, c, seats) {
		return true
	}
	if !ReworkPinned(c) || pinWaived(c) {
		return false
	}
	return c.F(FieldPinSince) == "" || !pinClockRun(s, c)
}

// pinClockUnit starts the pin clock on the primary (FieldPinSince now) and says what it
// waits for and how long: a pin whose friend is held or full (or a come-back pin down)
// with an age the sprint cannot tell. The deal stamps it once and waits this tick.
func pinClockUnit(s *Snapshot, c *Card, name, why string) Unit {
	return Unit{Key: c.ID, Stream: c.Row,
		Changes: []Change{change(Work, setEntry(c, map[string]string{FieldPinSince: stamp(s.Now)}))},
		Moved:   fmt.Sprintf("%s waits ready for friend %s (she is %s): its pin is waived after %s (nova-sprint set --pin-wait)", c.ID, name, why, s.PinWait())}
}

// pinFullAtStart is each dealable friend already at her room before this pass.
// Width 0 is no room, not a full queue. A card this pass places does not count.
func pinFullAtStart(s *Snapshot, seats []FriendSeat) map[string]bool {
	out := map[string]bool{}
	for _, f := range seats {
		if !friendDealable(s, f) {
			continue
		}
		room, _ := friendRoom(f)
		if room > 0 && room-friendLoad(s, f.Name) <= 0 {
			out[f.Name] = true
		}
	}
	return out
}

// pinFateFor is the clock for the named friend. No seats, a hard pin, a pin already
// waived, or a structural miss (not down, held, or full at the start) is no clock.
// A card come back to her (ReworkPinned) reads the clock too: it is a soft pin.
func pinFateFor(s *Snapshot, c *Card, seats []FriendSeat, name string, fullAtStart map[string]bool) pinFate {
	if len(seats) == 0 || c == nil || name == "" || HardPin(c) || pinWaived(c) {
		return pinFate{}
	}
	why := pinBlockWhy(s, seats, name, fullAtStart)
	if why == "" {
		return pinFate{}
	}
	return pinDecide(s, c, seats, name, why)
}

// pinBlockWhy is down, held, or full when the clock applies, else "".
// Held wins over down. Absent from the roster is down.
func pinBlockWhy(s *Snapshot, seats []FriendSeat, name string, fullAtStart map[string]bool) string {
	seat, found := pinSeat(seats, name)
	if (found && seat.Status == Held) || pinControlHeld(s, name) {
		return "held"
	}
	if !found || seat.Status == Down || !friendDealable(s, seat) {
		return "down"
	}
	if fullAtStart[name] {
		return "full"
	}
	return ""
}

// pinDecide times the block. A known age inside PinWait holds, a negative age
// holds as zero, and a first deal's unknown down age is already past: the story uses
// PinWait. A come-back pin's unknown age, and a first deal's unknown full or held
// age, start the clock instead (start): they hold this tick and the bound runs from
// the stamp, so a rework, return or redo is never a hole, and a newly full or held
// friend keeps a first card inside the bound while the sprint cannot tell how long
// she has been unable.
func pinDecide(s *Snapshot, c *Card, seats []FriendSeat, name, why string) pinFate {
	d, known := pinBlockedFor(s, c, seats, name, why)
	if known && d < 0 {
		d = 0
	}
	bound := s.PinWait()
	if known && d <= bound {
		return pinFate{why: why, d: d, hold: true}
	}
	if !known {
		if ReworkPinned(c) || why == "full" || why == "held" {
			return pinFate{why: why, hold: true, start: true}
		}
		d = bound
	} else {
		d = d.Round(time.Second)
	}
	return pinFate{why: why, d: d, waive: true}
}

// pinBlockedFor is how long the block has lasted, and whether that is known.
// pin_since on the card wins. Down then reads Since, her proof, and her control
// card's since. Held reads Since, then the control card's held stamp, then since.
// Full is unknown here: the deal starts the card's own clock on the first tick it
// finds her full (pin_since), so no work card's stamp is read as a second clock.
func pinBlockedFor(s *Snapshot, c *Card, seats []FriendSeat, name, why string) (time.Duration, bool) {
	if c != nil {
		if d, ok := pinAge(s, c.F(FieldPinSince)); ok {
			return d, true
		}
	}
	seat, found := pinSeat(seats, name)
	switch why {
	case "full":
		return 0, false
	case "held":
		if found && !seat.Since.IsZero() {
			return s.Now.Sub(seat.Since), true
		}
		if ctl := pinControl(s, name); ctl != nil {
			if d, ok := pinAge(s, ctl.F("held")); ok {
				return d, true
			}
			if d, ok := pinAge(s, ctl.F("since")); ok {
				return d, true
			}
		}
		return 0, false
	default:
		if found && !seat.Since.IsZero() {
			return s.Now.Sub(seat.Since), true
		}
		if found && !seat.Proof.IsZero() {
			return s.Now.Sub(seat.Proof), true
		}
		if ctl := pinControl(s, name); ctl != nil {
			if d, ok := pinAge(s, ctl.F("since")); ok {
				return d, true
			}
		}
		return 0, false
	}
}

func pinSeat(seats []FriendSeat, name string) (FriendSeat, bool) {
	for _, f := range seats {
		if f.Name == name {
			return f, true
		}
	}
	return FriendSeat{}, false
}

func pinControl(s *Snapshot, name string) *Card {
	if s == nil || s.Fleet == nil {
		return nil
	}
	return s.MemberCtl(FriendRow(name))
}

func pinControlHeld(s *Snapshot, name string) bool {
	ctl := pinControl(s, name)
	return ctl != nil && (ctl.F("held") != "" || ctl.F("status") == Held)
}

func pinAge(s *Snapshot, v string) (time.Duration, bool) {
	t, ok := parseStamp(v)
	if !ok || s == nil {
		return 0, false
	}
	return s.Now.Sub(t), true
}

func parseStamp(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, v)
	return t, err == nil
}

// pinWaivedLine is the one story line when a pin is waived and the card is dealt.
// The clock's reason reads "she is <down|held|full>"; a reason she can never honour
// (left her, her tiers, her restriction, no room) is its own clause and has no wait.
func pinWaivedLine(friend string, d time.Duration, why, unit string) string {
	after := ""
	if d > 0 {
		after = " after " + d.String()
	}
	switch why {
	case "down", "held", "full":
		return fmt.Sprintf("pin to %s waived%s: she is %s; dealt to %s", friend, after, why, unit)
	default:
		return fmt.Sprintf("pin to %s waived%s: %s; dealt to %s", friend, after, why, unit)
	}
}

// stampPinWaived writes pin_waived on the primary's work change, or adds one.
func stampPinWaived(u *Unit, c *Card, now time.Time) {
	if u == nil || c == nil {
		return
	}
	stamped := stamp(now)
	for i := range u.Changes {
		if u.Changes[i].Table != Work || u.Changes[i].Entry.ID != c.ID {
			continue
		}
		if u.Changes[i].Entry.Set == nil {
			u.Changes[i].Entry.Set = map[string]string{}
		}
		u.Changes[i].Entry.Set[FieldPinWaived] = stamped
		return
	}
	u.Changes = append(u.Changes, change(Work, setEntry(c, map[string]string{FieldPinWaived: stamped})))
}

// clearPinWaived drops pin_waived and the pin clock (FieldPinSince) when the preferred
// friend receives the card: her preference is honoured and the clock starts fresh if she
// loses it again. Unsetting an absent field is a no-op.
func clearPinWaived(u *Unit, c *Card) {
	if u == nil || c == nil {
		return
	}
	drop := unsetPresent(c, []string{FieldPinWaived, FieldPinSince})
	if len(drop) == 0 {
		return
	}
	for i := range u.Changes {
		if u.Changes[i].Table != Work || u.Changes[i].Entry.ID != c.ID {
			continue
		}
		u.Changes[i].Entry.Unset = append(u.Changes[i].Entry.Unset, drop...)
		for _, k := range drop {
			delete(u.Changes[i].Entry.Set, k)
		}
		return
	}
	u.Changes = append(u.Changes, change(Work, setEntry(c, nil, drop...)))
}

// machinePinWaiver stamps a fleet deal of a waived pin: a first deal's clock says so, and
// a come-back pin whose clock has run is waived with the deal even when the sprint has no
// roster. A hard pin, an already waived pin, and a pin still inside its clock leave the
// unit as it was.
func machinePinWaiver(s *Snapshot, c *Card, dest string, u *Unit) {
	if s == nil || u == nil || c == nil {
		return
	}
	name, ok := FriendCard(c)
	if !ok || name == "" || HardPin(c) || pinWaived(c) {
		return
	}
	fate := pinFateFor(s, c, s.Friends, name, pinFullAtStart(s, s.Friends))
	if !fate.waive {
		if !ReworkPinned(c) || !pinClockRun(s, c) {
			return
		}
		why := pinBlockWhy(s, s.Friends, name, pinFullAtStart(s, s.Friends))
		if why == "" {
			why = "she did not take it"
		}
		fate = pinFate{why: why, waive: true}
	}
	u.Moved += "; " + pinWaivedLine(name, fate.d, fate.why, dest)
	stampPinWaived(u, c, s.Now)
}

// PinCount is one friend's open named pins, and how many of them are waived.
type PinCount struct {
	Pinned int
	Waived int
}

// FriendPinCounts counts open primaries whose WHO names a friend. Waived is the
// subset carrying pin_waived. A bare WHO: friend counts for no one.
func FriendPinCounts(s *Snapshot) map[string]PinCount {
	out := map[string]PinCount{}
	if s == nil || s.Work == nil {
		return out
	}
	for _, c := range s.Work.Column(Waiting, Ready, Working, Review, Merging) {
		name, ok := FriendCard(c)
		if !ok || name == "" {
			continue
		}
		n := out[name]
		n.Pinned++
		if c.F(FieldPinWaived) != "" {
			n.Waived++
		}
		out[name] = n
	}
	return out
}

// PinCountWord is `pinned N, waived M` when either count is non-zero.
func PinCountWord(n PinCount) string {
	if n.Pinned == 0 && n.Waived == 0 {
		return ""
	}
	return fmt.Sprintf("pinned %d, waived %d", n.Pinned, n.Waived)
}

// PinPreferredWord is `preferred=<friend> waived=<time>` once the pin is waived.
func PinPreferredWord(c *Card) string {
	if c == nil || c.F(FieldPinWaived) == "" {
		return ""
	}
	name, ok := FriendCard(c)
	if !ok || name == "" {
		return ""
	}
	return "preferred=" + name + " waived=" + c.F(FieldPinWaived)
}

// OnlyTailIDs lists placed cards whose brief says `WHO: friend <name> only`.
func OnlyTailIDs(s *Snapshot) []string {
	if s == nil || s.Work == nil {
		return nil
	}
	var ids []string
	for _, c := range s.Work.Column(States...) {
		w, why := cardhdr.ReadWho(c.F("brief"))
		if why == "" && w.Tail {
			ids = append(ids, c.ID)
		}
	}
	slices.Sort(ids)
	return ids
}

// pinCardStarted says a waived card has started, so rebalance leaves it.
// The primary being working is the deal's own mark and is not a start.
func pinCardStarted(s *Snapshot, seat FriendSeat, pr, wc *Card) bool {
	if pr != nil && pr.F(FieldProgress) != "" {
		return true
	}
	if wc == nil {
		return false
	}
	if wc.Col == Working || wc.F(FieldProgress) != "" {
		return true
	}
	return friendStarted(s, seat, wc)
}
