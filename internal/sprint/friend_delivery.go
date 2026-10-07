package sprint

import (
	"fmt"
	"slices"
	"time"
)

// The friend-delivery check (docs/SPEC-SPRINT.md, section friend-width-and-delivery; the
// owner, 2026-10-07: friends must be "actually delivering work over time"). The second gap
// of that morning: a friend up with her lanes busy and no report for a long time. The model
// is tla/FriendWidth.tla (DownOnlyAfterTwoWindows).
//
// The check is a part of the tick (PartFriendDelivery), in the fleet's update after the
// friend-width check. For every friend up with a card working on her row: her last
// delivery is the newest report of hers (ok, failed or HOLD: the store's record of her last
// finish, a finish stamp on a done card of her row, a reported stamp on any card of her
// row), or her oldest working card's take when that is later (a friend who just took work
// has had no time to deliver). When it is DeliveryWindow old (set --delivery-window;
// default the friend-finish window, 30m) the tick raises one judgment on her row,
// NFriendDelivery, naming her busy lanes and how long, with the remedies: a wake ping (the
// friend-delivery rule's first answer, TickRuleFriendPing), friend down with the reason
// and an --until a window on (the rule's answer on the second consecutive window,
// TickRuleFriendDown: she is marked down with that reason until then, her unstarted cards
// go back to ready by the take-back path, and she comes back as a down friend does today,
// by her session's next proof), and wait. It is raised again in place at most every window
// while it holds, and closed when a report of hers arrives, she has no card working, or
// she is not up. A friend with no card working is not judged by this check.

// NFriendDelivery is the judgment of a friend with lanes busy that delivers nothing.
const NFriendDelivery = "a friend has lanes busy and delivers nothing"

// PartFriendDelivery is the friend-delivery part of the tick (TickFriendDelivery).
const PartFriendDelivery = "friend-delivery"

// RuleFriendDelivery is the rule that answers NFriendDelivery: a ping of her session on the
// first window, friend down on the second (TickRuleFriendPing, TickRuleFriendDown). Not in
// RuleNames, as RuleFriendWidth.
const RuleFriendDelivery = "friend-delivery"

// ActFriendDown is the act of the friend-delivery rule's second answer: the friend marked
// down with the reason, until a window on, her unstarted cards taken back.
const ActFriendDown = "friend down"

// PropDeliveryWindow is the work table's property: how long a friend with lanes busy may
// deliver nothing before the tick judges it, a duration (set --delivery-window).
const PropDeliveryWindow = "delivery_window"

// DeliveryWindow is that window: the sprint's setting, else the friend-finish window
// (FriendFinishAfter: set --friend-finish, default FriendFinishDefault), so the two
// checks of a friend who finishes nothing share one default.
func (s *Snapshot) DeliveryWindow() time.Duration {
	if s != nil && s.Work != nil {
		if v, ok := s.Work.Prop(PropDeliveryWindow); ok {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				return d
			}
		}
	}
	return s.FriendFinishAfter()
}

// The friend-delivery check's state, the fleet table's properties by friend (episodeProps).
const friendDeliveryPrefix = "friend_delivery"

// PropFriendDeliveryDown is when the friend-delivery rule marked her down in this episode
// (RFC3339); absent otherwise.
func PropFriendDeliveryDown(friend string) string {
	return friendDeliveryPrefix + "_" + epDown + "." + friend
}

// FriendLastDelivery is when a report of the friend's last arrived: the newest of the
// store's record of her last finish (FriendSeat.Finished), a finish stamp on a done card of
// her row, and a reported stamp (FieldReported) on any card of her row, which a HOLD report
// writes too; zero for none.
func FriendLastDelivery(s *Snapshot, name string, finished time.Time) time.Time {
	at := finished
	if s == nil || s.Fleet == nil {
		return at
	}
	row := FriendRow(name)
	for _, col := range []string{DoneOK, DoneFailed} {
		for _, c := range s.Fleet.Cell(row, col) {
			if t := stampAt(c, "finished"); t.After(at) {
				at = t
			}
		}
	}
	for _, col := range []string{Ready, Working, DoneOK, DoneFailed} {
		for _, c := range s.Fleet.Cell(row, col) {
			if t := stampAt(c, FieldReported); t.After(at) {
				at = t
			}
		}
	}
	return at
}

