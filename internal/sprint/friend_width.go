package sprint

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The friend-width check (docs/SPEC-SPRINT.md, section friend-width-and-delivery; the
// owner, 2026-10-07: "make sure the friends are working to their width and actually
// delivering work over time. [It] should become machinery because you cannot do it inside
// LLM thought reliably."). On 2026-10-07 at 11:48 AM three friends sat for an hour with
// free lanes beside queued cards (2 of 8 working with 3 ready; 4 of 16 with 4 ready; 10 of
// 16 with 23 ready) and nothing in the machinery noticed. The causes were each friend's own
// (a runner with a constant cap, a daemon that runs one lane and names no running card, a
// friend who takes what she wants); the symptom is one: free lanes beside queued cards, for
// a long time. The model is tla/FriendWidth.tla (RaisedOnlyAfterWindow, RaisedWhenWindowHeld,
// WidthNeverChangedByMachine, PingedWithinWindow).
//
// The check is a part of the tick (PartFriendWidth), in the fleet's update after the stall
// ladder. For every friend up: while her working lanes are under her width and a card sits
// ready on her row (dealt to her and not started), the clock runs (PropFriendWidthSince, in
// the store, so a server switch keeps it); once it has run WidthIdleAfter (set
// --width-idle-after, default 5m) the tick raises one judgment on her row, NFriendWidth,
// naming her free lanes, her queued cards and how long, with the remedies: a wake ping
// (the friend-width rule's one answer, TickRuleFriendPing: her session is pinged once an
// episode with the queued card ids in the body), friend take --all-unstarted (the queue
// handed back for the deal to give to a friend with live lanes), friend down, and wait. It
// is raised again in place at most every WidthIdleAfter while the condition holds, and
// closed, its clock cleared, when her working reaches her width, her queue empties, or she
// is not up. From the third raise on it says her observed concurrency (the most lanes she
// ran in the episode) beside her configured width, and the nova-config line that would set
// it, and never changes the width itself: one width per friend, set by people in
// nova-config (the owner, 2026-10-06).

// NFriendWidth is the judgment of a friend with free lanes beside queued cards.
const NFriendWidth = "a friend has free lanes beside queued cards"

// PartFriendWidth is the friend-width part of the tick (TickFriendWidth).
const PartFriendWidth = "friend-width"

// RuleFriendWidth is the rule that answers NFriendWidth once an episode, with a ping of her
// session (TickRuleFriendPing). Not in RuleNames: nova-config's answer_rules_off enum
// (config.AnswerRules, held equal to RuleNames) does not name it yet, so only run
// --answer-rules=false turns it off, as RulePaths.
const RuleFriendWidth = "friend-width"

// ActPing is the act of the friend-width and friend-delivery rules' first answer: her
// session pinged once, through the tick's ping path (TickReq.PingFriend).
const ActPing = "ping her session"

// PropWidthIdleAfter is the work table's property: how long a friend up may have free lanes
// beside queued cards before the tick judges it, a duration (set --width-idle-after).
const PropWidthIdleAfter = "width_idle_after"

// WidthIdleAfterDefault is WidthIdleAfter when the coordinator set none.
const WidthIdleAfterDefault = 5 * time.Minute

// WidthIdleAfter is that bound: the sprint's setting, else WidthIdleAfterDefault.
func (s *Snapshot) WidthIdleAfter() time.Duration {
	if s != nil && s.Work != nil {
		if v, ok := s.Work.Prop(PropWidthIdleAfter); ok {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				return d
			}
		}
	}
	return WidthIdleAfterDefault
}

// The friend-width check's state, the fleet table's properties by friend (a property name
// is letters, digits, _ . and -, so her name follows a dot), kept in the store like the
// stall ladder's rung so a server switch keeps the clock.
const friendWidthPrefix = "friend_width"

// PropFriendWidthSince is when free lanes beside queued cards were first seen on her row in
// this episode (RFC3339); absent while the condition does not hold.
func PropFriendWidthSince(friend string) string { return friendWidthPrefix + "_since." + friend }

// episodeProps names one check's state on one friend: the fleet table's properties under
// the check's prefix, each "<prefix>_<key>.<friend>".
type episodeProps struct{ prefix, friend string }

// The keys of an episode's properties.
const (
	epSince  = "since"  // when the condition was first seen (RFC3339)
	epRaised = "raised" // the raises of the judgment in this episode
	epLast   = "last"   // when it was last raised (RFC3339)
	epMax    = "max"    // the most lanes she ran in the episode (friend-width)
	epPinged = "pinged" // when the rule pinged her session (RFC3339)
	epDown   = "down"   // when the rule marked her down (RFC3339; friend-delivery)
)

