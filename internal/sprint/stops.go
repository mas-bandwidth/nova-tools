package sprint

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The machine's automatic stops (the owner, 2026-10-10 ~04:18Z: "sprint doctor can check
// this, but i still dislike these silent stops/failures"; and the night before, of a rest the
// machine made on an estimate: "it should raise it to you as a thing to do, but not do it
// automatically"). The night of 2026-10-09 lost its throughput to stops the machine made and
// told no one: two machine rows down since they never beat, a hard pin waiting on a friend
// who was down, a friend's stall judgment closed in the tick that raised it, 111 judgments
// waiting on the seat (the oldest 51 hours) quieted by one acknowledgement. Each was found by
// a person reading the dashboard, never by a push.
//
// Every automatic stop is now a signal to the seat while it holds:
//
//   - the stops that had none are judgments of the coordinator's pass (StopTypes): raised
//     once an episode, raised again every PassEvery of running time while the stop holds
//     (reraise, the push NRaisedAgain), closed when it clears. Each says what stopped, the
//     evidence, the effect (lanes or cards idled) and the verb that undoes it;
//   - the judgments late on the coordinator escalate (BehindLevel): every BehindEscalateEvery
//     of running time is a new level and a new episode, so no acknowledgement outlives its
//     level, and the overdue line itself is pushed (it is addressed to the coordinator);
//   - every stop, whichever part signals it, is listed with its age by LiveStops: the tick
//     keeps them, with the judgments waiting on the seat (SeatWaitsOf), in the stops record
//     the dashboard and `nova-sprint doctor` read.
//
// The model is tla/CoordinatorPass.tla: Kind = "stop" (StopSignalled, its witness
// silentstop) and Kind = "behind" (EscalatedPastAck, its witness ackforever).

// The stop judgments of the coordinator's pass.
const (
	// NStopMemberDown is a fleet machine down, or never beaten, or held adopting past
	// StopAdoptAfter, that the coordinator did not hold: its width idles.
	NStopMemberDown = "a fleet member is down and the coordinator did not hold it"
	// NStopPinWaits is a ready card hard-pinned to a friend who is not up: it, and every
	// card that needs it, waits for her alone.
	NStopPinWaits = "a card pinned to one friend waits while she is not up"
	// NFriendStalled is the stall ladder's third rung (friend_stall.go): two wakes
	// unanswered. Its own type, kept by the pass while her rung stands at 3 or above, so
	// the tick's check does not close it in the tick that raised it (as it did as NStalled,
	// whose findings never name a friend).
	NFriendStalled = "a friend stalled: two wakes unanswered"
)

// StopTypes are the stop judgments, kept by the coordinator's pass as its own (PassTypes).
var StopTypes = []string{NStopMemberDown, NStopPinWaits, NFriendStalled}

const (
	// StopMemberAfter is how long a member must have been down, in running time, before
	// the pass raises it: the status transitions' dwell, so a beat that lapses and comes
	// back raises nothing.
	StopMemberAfter = StatusDwell
	// StopAdoptAfter is how long a member may be held adopting (fleet_back.go) before the
	// hold is a stop: an adoption is a play of minutes.
	StopAdoptAfter = 15 * time.Minute
	// BehindEscalateEvery is the running time between two levels of the judgments late on
	// the coordinator: a new level is a new judgment, whatever quieted the one before.
	BehindEscalateEvery = 3 * PassEvery
)

func init() {
	TickDecisions[NStopMemberDown] = []string{"ack", "wait"}
	TickDecisions[NStopPinWaits] = []string{"ack", "wait"}
	TickDecisions[NFriendStalled] = []string{"ack", "wait"}
}

// Stop is one automatic stop that holds: its kind (a word), what it stops (its subject),
// since when, what it says (the evidence), its effect, the verb that undoes it, and the
// signal that carries it to the seat (a judgment type, or the note the part writes).
type Stop struct {
	Kind    string    `json:"kind"`
	Subject string    `json:"subject"`
	Since   time.Time `json:"since,omitzero"`
	What    string    `json:"what"`
	Effect  string    `json:"effect"`
	Undo    string    `json:"undo"`
	Signal  string    `json:"signal"`
	// the condition the pass raises, for the stops whose signal is the pass's own
	typ       string
	stream    string
	primaries []string
	decisions []string
}