// deliverySilence is how long the friend has had lanes busy and delivered nothing, in
// running time: since the later of her last delivery and her oldest working card's take
// (its deal when it was never taken); the ids of her working cards; and when her last
// delivery was (zero for never). ok is false when she has no card working or no stamp
// says when her silence began.
func deliverySilence(s *Snapshot, r TickReq, name string, finished time.Time) (silence time.Duration, ids []string, last time.Time, ok bool) {
	row := FriendRow(name)
	working := s.Fleet.Cell(row, Working)
	if len(working) == 0 {
		return 0, nil, finished, false
	}
	last = FriendLastDelivery(s, name, finished)
	var oldest time.Time
	for _, c := range working {
		ids = append(ids, c.ID)
		t := stampAt(c, "taken")
		if t.IsZero() {
			t = stampAt(c, "dealt")
		}
		if !t.IsZero() && (oldest.IsZero() || t.Before(oldest)) {
			oldest = t
		}
	}
	slices.Sort(ids)
	since := last
	if oldest.After(since) {
		since = oldest
	}
	if since.IsZero() {
		return 0, ids, last, false
	}
	silence, ok = r.running(s.Now, stamp(since))
	return silence, ids, last, ok
}

// deliveryDecisions are the remedies the friend-delivery judgment offers.
func deliveryDecisions(friend string, last time.Time, until time.Time) []string {
	since := "the start of her work"
	if !last.IsZero() {
		since = stamp(last)
	}
	return []string{
		"nova-friend ping --as <coordinator> --to " + friend + " --wake",
		fmt.Sprintf("friend down %s --reason 'no delivery since %s' --until %s", friend, since, stamp(until)),
		"ack",
		"wait",
	}
}

// deliveryReason is the reason the friend-delivery rule marks her down with.
func deliveryReason(last time.Time) string {
	if last.IsZero() {
		return "no delivery since the start of her work"
	}
	return "no delivery since " + stamp(last)
}

// TickFriendDelivery is the friend-delivery part of the tick (PartFriendDelivery): the
// comment at the top of the file. It is pure.
func TickFriendDelivery(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	if s.Fleet == nil || s.Work == nil {
		return p, 0
	}
	write := fleetPropWriter(s, &p)
	window := s.DeliveryWindow()
	names, seats := checkFriends(s, r)
	for _, f := range names {
		row := FriendRow(f)
		e := episodeProps{friendDeliveryPrefix, f}
		seat, ok := seats[f]
		silence, ids, last, known := deliverySilence(s, r, f, seat.Finished)
		holds := friendUpForChecks(s, r, seat, ok) && known && silence >= window
		if !holds {
			if e.any(s) {
				e.clear(s, write)
				p.Units = append(p.Units, Unit{Key: row, Moved: fmt.Sprintf("friend %s: delivering again, or no card working, or not up: the delivery clock is cleared", f)})
			}
			closeEpisode(&p, s, NFriendDelivery, row)
			continue
		}
		raised := e.count(s, epRaised)
		if raised > 0 {
			if d, ok := r.running(s.Now, e.get(s, epLast)); ok && d < window {
				continue // raised again at most every window
			}
		}
		lastWord := "no report of hers yet"
		if !last.IsZero() {
			lastWord = "her last report at " + stamp(last)
		}
		what := fmt.Sprintf("friend %s has %d lanes busy and delivered nothing for %s (%s; working: %s)",
			f, len(ids), silence.Round(time.Second), lastWord, Preview(ids, ", "))
		if pinged := e.get(s, epPinged); pinged != "" {
			what += "; her session was pinged by rule " + RuleFriendDelivery + " at " + pinged
		}
		if down := e.get(s, epDown); down != "" {
			what += "; marked down by rule " + RuleFriendDelivery + " at " + down
		}
		if raiseEpisode(&p, s, r, NFriendDelivery, row, what, deliveryDecisions(f, last, s.Now.Add(window)), raised) {
			write(e.name(epRaised), itoa(raised+1))
			write(e.name(epLast), stamp(s.Now))
		}
	}
	return p, 0
}