var episodeKeys = []string{epSince, epRaised, epLast, epMax, epPinged, epDown}

func (e episodeProps) name(key string) string { return e.prefix + "_" + key + "." + e.friend }

// get is the property's value, "" when absent.
func (e episodeProps) get(s *Snapshot, key string) string {
	v, _ := s.Fleet.Prop(e.name(key))
	return v
}

// count is the property's value as a count, 0 when absent or unreadable.
func (e episodeProps) count(s *Snapshot, key string) int {
	n, _ := strconv.Atoi(e.get(s, key))
	return n
}

// any says the episode has state in the store.
func (e episodeProps) any(s *Snapshot) bool {
	for _, k := range episodeKeys {
		if e.get(s, k) != "" {
			return true
		}
	}
	return false
}

// clear removes every property of the episode.
func (e episodeProps) clear(s *Snapshot, write func(name, value string)) {
	for _, k := range episodeKeys {
		if e.get(s, k) != "" {
			write(e.name(k), "")
		}
	}
}

// fleetPropWriter is a plan's writer of fleet table properties, each guarded on the value
// the plan read, written once a plan, and never when it changes nothing: a part that wrote
// an unchanged property every tick would never let the tick end.
func fleetPropWriter(s *Snapshot, p *Plan) func(name, value string) {
	written := map[string]bool{}
	return func(name, value string) {
		if written[name] {
			return
		}
		was, had := s.Fleet.Prop(name)
		if (!had && value == "") || (had && was == value) {
			return
		}
		written[name] = true
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: name, Value: value, Was: was, WasAbsent: !had})
	}
}

// checkFriends is every friend the checks look at, by name: the seats the tick was given
// and every friend row on the fleet table (a friend off the roster whose state is still
// in the store is cleared).
func checkFriends(s *Snapshot, r TickReq) (names []string, seats map[string]FriendSeat) {
	seats = map[string]FriendSeat{}
	set := map[string]bool{}
	for _, f := range r.Friends {
		seats[f.Name] = f
		set[f.Name] = true
	}
	for _, row := range s.Fleet.Rows() {
		if f, ok := FriendOfRow(row); ok {
			set[f] = true
		}
	}
	return slices.Sorted(maps.Keys(set)), seats
}

// friendUpForChecks says the friend is up for the checks: her seat is up and dealable
// (friendDealable: the friends' work switch on), the coordinator does not hold her, and her
// row is not held.
func friendUpForChecks(s *Snapshot, r TickReq, f FriendSeat, ok bool) bool {
	if !ok || !friendDealable(s, f) || r.Sessions[f.Name].Held {
		return false
	}
	return s.MemberCtl(FriendRow(f.Name)).F("status") != Held
}

// friendLanes is a friend's lanes at work and her queue: the cards working on her row (a
// read half a slot while read cards are on, as the deal counts them: halfLoad), and the
// cards ready on her row, dealt to her and not started (working on a friend's row means
// started: friendUnstartedWorking).
func friendLanes(s *Snapshot, name string) (working, queued int) {
	row := FriendRow(name)
	work, reads := rowWorking(s, row)
	working = work + reads
	if s.ReadCardsOn() {
		working = halfLoad(work, reads)
	}
	return working, s.Fleet.Count(row, Ready)
}

// queuedIDs is the ids of the cards ready on her row, sorted.
func queuedIDs(s *Snapshot, name string) []string {
	var ids []string
	for _, c := range s.Fleet.Cell(FriendRow(name), Ready) {
		ids = append(ids, c.ID)
	}
	slices.Sort(ids)
	return ids
}

// episodeJudgment is the open judgment of the type on the subject, and the coordinator's
// hold of it (an acknowledgement, or a wait) when one stands; nil for none.
func episodeJudgment(s *Snapshot, typ, subject string) (open, hold *Open) {
	for i := range s.Open {
		o := &s.Open[i]
		if o.Note.Kind == Judgment && o.Note.Type == typ && o.Subject() == subject {
			open = o
			break
		}
	}
	for i := range s.Acked {
		o := &s.Acked[i]
		if o.Note.Type == typ && o.Subject() == subject {
			hold = o
			break
		}
	}
	return open, hold
}

