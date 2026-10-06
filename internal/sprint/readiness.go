package sprint

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
)

// The headline's capacity is eligible execution, never raw counts (docs/SPEC-SPRINT.md,
// readiness-headline-is-eligible-capacity2.w4; the nova-sprint review of 2026-10-06, item 1: a fleet
// whose free slots cannot take the ready cards read as idle waste, and a deliberate hold read
// the same as accidental starvation). A free slot is a lane of a row up (a machine's width, a
// friend's lanes) that holds no working card; it is eligible when a card can fill it now: a
// card already dealt to the row and not taken, or a ready card the deal would place there,
// read by running the deal's own plans (friendDeal, then Deal) on the snapshot and applying
// nothing. A slot no card can fill is unused, and carries the reason, its owner and the next
// action: the build lanes of its machine full, a ready card the deal will not place (a held
// stream, a pin, a bound's judgment, a tier no route serves, a bench), a decision waiting on
// the coordinator (NeedsRank), the critical path in flight (Critical), or no work at all.
// A reason is a hold when a person keeps the work back on purpose (a held stream, a card
// admitted held, a sentinel); the headline counts those lanes apart from the starved ones.
// HeadlineCapacity is a read: it writes nothing and lifts no hold; the next action is the
// owner's command to run.

// The kinds of a CapacityReason, in the order unused lanes are given to them.
const (
	CapBuildLanes = "build lanes" // the machine's Go lanes are full with others waiting
	CapStreamHeld = "stream held" // ready cards of a stream the coordinator holds
	CapPinned     = "pinned"      // ready cards pinned to a friend who cannot take them
	CapBound      = "bound"       // ready cards at a bound, waiting on its judgment
	CapNoRoute    = "no route"    // ready cards of a tier no route serves
	CapBench      = "bench"       // ready cards whose bench has no member up
	CapRefused    = "refused"     // ready cards the deal refused for another reason
	CapNeed       = "need"        // waiting cards behind a decision of the coordinator (NeedsRank)
	CapInFlight   = "in flight"   // waiting cards behind work in flight, the critical path
	CapNoWork     = "no work"     // nothing ready or waiting is left to deal
)

// Owners of a reason's next action.
const (
	OwnerCoordinator = "coordinator"
	OwnerMachine     = "machine" // the tick moves it with no one acting
)

// Capacity is the headline's capacity at s.Now.
type Capacity struct {
	Free     int `json:"free"`     // the lanes up holding no working card
	Eligible int `json:"eligible"` // the free lanes a card can fill now
	Unused   int `json:"unused"`   // Free less Eligible
	Held     int `json:"held"`     // the unused lanes a deliberate hold leaves
	Starved  int `json:"starved"`  // the unused lanes nothing holds on purpose
	// Ready is the ready primaries, sentinels aside, and Placeable those the deal would
	// place now (on a free lane or behind one).
	Ready     int              `json:"ready"`
	Placeable int              `json:"placeable"`
	Rows      []CapacityRow    `json:"rows"`
	Reasons   []CapacityReason `json:"reasons,omitempty"`
	// Refill is the advice for the unused lanes: whether more ready work helps, checked
	// against the readers and the merge downstream; nil when no lane is unused.
	Refill *Refill `json:"refill,omitempty"`
}

// CapacityRow is one row up: a machine (its member name) or a friend (FriendRow).
type CapacityRow struct {
	Row      string `json:"row"`
	Friend   bool   `json:"friend,omitempty"`
	Width    int    `json:"width"`  // its lanes: a machine's width, a friend's lanes (friendRoom)
	Working  int    `json:"work"`   // its work cards working
	Queued   int    `json:"queued"` // its work cards dealt and not taken
	Placed   int    `json:"placed"` // the ready cards the deal would place on it now
	Free     int    `json:"free"`
	Eligible int    `json:"eligible"`
	// Lanes is why its free lanes are not eligible though cards are there: its build
	// lanes full; "" when they are not.
	Lanes string `json:"lanes,omitempty"`
}