// ruleFriendDelivery: the friend-delivery judgment. On the first window her session is
// pinged once (ActPing); on the second consecutive window she is marked down with the
// reason until a window on (ActFriendDown), once an episode; after that a mind's.
func ruleFriendDelivery(s *Snapshot, r TickReq, a *RuleAnswer) {
	a.Rule = RuleFriendDelivery
	f, ok := FriendOfRow(a.Subject)
	if !ok {
		left(a, "not a friend's row")
		return
	}
	e := episodeProps{friendDeliveryPrefix, f}
	if down := e.get(s, epDown); down != "" {
		left(a, "marked down by this rule at "+down+": a mind's")
		return
	}
	var finished time.Time
	for _, seat := range r.Friends {
		if seat.Name == f {
			finished = seat.Finished
		}
	}
	window := s.DeliveryWindow()
	silence, _, last, known := deliverySilence(s, r, f, finished)
	switch {
	case !known:
		left(a, "no card of hers is working now: the judgment closes with the condition")
	case silence >= 2*window:
		a.Act, a.from, a.until = ActFriendDown, f, s.Now.Add(window)
		a.Why = fmt.Sprintf("friend %s delivered nothing for %s, two windows of %s: down with reason '%s' until %s, her unstarted cards back to ready",
			f, silence.Round(time.Second), window, deliveryReason(last), stamp(a.until))
	case e.get(s, epPinged) == "":
		a.Act, a.from = ActPing, f
		a.Why = fmt.Sprintf("friend %s has lanes busy and delivered nothing for %s: her session is pinged once", f, silence.Round(time.Second))
	default:
		left(a, "her session was pinged at "+e.get(s, epPinged)+"; down by rule after the second window of "+window.String())
	}
}

// PartRuleFriendDown is the tick part that marks down the friends the friend-delivery rule
// answers with ActFriendDown (TickRuleFriendDown).
const PartRuleFriendDown = "rule friend down"

// TickRuleFriendDown marks down the first friend the friend-delivery rule answers with
// ActFriendDown (one a tick: a plan carries one health observation): her health observed
// down with the reason and until a window on, as the stall ladder's rung 5 does, her fleet
// row's status down, her unstarted cards taken back (FriendTake with All; a started card
// stays with her and finishes), the judgment closed as answered by rule, and the log
// saying MOVED rule friend-delivery: friend <f> down. She comes back as a down friend does:
// by her session's next proof, which the next observation of her carries.
func TickRuleFriendDown(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	if s.Fleet == nil {
		return p, 0
	}
	write := fleetPropWriter(s, &p)
	for _, a := range acting(s, r, ActFriendDown) {
		f := a.from
		row := FriendRow(f)
		_, _, last, _ := deliverySilence(s, r, f, time.Time{})
		reason := deliveryReason(last)
		said := RuleSaid(RuleFriendDelivery, a.Act+": "+a.Why)
		started := map[string]string{}
		for _, seat := range r.Friends {
			if seat.Name == f {
				for _, run := range seat.Running {
					started[run] = "her beat names it running"
				}
			}
		}
		for _, c := range append(append([]*Card(nil), s.Fleet.Cell(row, Ready)...), s.Fleet.Cell(row, Working)...) {
			switch {
			case c.Col == Working:
				started[c.ID] = "working on her row: started"
			case c.F(FieldProgress) != "":
				started[c.ID] = "progress was stamped on it"
			case c.F(FieldReported) != "":
				started[c.ID] = "a report of hers was written on it"
			}
		}
		take := FriendTake(s, FriendTakeReq{Friend: f, All: true, Reason: said, Who: r.who(), Started: started})
		p.Units = append(p.Units, take.Units...)
		p.Refused = append(p.Refused, take.Refused...)
		p.Rows = append(p.Rows, take.Rows...)
		gen := max(s.SeatGeneration, FirstSeatGeneration)
		p.Health = &FriendHealthWrite{Friend: f, Health: FriendHealth{State: Down, Reason: reason, Until: a.until, Seen: s.Now, Generation: gen}}
		e := episodeProps{friendDeliveryPrefix, f}
		write(e.name(epDown), stamp(s.Now))
		u := Unit{Key: row, Moved: fmt.Sprintf("rule %s: friend %s down: %s, until %s; answered by rule %s", RuleFriendDelivery, f, reason, stamp(a.until), RuleFriendDelivery)}
		if ctl := s.MemberCtl(row); ctl != nil {
			u.Changes = []Change{change(Fleet, setEntry(ctl, map[string]string{"status": Down, "since": stamp(s.Now)}))}
		}
		u.Closes = append(u.Closes, a.open)
		u.Notes = append(u.Notes, decided(a.open, said, r.who(), s.Now, a.Subject))
		p.Units = append(p.Units, u)
		break // one observation a plan: the next tick marks the next friend
	}
	return p, 0
}