// The kinds of stop, as LiveStops names them.
const (
	StopKindMemberDown    = "member-down"
	StopKindMemberAdopt   = "member-adopting"
	StopKindPinWaits      = "pin-waits"
	StopKindFriendStalled = "friend-stalled"
	StopKindFriendDown    = "friend-stall-down"
	StopKindRouteRest     = "route-rest"
	StopKindProviderRest  = "provider-rest"
	StopKindStreamStopped = "stream-stopped"
)

// Age is how long the stop has held at now, to the second; zero when its start is not known.
func (x Stop) Age(now time.Time) time.Duration {
	if x.Since.IsZero() || now.Before(x.Since) {
		return 0
	}
	return now.Sub(x.Since).Truncate(time.Second)
}

// Line is the stop as one line: kind, subject, age, what, effect and the undo verb.
func (x Stop) Line(now time.Time) string {
	age := "-"
	if !x.Since.IsZero() {
		age = x.Age(now).String()
	}
	return fmt.Sprintf("%s %s age=%s: %s; effect: %s; undo: %s", x.Kind, x.Subject, age, x.What, x.Effect, x.Undo)
}

// LiveStops is every automatic stop that holds at s.Now, in kind and subject order: the
// pass's own (memberStops, pinStops, friendStallStops) and those another part signals
// (the rests, a stream stopped by the lander), listed so the seat sees each with its age.
func LiveStops(s *Snapshot, r TickReq) []Stop {
	if s == nil {
		return nil
	}
	var out []Stop
	out = append(out, memberStops(s, r)...)
	out = append(out, pinStops(s, r)...)
	out = append(out, friendStallStops(s)...)
	out = append(out, restStops(s)...)
	out = append(out, streamStops(s)...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Subject < out[j].Subject
	})
	return out
}

// stopConds is the pass's conditions: one for each live stop the pass signals itself.
func stopConds(s *Snapshot, r TickReq) []cond {
	var out []cond
	for _, x := range LiveStops(s, r) {
		if x.typ == "" {
			continue
		}
		out = append(out, cond{typ: x.typ, stream: x.stream, primaries: x.primaries,
			what:      fmt.Sprintf("%s; effect: %s; undo: %s", x.What, x.Effect, x.Undo),
			decisions: x.decisions})
	}
	return out
}