// raiseEpisode writes the judgment of typ on the row, or raises the open one again in
// place with the latest facts and the raises counted in Before, with the push to the
// coordinator (NRaisedAgain), as the coordinator's pass does (reraise). An acknowledgement
// keeps it quiet until the episode ends; a wait keeps it quiet until its review time, in
// running time, and is closed then. It says whether it raised.
func raiseEpisode(p *Plan, s *Snapshot, r TickReq, typ, row, what string, decisions []string, raised int) bool {
	open, hold := episodeJudgment(s, typ, row)
	if hold != nil {
		n := hold.Note
		if n.Review.IsZero() {
			return false // acknowledged: quiet until the episode ends
		}
		base := n.ReviewSet
		if base.IsZero() {
			base = n.At
		}
		if !DueNow(s.Now, n.Review, base, r.Stopped) {
			return false // waited: quiet until its review time
		}
		p.Closes = append(p.Closes, *hold)
	}
	if open == nil {
		n := Note{Kind: Judgment, Type: typ, Primaries: []string{row}, Count: 1, What: what, Who: r.who(), At: s.Now, Marked: true}
		n.Decisions = append([]string(nil), decisions...)
		p.Notes = append(p.Notes, n)
		return true
	}
	n := open.Note
	n.Before, n.What = raised, what
	n.Decisions = append([]string(nil), decisions...)
	p.Updates = append(p.Updates, n)
	to := s.Coordinator
	if to == "" {
		to = "coordinator"
	}
	p.Notes = append(p.Notes, Note{Kind: Happened, Type: NRaisedAgain, Primaries: n.Primaries, Count: n.Count, Who: r.who(), To: to, At: s.Now,
		What: fmt.Sprintf("%s (%s) still holds, open since %s: %s", n.ID, n.Type, stamp(n.At), what),
		Hint: "run: nova-sprint inbox; ack it, or wait it, to quiet it"})
	return true
}

// closeEpisode closes the judgment of the type on the row and the coordinator's hold of it.
func closeEpisode(p *Plan, s *Snapshot, typ, row string) {
	open, hold := episodeJudgment(s, typ, row)
	if open != nil {
		p.Closes = append(p.Closes, *open)
	}
	if hold != nil {
		p.Closes = append(p.Closes, *hold)
	}
}

// widthDecisions are the remedies the friend-width judgment offers.
func widthDecisions(friend string) []string {
	return []string{
		"nova-friend ping --as <coordinator> --to " + friend + " --wake",
		"friend take " + friend + " --all-unstarted",
		"friend down " + friend + " --reason 'free lanes beside queued cards'",
		"ack",
		"wait",
	}
}

// WidthConcurrencyRaises is the raise from which the friend-width judgment says her observed
// concurrency beside her configured width.
const WidthConcurrencyRaises = 3

// TickFriendWidth is the friend-width part of the tick (PartFriendWidth): the comment at
// the top of the file. It is pure: a function of the snapshot and the tick's request.
func TickFriendWidth(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	if s.Fleet == nil || s.Work == nil {
		return p, 0
	}
	write := fleetPropWriter(s, &p)
	after := s.WidthIdleAfter()
	names, seats := checkFriends(s, r)
	for _, f := range names {
		row := FriendRow(f)
		e := episodeProps{friendWidthPrefix, f}
		seat, ok := seats[f]
		working, queued := friendLanes(s, f)
		_, width := friendRoom(seat)
		holds := friendUpForChecks(s, r, seat, ok) && working < width && queued > 0
		if !holds {
			if e.any(s) {
				e.clear(s, write)
				p.Units = append(p.Units, Unit{Key: row, Moved: fmt.Sprintf("friend %s: free lanes beside queued cards no more (working %d of %d, %d queued): the clock is cleared", f, working, width, queued)})
			}
			closeEpisode(&p, s, NFriendWidth, row)
			continue
		}
		since := e.get(s, epSince)
		idle, known := r.running(s.Now, since)
		if since == "" || !known {
			// first seen: the clock starts, and the unit's line is how the start is known
			write(e.name(epSince), stamp(s.Now))
			write(e.name(epMax), itoa(working))
			p.Units = append(p.Units, Unit{Key: row, Moved: fmt.Sprintf("friend %s: %d free lanes beside %d queued cards first seen; judged after %s", f, width-working, queued, after)})
			continue
		}
		maxLanes := e.count(s, epMax)
		if working > maxLanes {
			maxLanes = working
			write(e.name(epMax), itoa(maxLanes))
			p.Units = append(p.Units, Unit{Key: row, Moved: fmt.Sprintf("friend %s ran %d lanes: the most in this episode", f, maxLanes)})
		}
		if idle < after {
			continue
		}
		raised := e.count(s, epRaised)
		if raised > 0 {
			if d, ok := r.running(s.Now, e.get(s, epLast)); ok && d < after {
				continue // raised again at most every WidthIdleAfter
			}
		}
		ids := queuedIDs(s, f)
		what := fmt.Sprintf("friend %s has %d free lanes and %d queued cards for %s (working %d of width %d; queued: %s)",
			f, width-working, queued, idle.Round(time.Second), working, width, Preview(ids, ", "))
		if pinged := e.get(s, epPinged); pinged != "" {
			what += "; her session was pinged by rule " + RuleFriendWidth + " at " + pinged
		}
		if raised+1 >= WidthConcurrencyRaises {
			what += fmt.Sprintf("; over the last %s she never ran more than %d lanes; her width in nova-config is %d; nova-config friend set %s --width %d if that is her real width",
				idle.Round(time.Second), maxLanes, width, f, maxLanes)
		}
		if raiseEpisode(&p, s, r, NFriendWidth, row, what, widthDecisions(f), raised) {
			write(e.name(epRaised), itoa(raised+1))
			write(e.name(epLast), stamp(s.Now))
		}
	}
	return p, 0
}