// CapacityReason is why some unused lanes are unused.
type CapacityReason struct {
	Kind  string   `json:"kind"`
	Hold  bool     `json:"hold,omitempty"` // a deliberate hold, never starvation
	Lanes int      `json:"lanes"`          // the unused lanes it accounts for
	Cards []string `json:"cards,omitempty"`
	Say   string   `json:"say"`
	Owner string   `json:"owner"`
	Next  string   `json:"next"`
}

// Refill is the advice on refilling ready: Advise when more ready work would run, else the
// downstream stage it would only queue behind, and the action that moves it.
type Refill struct {
	Advise bool   `json:"advise"`
	Say    string `json:"say"`
	Owner  string `json:"owner"`
	Next   string `json:"next"`
}

// CapacityReq is what the headline reads beside the snapshot: the friends as the deal sees
// them, and the build lanes (lane list), nil when none were read.
type CapacityReq struct {
	Friends []FriendSeat
	Lanes   []LaneRow
}

// HeadlineCapacity is the capacity at s.Now (see the top of this file).
func HeadlineCapacity(s *Snapshot, r CapacityReq) Capacity {
	var out Capacity
	if s == nil || s.Work == nil || s.Fleet == nil {
		return out
	}
	s, _ = s.withRests()
	for _, c := range s.Work.Column(Ready) {
		if !IsSentinel(c) {
			out.Ready++
		}
	}

	// the deal, planned and not applied: the friends first, then the machines (TickDeal's order)
	up := s.UpMembers()
	fp, _, _ := friendDeal(s, friendOffer(s), r.Friends)
	placed, placedOn := map[string]bool{}, map[string]int{}
	tally := func(p Plan) {
		for _, u := range p.Units {
			for _, ch := range u.Changes {
				if ch.Table != Fleet {
					continue
				}
				row := ""
				switch e := ch.Entry; {
				case e.Create != nil:
					row = e.Create.Row
				case e.Move != nil:
					row = e.Move.Row
				}
				if row != "" && !placed[u.Key] {
					placed[u.Key] = true
					placedOn[row]++
				}
			}
		}
	}
	tally(fp)
	why := map[string]reasonOf{} // a ready card left unplaced: why
	var machine []string
	for _, c := range s.Work.Column(Ready) {
		if IsSentinel(c) || placed[c.ID] {
			continue
		}
		if x, ok := readyHeldBy(s, c, up, r.Friends); ok {
			why[c.ID] = x
			continue
		}
		machine = append(machine, c.ID)
	}
	if len(up) > 0 && len(machine) > 0 {
		dp := Deal(s, DealReq{Sel: Sel{Only: machine}})
		tally(dp)
		for _, f := range dp.Refused {
			if !placed[f.Key] {
				why[f.Key] = refusedReason(f.Why)
			}
		}
	}
	for _, id := range machine {
		if _, ok := why[id]; !ok && !placed[id] {
			why[id] = reasonOf{kind: CapRefused, say: "no machine is up to deal it to", owner: OwnerCoordinator, next: "nova-sprint fleet up <member>"}
		}
	}
	out.Placeable = len(placed)

	// the rows: each free lane eligible while a card dealt to the row, or one the deal
	// would place there, can fill it
	full := map[string]LaneRow{}
	for _, l := range r.Lanes {
		if l.Kind == LaneGo && l.Width > 0 && len(l.Held) >= l.Width && len(l.Waiting) > 0 {
			full[l.Machine] = l
		}
	}
	var lanesFull []string
	lanesUnused := 0
	addRow := func(row string, friend bool, width int) {
		cr := CapacityRow{Row: row, Friend: friend, Width: width, Working: s.Fleet.Count(row, Working), Queued: s.Fleet.Count(row, Ready), Placed: placedOn[row]}
		cr.Free = max(0, width-cr.Working)
		cr.Eligible = min(cr.Free, cr.Queued+cr.Placed)
		if l, ok := full[row]; ok && !friend && cr.Eligible > 0 {
			cr.Lanes = fmt.Sprintf("build lanes full: %d of %d held, %d waiting", len(l.Held), l.Width, len(l.Waiting))
			lanesUnused += cr.Eligible
			lanesFull = append(lanesFull, row)
			cr.Eligible = 0
		}
		out.Free += cr.Free
		out.Eligible += cr.Eligible
		out.Rows = append(out.Rows, cr)
	}
	for _, m := range up {
		addRow(m, false, s.Width(m))
	}
	for _, f := range r.Friends {
		if f.Status != Up {
			continue
		}
		_, lanes := friendRoom(f)
		addRow(FriendRow(f.Name), true, lanes)
	}
	out.Unused = out.Free - out.Eligible
	left := out.Unused
	give := func(x reasonOf, cards []string, want int) {
		n := min(left, want)
		if n <= 0 {
			return
		}
		left -= n
		out.Reasons = append(out.Reasons, CapacityReason{Kind: x.kind, Hold: x.hold, Lanes: n, Cards: cards, Say: x.say, Owner: x.owner, Next: x.next})
	}

	// 1. a machine's build lanes, full with others waiting
	if lanesUnused > 0 {
		give(reasonOf{kind: CapBuildLanes, say: "build lanes full on " + strings.Join(lanesFull, ", ") + ": the gate waits for a Go lane", owner: OwnerMachine, next: "nova-sprint lane list"}, nil, lanesUnused)
	}

	// 2. ready cards the deal will not place, grouped by their reason, the most cards first
	type group struct {
		x     reasonOf
		cards []string
	}
	groups := map[string]*group{}
	for _, id := range slices.Sorted(maps.Keys(why)) {
		x := why[id]
		k := x.kind + "\x00" + x.say
		if groups[k] == nil {
			groups[k] = &group{x: x}
		}
		groups[k].cards = append(groups[k].cards, id)
	}
	list := make([]*group, 0, len(groups))
	for _, g := range groups {
		list = append(list, g)
	}
	slices.SortFunc(list, func(a, b *group) int {
		return cmp.Or(cmp.Compare(len(b.cards), len(a.cards)), cmp.Compare(capRank(a.x.kind), capRank(b.x.kind)), cmp.Compare(a.x.say, b.x.say))
	})
	for _, g := range list {
		give(g.x, g.cards, len(g.cards))
	}

	// 3. waiting cards behind a decision of the coordinator, heaviest first (NeedsRank)
	waiting := len(waitingCards(s))
	named := map[string]bool{}
	for _, n := range NeedsRank(s) {
		named[n.ID] = true
		for _, id := range n.Cards {
			named[id] = true
		}
		give(needReason(s, n), n.Cards, n.Behind+len(n.Cards))
	}
	// a sentinel not reached yet is a gate the coordinator set on purpose: the cards behind
	// it wait on its release as much as on the cards before it
	for _, c := range s.Work.Column(Waiting) {
		if !IsSentinel(c) || named[c.ID] {
			continue
		}
		if behind := len(Behind(s, c)); behind > 0 {
			give(reasonOf{kind: CapNeed, hold: true, say: fmt.Sprintf("sentinel %s waits for the cards before it, %d cards behind", c.ID, behind), owner: OwnerCoordinator,
				next: "nova-sprint release " + c.ID + " --reason '<why the wave goes now>'"}, []string{c.ID}, behind)
		}
	}

	// 4. waiting cards behind work in flight: the critical path's heaviest card names it
	if free := waiting - HeldBack(s); free > 0 {
		x := reasonOf{kind: CapInFlight, say: fmt.Sprintf("%d cards wait on work in flight", free), owner: OwnerMachine, next: "nova-sprint needs --roots"}
		var cards []string
		if crit := Critical(s, 1); len(crit) > 0 {
			c := crit[0]
			cards = []string{c.ID}
			x.say += fmt.Sprintf("; the critical path: %s %d behind, %s", c.ID, c.Behind, c.State)
			x.next = inFlightNext(s, c)
		}
		give(x, cards, free)
	}

	// 5. nothing left to deal
	if left > 0 {
		say := "nothing is ready and nothing waits: the free lanes have no work"
		if out.Ready > 0 || waiting > 0 {
			say = fmt.Sprintf("no card can fill them: ready %d, waiting %d", out.Ready, waiting)
		}
		give(reasonOf{kind: CapNoWork, say: say, owner: OwnerCoordinator, next: "nova-sprint add"}, nil, left)
	}
	for _, x := range out.Reasons {
		if x.Hold {
			out.Held += x.Lanes
		}
	}
	out.Starved = out.Unused - out.Held
	if out.Unused > 0 {
		out.Refill = refillAdvice(s, out)
	}
	return out
}