// memberStops is each fleet machine whose status is not up while the coordinator did not
// hold it, down for StopMemberAfter of running time, or never beaten; and each held adopting
// (fleet_back.go) past StopAdoptAfter. A hold the coordinator made (fleet down) is hers, and
// a hold by fleet sync follows the inventory she keeps: neither is a stop. With the fleet's
// work switched off no member's width idles, and none is raised.
func memberStops(s *Snapshot, r TickReq) []Stop {
	if s.Fleet == nil || s.FleetOff() {
		return nil
	}
	var out []Stop
	for _, m := range s.Members() {
		ctl := s.MemberCtl(m)
		if ctl == nil || ctl.F("status") == Up {
			continue
		}
		width := s.Width(m)
		since := stampAt(ctl, "since")
		effect := fmt.Sprintf("its width %d idles", width)
		if n := len(s.Fleet.Cell(m, Ready)) + len(s.Fleet.Cell(m, Working)); n > 0 {
			effect += fmt.Sprintf(", %d cards on its row", n)
		}
		if ctl.F("held") != "" {
			if ctl.F(FieldHeldBy) != HeldByAdopt {
				continue // the coordinator's hold, or the inventory's
			}
			at := stampAt(ctl, FieldAdoptSince)
			if at.IsZero() {
				at = stampAt(ctl, "held")
			}
			d, ok := r.running(s.Now, stamp(at))
			if at.IsZero() || !ok || d < StopAdoptAfter {
				continue
			}
			out = append(out, Stop{Kind: StopKindMemberAdopt, Subject: m, Since: at,
				What:   fmt.Sprintf("member %s has been held adopting since %s, past %s", m, stamp(at), StopAdoptAfter),
				Effect: effect, Undo: "nova-sprint fleet up " + m, Signal: NStopMemberDown,
				typ: NStopMemberDown, primaries: []string{m},
				decisions: []string{"fleet up " + m, "fleet down " + m, "ack", "wait"}})
			continue
		}
		// the evidence: its beat, when the binding read the beats (presence.go); a member
		// that never beat is raised once it has been on the table a dwell, at once when the
		// time it came on is not known
		why := "its status is " + orDash(ctl.F("status"))
		dwell := true
		if r.Beats != nil {
			if b := r.Beats[m]; b.Beaten() {
				// an absolute time, never an age: the text changes only when the stop does, so
				// neither the judgment nor the stops record is rewritten every tick
				why = "its last beat was at " + stamp(b.At)
				if since.IsZero() {
					since = b.At
				}
			} else {
				why, dwell = "it has never beaten", !since.IsZero()
			}
		}
		if dwell {
			if d, ok := r.running(s.Now, stamp(since)); since.IsZero() || !ok || d < StopMemberAfter {
				continue
			}
		}
		out = append(out, Stop{Kind: StopKindMemberDown, Subject: m, Since: since,
			What:   fmt.Sprintf("member %s is %s and the coordinator did not hold it: %s", m, orDash(ctl.F("status")), why),
			Effect: effect,
			Undo:   "start its member loop (it comes up when it beats), or nova-sprint fleet down " + m + " to hold it off",
			Signal: NStopMemberDown, typ: NStopMemberDown, primaries: []string{m},
			decisions: []string{"fleet down " + m, "fleet up " + m, "ack", "wait"}})
	}
	return out
}

// pinStops is each ready primary hard-pinned to one friend (OnlyFriend) while she is not up:
// no deal gives it to anyone else, so it and every card that needs it wait for her.
func pinStops(s *Snapshot, r TickReq) []Stop {
	if s.Work == nil || r.Friends == nil {
		return nil // the friends not read: who is up is not known here
	}
	seats := map[string]FriendSeat{}
	for _, f := range r.Friends {
		seats[f.Name] = f
	}
	var out []Stop
	for _, pr := range s.Work.Column(Ready) {
		if pr == nil || IsSentinel(pr) || !OnlyFriend(pr) || StreamHeld(s, pr.Row) {
			continue
		}
		name, _ := FriendCard(pr)
		if name == "" {
			continue
		}
		seat, known := seats[name]
		if known && seat.Status == Up {
			continue
		}
		// her status word alone: the evidence behind it (seat.Why) carries ages, and the text
		// changes only when the stop does
		status := "not on the roster"
		if known {
			status = orDash(seat.Status)
		}
		what := fmt.Sprintf("%s is pinned to friend %s alone and waits: she is %s", pr.ID, name, status)
		behind := needing(s, pr.ID)
		effect := "1 card waits"
		if len(behind) > 0 {
			effect = fmt.Sprintf("1 card waits, and %d that need it (%s)", len(behind), Preview(behind, ", "))
		}
		out = append(out, Stop{Kind: StopKindPinWaits, Subject: pr.ID, Since: stampAt(pr, "since"),
			What: what, Effect: effect,
			Undo:   "nova-sprint unpin " + pr.ID + " --reason '<why>', or bring her up",
			Signal: NStopPinWaits, typ: NStopPinWaits, stream: pr.Row, primaries: []string{pr.ID},
			decisions: []string{"unpin " + pr.ID + " --reason '<why>'", "ack", "wait"}})
	}
	return out
}

// needing is the open primaries whose needs name id, in id order.
func needing(s *Snapshot, id string) []string {
	var out []string
	for _, c := range s.Work.Column(Waiting, Ready) {
		if c != nil && slices.Contains(Split(c.F("needs")), id) {
			out = append(out, c.ID)
		}
	}
	sort.Strings(out)
	return out
}