// ruleFriendWidth: the friend-width judgment. Its one answer by rule is a ping of her
// session with the queued card ids, once an episode (PropFriendWidth pinged); the rest
// (friend take, friend down, wait) is a mind's.
func ruleFriendWidth(s *Snapshot, a *RuleAnswer) {
	a.Rule = RuleFriendWidth
	f, ok := FriendOfRow(a.Subject)
	if !ok {
		left(a, "not a friend's row")
		return
	}
	e := episodeProps{friendWidthPrefix, f}
	if pinged := e.get(s, epPinged); pinged != "" {
		left(a, "her session was pinged at "+pinged+": friend take, friend down or wait is a mind's")
		return
	}
	a.Act, a.from = ActPing, f
	a.Why = fmt.Sprintf("friend %s has free lanes beside queued cards: her session is pinged once with the queued card ids", f)
}

// PartRuleFriendPing is the tick part that pings the friends the friend-width and
// friend-delivery rules answer (TickRuleFriendPing).
const PartRuleFriendPing = "rule friend ping"

// TickRuleFriendPing pings each friend the friend-width and friend-delivery rules answer
// with ActPing: her session is sent the judgment's facts, with her queued (or working) card
// ids in the body, through the tick's ping path (TickReq.PingFriend, which the store's tick
// collects and sends once the part's step commits, as the stall ladder's wakes), the ping
// is recorded on her episode so it goes once, and the log says MOVED rule <name>: pinged
// <friend>. The judgment stays open for the coordinator: the ping is the first remedy, not
// the answer.
func TickRuleFriendPing(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	if s.Fleet == nil {
		return p, 0
	}
	write := fleetPropWriter(s, &p)
	done := map[string]bool{}
	for _, a := range acting(s, r, ActPing) {
		f := a.from
		if done[f+"\x00"+a.Rule] {
			continue
		}
		done[f+"\x00"+a.Rule] = true
		row := FriendRow(f)
		var subject, body string
		var ids []string
		switch a.Rule {
		case RuleFriendWidth:
			ids = queuedIDs(s, f)
			working, queued := friendLanes(s, f)
			subject = fmt.Sprintf("free lanes: friend %s has %d queued cards and %d lanes working", f, queued, working)
			body = fmt.Sprintf("Your sprint row holds %d cards you have not started: %s. You are working %d lanes. Start them, or say why not; friend take hands them back for another friend.", queued, strings.Join(ids, ", "), working)
		default:
			for _, c := range s.Fleet.Cell(row, Working) {
				ids = append(ids, c.ID)
			}
			slices.Sort(ids)
			subject = fmt.Sprintf("no delivery: friend %s has %d lanes busy and has reported nothing for %s", f, len(ids), s.DeliveryWindow())
			body = fmt.Sprintf("Your sprint row holds %d working cards (%s) and no report of yours has arrived for %s. Report each (ok, failed or HOLD), or say what is blocking you.", len(ids), strings.Join(ids, ", "), s.DeliveryWindow())
		}
		if r.PingFriend != nil {
			_ = r.PingFriend(f, subject, body) // ignored: the ping is best effort; the record stands and the judgment stays
		}
		e := episodeProps{friendWidthPrefix, f}
		if a.Rule == RuleFriendDelivery {
			e = episodeProps{friendDeliveryPrefix, f}
		}
		write(e.name(epPinged), stamp(s.Now))
		p.Units = append(p.Units, Unit{Key: row, Moved: fmt.Sprintf("rule %s: pinged %s with %d cards (%s)", a.Rule, f, len(ids), Preview(ids, ", "))})
	}
	return p, 0
}
