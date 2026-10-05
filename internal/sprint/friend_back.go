package sprint

import (
	"fmt"
	"regexp"
	"time"
)

// A friend back up by the machine (docs/SPEC-SPRINT.md section 1, "A friend
// back up"; the owner, 2026-10-05 9:35 AM ET: "Just like you notice that
// friends are down, you should notice they are back up automatically"). A
// hold (friend down) or a down observed by the coordinator's daemon (friend
// health) carries its cause and, when known, when it ends: friend down
// --reason --until, a usage limit's reset time. A cause that ends (out of
// credit, a usage limit, deaf, or any hold with --until) is probed by the
// machine on a backoff, and at its known end; when the probe passes she is
// brought up at her row's width (nova-config's), and the coordinator gets one
// note. A hold by hand with no ending cause is never released by the machine:
// it is the coordinator's alone.

// The causes that end, as FriendBackCause names them.
const (
	CauseOutOfCredit = "out of credit"
	CauseUsageLimit  = "usage limit"
	CauseDeaf        = "deaf"
	CauseUntil       = "until"
)

// NFriendBack is the happened note the coordinator gets once per return:
// "<friend> is back up: <cause> ended".
const NFriendBack = "friend back"

// FriendBackFirst is the first wait between probes of a friend whose cause
// has no known end, doubled after each probe that fails up to FriendBackMax.
const (
	FriendBackFirst = time.Minute
	FriendBackMax   = 30 * time.Minute
)

var (
	creditWords = regexp.MustCompile(`(?i)out of (?:ai )?credits?|insufficient [a-z ]{0,20}credits|credits? (?:exhausted|ran out)|no (?:more )?credits?|funds|billing|balance`)
	limitWords  = regexp.MustCompile(`(?i)usage limit|rate limit|limit reached|hit your (?:[a-z-]+ )?limit|quota (?:exceeded|exhausted)|allowance`)
	deafWords   = regexp.MustCompile(`(?i)\bdeaf\b|not answering|unanswered|no pong`)
)

// FriendBackCause is the cause a hold or a down carries that the machine may
// end, from its reason and its until: out of credit, a usage limit and deaf
// by their words, and any other reason with a known end is CauseUntil. ""
// is none: a hold by hand with no ending cause, which only the coordinator
// releases.
func FriendBackCause(reason string, until time.Time) string {
	switch {
	case creditWords.MatchString(reason):
		return CauseOutOfCredit
	case limitWords.MatchString(reason):
		return CauseUsageLimit
	case deafWords.MatchString(reason):
		return CauseDeaf
	case !until.IsZero():
		return CauseUntil
	}
	return ""
}

// FriendBackProbe is what the machine keeps of its probes of one hold: when
// it last probed, and how many probes have failed since the hold began.
type FriendBackProbe struct {
	At     time.Time `json:"at,omitzero"`
	Failed int       `json:"failed,omitempty"`
}

// FriendBackWait is the wait after failed probes: FriendBackFirst doubled for
// each failure past the first, never past FriendBackMax.
func FriendBackWait(failed int) time.Duration {
	d := FriendBackFirst
	for i := 1; i < failed && d < FriendBackMax; i++ {
		d *= 2
	}
	return min(d, FriendBackMax)
}

// FriendBackDue says the machine probes the friend at now: her hold or down
// has a cause that ends, its known end (until) has come, and either she was
// never probed since that end or the backoff after her last failed probe has
// passed. A probe that failed before a known end is not counted against it:
// the end is probed at once.
func FriendBackDue(cause string, until time.Time, p FriendBackProbe, now time.Time) bool {
	if cause == "" {
		return false
	}
	if !until.IsZero() && now.Before(until) {
		return false
	}
	if p.At.IsZero() || (!until.IsZero() && p.At.Before(until)) {
		return true
	}
	return !now.Before(p.At.Add(FriendBackWait(p.Failed)))
}

// FriendProbeResult is one probe of a friend: whether her session answered a
// wake ping (the daemon's own answer does not count), and for an out-of-credit
// cause whether her run refused for funds; Why is what failed, for the log.
type FriendProbeResult struct {
	Answered     bool
	FundsRefused bool
	Why          string
}

// Passes says the probe brings her back for the cause: her session answered
// and, for out of credit, her run did not refuse for funds.
func (r FriendProbeResult) Passes(cause string) bool {
	return r.Answered && !(cause == CauseOutOfCredit && r.FundsRefused)
}

// FriendProbeFn probes one friend for the cause (the binding's: a wake ping
// her session must answer, nova-friend ping --wake and wait-pong).
type FriendProbeFn func(friend, cause string) FriendProbeResult

// FriendBackWhat is the note's words: "<friend> is back up: <cause> ended",
// with the reason the hold or the down gave when it is not the cause's own.
func FriendBackWhat(friend, cause, reason string) string {
	what := cause
	if cause == CauseUntil || what == "" {
		what = "the hold"
		if reason != "" {
			what = fmt.Sprintf("the hold (%s)", reason)
		}
	}
	return fmt.Sprintf("%s is back up: %s ended", friend, what)
}

// FriendBackNote is the one note to the coordinator for a return.
func FriendBackNote(friend, cause, reason, who, to string, now time.Time) Note {
	n := happened(NFriendBack, "", now)
	n.Who, n.To, n.What = who, to, FriendBackWhat(friend, cause, reason)
	return n
}