// reasonOf is a reason's words before its lanes are counted.
type reasonOf struct {
	kind, say, owner, next string
	hold                   bool
}

// capRank orders reasons of equal weight: the kinds in the order they are declared.
func capRank(kind string) int {
	return slices.Index([]string{CapBuildLanes, CapStreamHeld, CapPinned, CapBound, CapNoRoute, CapBench, CapRefused, CapNeed, CapInFlight, CapNoWork}, kind)
}

// readyHeldBy is why the tick's deal leaves a ready card off every machine (TickDeal's
// order): its stream held, a hard pin to a friend, its bound's judgment, a tier no route
// serves, or a bench with no member up. ok is false for a card the machines' deal is offered.
func readyHeldBy(s *Snapshot, c *Card, up []string, friends []FriendSeat) (reasonOf, bool) {
	switch {
	case StreamHeld(s, c.Row):
		return reasonOf{kind: CapStreamHeld, hold: true, say: "stream " + c.Row + " is held by the coordinator", owner: OwnerCoordinator, next: "nova-sprint unhold " + c.Row}, true
	case OnlyFriend(c):
		name := strings.TrimPrefix(c.F(FieldWho), "only."+friendRowPrefix)
		return reasonOf{kind: CapPinned, say: "pinned to friend " + name + ", who cannot take it now" + friendWhy(friends, name), owner: "friend " + name, next: "nova-sprint view worker --as " + name}, true
	}
	if wc := AtRedealBound(s, c); wc != nil {
		return reasonOf{kind: CapBound, say: "at its redeal bound: its judgment decides", owner: OwnerCoordinator, next: "nova-sprint inbox --read"}, true
	}
	if wc, _ := AtStagingBound(s, c, up); wc != nil {
		return reasonOf{kind: CapBound, say: "refused at staging by every member up: its judgment decides", owner: OwnerCoordinator, next: "nova-sprint inbox --read"}, true
	}
	if tier, why := s.noRoute(escalating(s, c)); why != "" {
		return reasonOf{kind: CapNoRoute, say: "tier " + tier + ": no route serves it", owner: OwnerCoordinator, next: "nova-sprint routes"}, true
	}
	if b := Bench(c); len(b) > 0 && len(onlyBench(up, b)) == 0 {
		return reasonOf{kind: CapBench, say: benchRefusal(b), owner: OwnerCoordinator, next: "nova-sprint fleet up " + b[0]}, true
	}
	return reasonOf{}, false
}