// friendStallStops is each friend the stall ladder (friend_stall.go) has climbed past its
// wakes: at rung 3 or above while she holds cards, the pass keeps NFriendStalled; marked
// down by it (rung 5), the down is listed until her activity releases her.
func friendStallStops(s *Snapshot) []Stop {
	if s.Fleet == nil {
		return nil
	}
	var out []Stop
	for _, row := range s.Fleet.Rows() {
		f, ok := FriendOfRow(row)
		if !ok {
			continue
		}
		if v, _ := s.Fleet.Prop(PropFriendStallDown(f)); v != "" {
			at, _ := time.Parse(time.RFC3339, v) // ignored: an unreadable stamp lists the down with no age
			out = append(out, Stop{Kind: StopKindFriendDown, Subject: f, Since: at,
				What:   fmt.Sprintf("friend %s was marked down by the stall ladder (reason stalled) at %s", f, v),
				Effect: "no card is dealt to her until her activity releases her",
				Undo:   "nova-friend ping --as <coordinator> --to " + f + " --wake; her activity releases her", Signal: NFriendStall})
		}
		v, _ := s.Fleet.Prop(PropFriendStallRung(f))
		rung, _ := strconv.Atoi(v) // ignored: no rung, or one that does not read, is rung 0
		cards := len(s.Fleet.Cell(row, Ready)) + len(s.Fleet.Cell(row, Working))
		if rung < 3 || cards == 0 {
			continue
		}
		did := "two wakes unanswered"
		switch {
		case rung >= 5:
			did += ", her unstarted cards taken back and she was marked down"
		case rung == 4:
			did += ", her unstarted cards taken back"
		}
		out = append(out, Stop{Kind: StopKindFriendStalled, Subject: f,
			What:   fmt.Sprintf("friend %s stalled: the stall ladder stands at rung %d of 5 (%s)", f, rung, did),
			Effect: fmt.Sprintf("%d cards on her row wait", cards),
			Undo:   "nova-friend ping --as <coordinator> --to " + f + " --wake, or nova-sprint friend take " + f + " --all-unstarted",
			Signal: NFriendStalled, typ: NFriendStalled, primaries: []string{f},
			decisions: []string{"nova-friend ping --as <coordinator> --to " + f + " --wake", "friend take " + f + " --all-unstarted", "friend down " + f + " --reason 'stalled'", "ack", "wait"}})
	}
	return out
}

// restStops is each route and provider rest that holds at s.Now (route_rest.go,
// provider_funds.go): their signals are the rests' own (a note, and a judgment for a
// provider's funds or key), and the rest decision is theirs; listed here with their age.
func restStops(s *Snapshot) []Stop {
	if s.Fleet == nil {
		return nil
	}
	var out []Stop
	provs := ProviderRests(s.Fleet)
	for _, p := range slices.Sorted(maps.Keys(provs)) {
		x := provs[p]
		if !x.Resting(s.Now) {
			continue
		}
		undo := "it ends at " + x.UntilSaid()
		switch {
		case x.Funds():
			undo = "nova-sprint funded " + p + " --reason '<the payment>'"
		case x.Cause == RestAuth && x.Open():
			undo = "the owner replaces the key, then nova-sprint routes wake " + p + " --reason '<the key replaced>'"
		}
		out = append(out, Stop{Kind: StopKindProviderRest, Subject: p, Since: x.At,
			What:   fmt.Sprintf("provider %s rests until %s: %s", p, x.UntilSaid(), x.Said()),
			Effect: "the deal draws no work card on its routes", Undo: undo, Signal: NProviderRested})
	}
	byProv := rule3Rests(s.Fleet)
	for _, p := range slices.Sorted(maps.Keys(byProv)) {
		for _, route := range slices.Sorted(maps.Keys(byProv[p])) {
			x := byProv[p][route]
			if !x.Resting(s.Now) {
				continue
			}
			out = append(out, Stop{Kind: StopKindRouteRest, Subject: route, Since: x.At,
				What:   fmt.Sprintf("route %s rests until %s: %s", route, stamp(x.Until), x.Said()),
				Effect: "the deal draws no work card on it", Undo: "it ends at " + stamp(x.Until), Signal: NRouteRested})
		}
	}
	return out
}

