package sprint

import (
	"context"
	"strings"
	"time"
)

// A friend brought back up (docs/SPEC-SPRINT.md section 1, "A friend brought
// back up"). A hold or a down for a cause that ends is probed by the tick and
// released when the probe passes. A hold by hand, with no such cause, is not.

// How often the tick probes a friend held or down for a cause that ends.
// The first probe waits FriendBackBase from the cause. Each failed probe
// doubles the wait, up to FriendBackCap. The known end (an until) is probed
// when it arrives, as well as on that backoff.
const (
	FriendBackBase = time.Minute
	FriendBackCap  = 16 * time.Minute
)

// NFriendBack is the happened note to the coordinator when a friend is brought
// back up: "<friend> is back up: <cause> ended".
const NFriendBack = "friend back"

// FriendProbe is one probe of a friend. Session says her session answered the
// wake ping. Daemon says only her daemon answered; that does not count.
// FundsRefuse says her next run would still refuse for funds, which keeps an
// out-of-credit cause in place.
type FriendProbe struct {
	Session     bool
	Daemon      bool
	FundsRefuse bool
}

// FriendBack is one friend the tick is bringing back up, for the friend-back
// part. The store releases her before the parts run and leaves this empty, so
// the part plans nothing on a tick; a caller that fills it gets the notes.
type FriendBack struct {
	Name  string
	Cause string
}

type friendProbeKey struct{}

// FriendProbeFn answers a probe for a friend. The bool says it answered; false
// leaves the answer to the records.
type FriendProbeFn func(friend string) (FriendProbe, bool)

// WithFriendProbe returns a context the tick reads its probes from. Tests set
// it. A tick with none reads the friend's health and beat.
func WithFriendProbe(ctx context.Context, fn FriendProbeFn) context.Context {
	return context.WithValue(ctx, friendProbeKey{}, fn)
}

// FriendProbeFrom is the probe function on ctx, or nil.
func FriendProbeFrom(ctx context.Context) FriendProbeFn {
	fn, _ := ctx.Value(friendProbeKey{}).(FriendProbeFn)
	return fn
}

// FriendEnding says a hold or a down ends on its own. The cause is the word
// the note uses. Out of credit, a usage limit, and deaf end even with no
// until. Any until ends, whatever the reason. Anything else is a hold by hand.
func FriendEnding(reason string, until time.Time) (cause string, ok bool) {
	r := strings.TrimSpace(reason)
	low := strings.ToLower(r)
	switch {
	case strings.Contains(low, "credit"):
		return "out of credit", true
	case strings.Contains(low, "deaf"):
		return "deaf", true
	case strings.Contains(low, "usage limit") || strings.Contains(low, "rate limit") || strings.Contains(low, "rate limited") || low == "limit":
		return "usage limit", true
	case !until.IsZero():
		if r == "" {
			return "until " + until.UTC().Truncate(time.Second).Format(time.RFC3339), true
		}
		return r, true
	default:
		return "", false
	}
}

// FriendBackWait is the wait after failures failed probes: FriendBackBase,
// doubled each time, capped at FriendBackCap. Fewer than one failure waits
// FriendBackBase.
func FriendBackWait(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	shift := failures - 1
	if shift > 4 {
		shift = 4
	}
	d := FriendBackBase << shift
	if d > FriendBackCap || d <= 0 {
		return FriendBackCap
	}
	return d
}

func backSec(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC().Truncate(time.Second)
}

// FriendBackDue says a probe is due at now. The first is due FriendBackBase
// after the cause, not at once. A probe already made before the cause does
// not count. At the known end a probe is due when none has been made since
// that end. After a probe, the next waits FriendBackWait.
func FriendBackDue(now, causeAt, until, probeAt time.Time, failures int) bool {
	now, causeAt, until, probeAt = backSec(now), backSec(causeAt), backSec(until), backSec(probeAt)
	if causeAt.IsZero() {
		causeAt = now
	}
	if probeAt.Before(causeAt) {
		probeAt = time.Time{}
		failures = 0
	}
	if !until.IsZero() && !now.Before(until) && (probeAt.IsZero() || probeAt.Before(until)) {
		return true
	}
	from, wait := causeAt, FriendBackBase
	if !probeAt.IsZero() {
		from, wait = probeAt, FriendBackWait(failures)
	}
	return !now.Before(from.Add(wait))
}

// FriendProbePasses says the probe lets her work. Her session must have
// answered. A daemon pong does not. An out-of-credit cause also needs her
// next run not to refuse for funds.
func FriendProbePasses(p FriendProbe, credit bool) bool {
	if !p.Session {
		return false
	}
	if credit && p.FundsRefuse {
		return false
	}
	return true
}

// ProbeFromRecords is the probe when no function was injected. Her session
// answered when friend health says up, under the current seat generation,
// within FriendPongWindow, and not before the cause: a pong from before the
// hold is not an answer. A daemon pong (asleep) is not a session. Out of
// credit still refuses for funds while her beat or her health says credit,
// and while her session has not answered.
func ProbeFromRecords(now, causeAt time.Time, credit bool, health FriendHealth, generation uint64, report *FriendReport) FriendProbe {
	now, causeAt = backSec(now), backSec(causeAt)
	seen := backSec(health.Seen)
	session := health.State == Up && health.Generation == generation && !seen.IsZero() && !seen.After(now) && now.Sub(seen) < FriendPongWindow && !seen.Before(causeAt)
	p := FriendProbe{Session: session, Daemon: health.State == DaemonPong}
	if credit && (!session || reasonSaysCredit(health.Reason) || (report != nil && reasonSaysCredit(report.Reason))) {
		p.FundsRefuse = true
	}
	return p
}

func reasonSaysCredit(reason string) bool {
	return strings.Contains(strings.ToLower(reason), "credit")
}

// FriendBackNote is the one note to the coordinator for a friend brought back up.
func FriendBackNote(name, cause, who, to string, at time.Time) Note {
	return Note{
		Kind: Happened,
		Type: NFriendBack,
		Who:  who,
		To:   to,
		At:   at,
		What: name + " is back up: " + cause + " ended",
	}
}

// TickFriendBack is the friend-back part (PartFriendBack). The store brings
// her up in friendSeats, before the parts, and does not fill TickReq.Back, so
// on a tick this plans nothing. A Back that names her yields her note.
func TickFriendBack(s *Snapshot, r TickReq) (Plan, int) {
	if s == nil || len(r.Back) == 0 {
		return Plan{}, 0
	}
	notes := make([]Note, 0, len(r.Back))
	for _, b := range r.Back {
		if b.Name == "" || b.Cause == "" {
			continue
		}
		notes = append(notes, FriendBackNote(b.Name, b.Cause, r.who(), s.Coordinator, s.Now))
	}
	if len(notes) == 0 {
		return Plan{}, 0
	}
	return Plan{Notes: notes}, 0
}
