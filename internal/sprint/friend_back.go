package sprint

import (
	"fmt"
	"strings"
	"time"
)

// A friend back up (docs/SPEC-SPRINT.md section 1, "A friend back up"; the owner,
// 2026-10-05 ~9:35 AM ET: "Just like you notice that friends are down, you should notice
// they are back up automatically."). A friend held (friend down, hold <friend>) or down by
// her beat's word (friend beat --until) for a cause that ends is probed by the machine,
// and brought up when the probe passes:
//
//   - the cause is the hold's reason or the beat's: out of credit, a usage limit, deaf, or
//     any hold with --until, the time it ends (HoldCause); a hold by hand, with no --until
//     and no such reason, has none, and the machine never releases it;
//   - the probe is a wake to her session on a backoff from the hold (FriendBackFirst,
//     doubling to FriendBackMax), and at the known end; it passes on her session's own
//     evidence after the last wake (so after the hold began and its end), in its window (a wake ping her
//     session answered, the coordinator's friend health --state up, or a card of hers
//     finished; her daemon's pong and her beat are none), while no beat of hers says she
//     still refuses (a usage limit or out of credits with an end not yet reached). Her
//     session's answer is a turn she ran: for out of credit, her next run not refusing
//     for funds;
//   - the return releases the hold at her row's width (the roster's, nova-config's by
//     friend sync), before the tick's deal and level read the friends, so her queue is
//     levelled the same tick, and tells the coordinator once (NFriendBack).

const (
	// NFriendBack is the happened note to the coordinator of a friend brought back up:
	// "<friend> is back up: <cause> ended".
	NFriendBack = "a friend is back up"
	// FriendBackFirst is how long after a hold the first wake goes to her session, and
	// the first wait of the backoff; each wake after doubles it, to FriendBackMax.
	FriendBackFirst = 5 * time.Minute
	// FriendBackMax is the longest wait between two wakes.
	FriendBackMax = time.Hour
)

// The causes that end, as a hold's or a beat's reason says them (HoldCause).
const (
	CauseCredit = "out of credit"
	CauseLimit  = "usage limit"
	CauseDeaf   = "deaf"
)

// HoldCause is the cause a hold or a beat's down word ends with, "" for none: out of
// credit (a reason naming credit or funds), a usage limit (a reason naming a limit),
// deaf, else, with until set, the reason (or "the hold until <until>"). A hold with
// no until whose reason names none of them is a hold by hand.
func HoldCause(reason string, until time.Time) string {
	r := strings.ToLower(reason)
	switch {
	case strings.Contains(r, "credit") || strings.Contains(r, "funds"):
		return CauseCredit
	case strings.Contains(r, "limit"):
		return CauseLimit
	case strings.Contains(r, "deaf"):
		return CauseDeaf
	case until.IsZero():
		return ""
	case strings.TrimSpace(reason) != "":
		return strings.TrimSpace(reason)
	}
	return "the hold until " + until.UTC().Format(time.RFC3339)
}

// FriendHold is one episode of a friend not up for a cause: a hold (Held, from At) or
// her beat's down word (At is its Until), its reason and end, and the probe's backoff
// in it: how many wakes went and when the last did.
type FriendHold struct {
	Friend string
	Held   bool
	At     time.Time
	Reason string
	Until  time.Time
	Probes int
	Probed time.Time
}

// Cause is the episode's cause (HoldCause), "" for a hold by hand.
func (h FriendHold) Cause() string { return HoldCause(h.Reason, h.Until) }

// NextProbe is when the next wake is due: at the known end until a wake went at or
// after it, else FriendBackFirst after the hold, then each wait double the last, to
// FriendBackMax.
func (h FriendHold) NextProbe() time.Time {
	if !h.Until.IsZero() && h.Probed.Before(h.Until) {
		return h.Until
	}
	if h.Probes == 0 {
		return h.At.Add(FriendBackFirst)
	}
	wait := FriendBackMax
	if h.Probes < 8 {
		wait = min(FriendBackFirst<<h.Probes, FriendBackMax)
	}
	return h.Probed.Add(wait)
}

// ProbeDue says a wake is due at now: the episode has a cause and its next probe is
// not after now.
func (h FriendHold) ProbeDue(now time.Time) bool {
	return h.Cause() != "" && !now.Before(h.NextProbe())
}

// FriendHoldOf is the episode a friend is in, ok false for none: her hold (Held, At,
// Reason, Until as the roster keeps them), else her beat's down word. A hold by hand is
// an episode with no cause.
func FriendHoldOf(friend string, held bool, at time.Time, reason string, until time.Time, b Beat) (FriendHold, bool) {
	switch {
	case held:
		return FriendHold{Friend: friend, Held: true, At: at, Reason: reason, Until: until}, true
	case b.SaysDown():
		return FriendHold{Friend: friend, At: b.Friend.Until, Reason: b.Friend.Reason, Until: b.Friend.Until}, true
	}
	return FriendHold{}, false
}

// FriendBackPresence is the presence the friends' rule reads (FriendStatus): her beat's
// down word dropped once its end has passed and her session gave evidence after it (a
// wake ping her session answered, under the seat's generation, or a card of hers
// finished), so a word her runner never withdrew does not keep her down after the
// limit reset; with no such evidence the word stands.
func FriendBackPresence(p FriendPresence, now time.Time) FriendPresence {
	if !p.Beat.SaysDown() || now.Before(p.Beat.Friend.Until) || !sessionSince(p, p.Beat.Friend.Until) {
		return p
	}
	rep := *p.Beat.Friend
	rep.Until, rep.Reason = time.Time{}, ""
	p.Beat.Friend = &rep
	return p
}

// sessionSince says her session gave evidence after since: a wake ping it answered
// under the seat's generation, or a card of hers finished.
func sessionSince(p FriendPresence, since time.Time) bool {
	pong := p.Health.State == Up && p.Health.Generation == p.Generation && p.Health.Seen.After(since)
	return pong || p.Finished.After(since)
}

// FriendBackPassed says the probe passed at now: the episode has a cause, its known end
// has come, a wake went and her session gave evidence after it (so after the hold began
// and after its end: a pong a second after the hold is no answer to a probe), no beat
// of hers says she still refuses, and with the hold released she is up by the friends'
// rule (FriendStatus: the evidence is in its window).
func FriendBackPassed(h FriendHold, p FriendPresence, now time.Time) bool {
	if h.Cause() == "" || h.Probes == 0 || (!h.Until.IsZero() && now.Before(h.Until)) {
		return false
	}
	since := h.Probed
	for _, t := range []time.Time{h.At, h.Until} {
		if t.After(since) {
			since = t
		}
	}
	if !sessionSince(p, since) {
		return false
	}
	p.Held = false
	return FriendStatus(FriendBackPresence(p, now), now) == Up
}

// FriendBackReq is a friend's return, as the store's tick read of the friends found it:
// the friend and the cause that ended.
type FriendBackReq struct {
	Friend, Cause string
}

// FriendBackWhat is the note's line: "<friend> is back up: <cause> ended".
func FriendBackWhat(r FriendBackReq) string {
	return fmt.Sprintf("%s is back up: %s ended", r.Friend, r.Cause)
}

// FriendBack is the return's note: one happened note to the coordinator on her row; the
// store releases the hold beside it.
func FriendBack(s *Snapshot, r FriendBackReq) Plan {
	n := happened(NFriendBack, "", s.Now, FriendRow(r.Friend))
	n.Who, n.To, n.What = MachineActor, s.Coordinator, FriendBackWhat(r)
	return Plan{Notes: []Note{n}}
}