// streamStops is each stream the lander stopped (steps_merge.go): its judgment is the
// lander's, listed here with its age.
func streamStops(s *Snapshot) []Stop {
	if s.Merge == nil {
		return nil
	}
	var out []Stop
	for _, st := range s.Merge.Rows() {
		ctl := s.StreamCtl(st)
		if ctl == nil || ctl.F("state") != StreamStopped {
			continue
		}
		out = append(out, Stop{Kind: StopKindStreamStopped, Subject: st, Since: stampAt(ctl, "since"),
			What:   fmt.Sprintf("stream %s stopped landing (%s)", st, orDash(ctl.F("cause"))),
			Effect: fmt.Sprintf("%d cards wait to merge", s.Merge.Count(st, Queued)),
			Undo:   "nova-sprint resume --stream " + st, Signal: "the lander's judgment"})
	}
	return out
}

// SeatWaits is what waits on the seat: the open judgments (the sprint is done has no due
// time and is not counted), those past their due time, and the oldest, with its age; and
// the level the pass escalated the judgments late on the coordinator to (BehindLevel, on its
// open NCoordinatorBehind).
type SeatWaits struct {
	Judgments  int       `json:"judgments"`
	Overdue    int       `json:"overdue"`
	OldestID   string    `json:"oldest_id,omitempty"`
	OldestType string    `json:"oldest_type,omitempty"`
	OldestAt   time.Time `json:"oldest_at,omitzero"`
	Level      int       `json:"level"`
}

// SeatWaitsOf is the judgments waiting on the seat at s.Now.
func SeatWaitsOf(s *Snapshot, r TickReq) SeatWaits {
	var w SeatWaits
	seen := map[string]bool{}
	for _, o := range s.Open {
		n := o.Note
		if n.Kind != Judgment || n.Type == NSprintDone || seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		w.Judgments++
		if n.Type == NCoordinatorBehind {
			// the level the pass escalated the late ones to opens its text
			if l, err := strconv.Atoi(strings.TrimPrefix(behindLevelWord(n.What), behindLevelPrefix)); err == nil {
				w.Level = max(w.Level, l)
			}
		}
		if JudgmentOverdue(s, r, n) {
			w.Overdue++
		}
		if w.OldestAt.IsZero() || n.At.Before(w.OldestAt) {
			w.OldestID, w.OldestType, w.OldestAt = n.ID, n.Type, n.At
		}
	}
	return w
}

// Line is the seat's waits as one line.
func (w SeatWaits) Line(now time.Time) string {
	if w.Judgments == 0 {
		return "no judgment waits on the seat"
	}
	line := fmt.Sprintf("%d judgments wait on the seat, %d past their deadline; the oldest %s (%s) for %s", w.Judgments, w.Overdue, w.OldestID, w.OldestType, now.Sub(w.OldestAt).Truncate(time.Second))
	if w.Level > 0 {
		line += fmt.Sprintf("; escalated to level %d", w.Level)
	}
	return line
}

// BehindLevel is the escalation level of the judgments late on the coordinator: one level
// for every BehindEscalateEvery of running time since since, the overdue line of the oldest
// of them the pass counts (behindConds).
func BehindLevel(s *Snapshot, r TickReq, since time.Time) int {
	d, ok := r.running(s.Now, stamp(since))
	if !ok || d < 0 {
		return 0
	}
	return int(d / BehindEscalateEvery)
}

// behindLevelPrefix opens the text of the judgments late on the coordinator: its level,
// part of the condition (condKey), so a new level is a new judgment.
const behindLevelPrefix = "level "

// behindLevelWord is the level a text of NCoordinatorBehind opens with ("level 2"), or "".
func behindLevelWord(what string) string {
	if !strings.HasPrefix(what, behindLevelPrefix) {
		return ""
	}
	word, _, _ := strings.Cut(what, ":")
	return word
}