// friendWhy is the words of a friend's status as the seat says it: ": down (why)".
func friendWhy(friends []FriendSeat, name string) string {
	for _, f := range friends {
		if f.Name == name {
			if f.Status == Up {
				return ": their lanes are full"
			}
			return ": " + cmp.Or(f.Status, Down) + parens(f.Why)
		}
	}
	return ": not on the roster"
}

func parens(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}

// refusedReason is a reason from the machines' deal's own refusal.
func refusedReason(why string) reasonOf {
	if strings.Contains(why, "unhold") {
		return reasonOf{kind: CapStreamHeld, hold: true, say: why, owner: OwnerCoordinator, next: "nova-sprint where --all"}
	}
	return reasonOf{kind: CapRefused, say: why, owner: OwnerCoordinator, next: "nova-sprint where --all"}
}

// needReason is a decision of NeedsRank as a reason: a card admitted held and a sentinel
// are holds (the coordinator keeps the work back on purpose); a judgment and a stopped
// stream are not.
func needReason(s *Snapshot, n Need) reasonOf {
	behind := fmt.Sprintf("%d cards behind", n.Behind)
	switch n.Kind {
	case NeedHeld:
		return reasonOf{kind: CapNeed, hold: true, say: "card " + n.ID + " admitted held, " + behind, owner: OwnerCoordinator, next: "nova-sprint release " + n.ID + " --reason '<why it goes now>'"}
	case NeedSentinel:
		state := "held"
		if c := s.Work.Card(n.ID); c != nil && !IsHeld(c) {
			state = "reached"
		}
		return reasonOf{kind: CapNeed, hold: true, say: "sentinel " + n.ID + " " + state + ", " + behind, owner: OwnerCoordinator, next: "nova-sprint release " + n.ID + " --reason '<why the wave goes now>'"}
	case NeedStream:
		return reasonOf{kind: CapNeed, say: "stream " + n.ID + " stopped (" + cmp.Or(n.Type, "-") + "), " + behind, owner: OwnerCoordinator, next: "nova-sprint resume --stream " + n.ID}
	}
	return reasonOf{kind: CapNeed, say: "judgment " + cmp.Or(n.Alias, n.ID) + " (" + n.Type + "), " + behind, owner: OwnerCoordinator, next: "nova-sprint inbox --open " + n.ID}
}