// The re-push of the stops is one digest (cold reader B, PR 5548: forty live stops were forty
// "still holds" lines every ten minutes). A stop's first raise is its own judgment, pushed;
// after it, every PassEvery of running time while any stop judgment has gone that long without
// a push, the pass writes one happened note to the coordinator, NStopsDigest: how many stops
// hold by kind, the StopsDigestOldest oldest with their undo verbs, and `nova-sprint doctor`
// for the full list. The clock is an acknowledgement, NStopsDigestClock, its At the last
// digest's, closed and written again with each digest (shown in no inbox, as the empty-row
// clock). A stop acknowledged, or waited to a review time not reached, is left out.
const (
	// NStopsDigest is the digest of the automatic stops still holding.
	NStopsDigest = "automatic stops still hold"
	// NStopsDigestClock is the digest's clock: an acknowledgement, never a judgment.
	NStopsDigestClock = "the automatic stops' digest"
	// StopsDigestOldest is how many of the oldest stops the digest names.
	StopsDigestOldest = 5
)

// stopKindOf is the short word of a stop judgment's type, as the digest counts them.
var stopKindOf = map[string]string{
	NStopMemberDown: StopKindMemberDown,
	NStopPinWaits:   StopKindPinWaits,
	NFriendStalled:  StopKindFriendStalled,
}

// stopsDigest writes the tick's digest of the stops still holding when one is due (above).
func stopsDigest(p *Plan, s *Snapshot, r TickReq) {
	var clock *Open
	for i, o := range s.Acked {
		if o.Note.Type == NStopsDigestClock {
			clock = &s.Acked[i]
		}
	}
	lastDigest := time.Time{}
	if clock != nil {
		lastDigest = clock.Note.At
	}
	seen := map[string]bool{}
	var stops []Note
	due := false
	for _, o := range s.Open {
		n := o.Note
		if n.Kind != Judgment || !slices.Contains(StopTypes, n.Type) || seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		if !n.Review.IsZero() && s.Now.Before(n.Review) {
			continue // waited: quiet until its review time
		}
		stops = append(stops, n)
		last := n.At
		if lastDigest.After(last) {
			last = lastDigest
		}
		if d, ok := r.running(s.Now, stamp(last)); ok && d >= PassEvery {
			due = true
		}
	}
	if !due || len(stops) == 0 {
		return
	}
	if clock != nil {
		if d, ok := r.running(s.Now, stamp(clock.Note.At)); !ok || d < PassEvery {
			return
		}
	}
	byKind := map[string]int{}
	for _, n := range stops {
		byKind[cmpOr(stopKindOf[n.Type], n.Type)]++
	}
	var counts []string
	for _, k := range slices.Sorted(maps.Keys(byKind)) {
		counts = append(counts, fmt.Sprintf("%d %s", byKind[k], k))
	}
	sort.SliceStable(stops, func(i, j int) bool { return stops[i].At.Before(stops[j].At) })
	var oldest []string
	for _, n := range stops[:min(len(stops), StopsDigestOldest)] {
		undo := "see its judgment"
		if _, u, ok := strings.Cut(n.What, "; undo: "); ok {
			undo = u
		}
		oldest = append(oldest, fmt.Sprintf("%s %s %s since %s, undo: %s", n.ID, cmpOr(stopKindOf[n.Type], n.Type), strings.Join(n.Primaries, ","), stamp(n.At), undo))
	}
	to := s.Coordinator
	if to == "" {
		to = "coordinator"
	}
	p.Notes = append(p.Notes, Note{Kind: Happened, Type: NStopsDigest, Who: r.who(), To: to, At: s.Now,
		What: fmt.Sprintf("%d automatic stops still hold (%s); the %d oldest: %s", len(stops), strings.Join(counts, ", "), len(oldest), strings.Join(oldest, "; ")),
		Hint: "the full list: nova-sprint doctor; ack or wait a stop's judgment to quiet it"})
	if clock != nil {
		p.Closes = append(p.Closes, *clock)
	}
	p.Notes = append(p.Notes, Note{Kind: Acknowledged, Type: NStopsDigestClock, What: "digest", Who: r.who(), At: s.Now, SprintLevel: true})
}

// cmpOr is a when it is not empty, else b.
func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