// inFlightNext is the command that moves the critical path's card from its state.
func inFlightNext(s *Snapshot, c CriticalCard) string {
	stream := ""
	if pc := s.Work.Card(c.ID); pc != nil {
		stream = pc.Row
	}
	switch c.State {
	case Review:
		return "nova-sprint ask --stream " + stream
	case Merging:
		return "nova-sprint land --stream " + stream
	}
	return "nova-sprint card " + c.ID
}

// refillAdvice says whether refilling ready would run on the unused lanes, or only move
// the queue: with the readers at their width while results wait in review, or results
// waiting to merge on a stopped stream, more work dealt ends in that queue, and the action
// is the downstream one.
func refillAdvice(s *Snapshot, c Capacity) *Refill {
	review, merging, reviewStream, mergeStream := 0, 0, "", ""
	for _, pc := range s.Work.Column(Review) {
		review++
		reviewStream = cmp.Or(reviewStream, pc.Row)
	}
	for _, pc := range s.Work.Column(Merging) {
		if s.StreamCtl(pc.Row).F("state") == StreamStopped {
			merging++
			mergeStream = cmp.Or(mergeStream, pc.Row)
		}
	}
	if s.Readers != nil && review > 0 {
		room, readers := 0, s.UpReaders()
		for _, rd := range readers {
			w := s.ReaderWidth(rd)
			if w == math.MaxInt {
				room = math.MaxInt
				break
			}
			room += max(0, w-s.readerLoad(rd))
		}
		if room == 0 {
			say := fmt.Sprintf("refilling moves the queue: %d results in review and the readers have no free room", review)
			if len(readers) == 0 {
				say = fmt.Sprintf("refilling moves the queue: %d results in review and no reader is up", review)
			}
			return &Refill{Say: say, Owner: OwnerCoordinator, Next: "nova-sprint ask --stream " + reviewStream}
		}
	}
	if merging > 0 {
		return &Refill{Say: fmt.Sprintf("refilling moves the queue: %d results wait to merge on a stopped stream", merging), Owner: OwnerCoordinator, Next: "nova-sprint resume --stream " + mergeStream}
	}
	// the downstream has room: the first reason a person can act on is the refill
	for _, x := range c.Reasons {
		if x.Owner != OwnerMachine {
			return &Refill{Advise: true, Say: "the readers and the merge have room: " + x.Say, Owner: x.Owner, Next: x.Next}
		}
	}
	return &Refill{Advise: true, Say: "the readers and the merge have room; the machine moves the rest", Owner: OwnerMachine, Next: "nova-sprint needs --roots"}
}

// Line is the capacity as the headline says it: "slots <eligible>/<free> free" and, with
// lanes unused, the held and starved lanes and the heaviest reason with its next action.
func (c Capacity) Line() string {
	l := fmt.Sprintf("slots %d/%d free", c.Eligible, c.Free)
	if c.Unused == 0 {
		return l
	}
	l += fmt.Sprintf(" (unused %d: held %d, starved %d)", c.Unused, c.Held, c.Starved)
	if len(c.Reasons) > 0 {
		x := c.Reasons[0]
		l += "; " + x.Say + " [" + x.Owner + "]"
	}
	return l
}
