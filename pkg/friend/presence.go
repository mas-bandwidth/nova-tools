package friend

import (
	"context"
	"fmt"
	"time"
)

// Presence is whether the friend's SESSION is alive, never its daemon
// (docs/SPEC-FRIEND.md, presence). The finding of 2026-10-04: three friends
// read up with eight cards each while their harness apps were not running,
// because the daemon answered every ping itself. The proof is the session's:
// after SessionQuiet with no bus message from the session, the daemon
// delivers a session check carrying a fresh nonce through the adapter, the
// way a card goes in; only the session's own reply carrying that nonce
// counts; SessionBound with none and the friend is down, NoSessionAnswer; the
// next answer, or any bus message the session writes, brings it back up. The
// daemon's pong stays the daemon's and never counts.
const (
	SessionQuiet = 10 * time.Minute
	SessionBound = 5 * time.Minute
	// ProveEvery is how long after the last check went in the next goes in while the
	// session is up, whatever else it says on the bus, timed from the ask: the sprint
	// server counts only an answered check, for sprint.FriendProofLive (fifteen
	// minutes), so a session that answers within SessionBound is proved again before
	// the last proof lapses (ProveEvery + SessionBound < FriendProofLive).
	ProveEvery = 8 * time.Minute
	// ReaskAfter is how long an unanswered check waits before it is asked again when the
	// session has not read it: a queueing harness keeps every copy, so a check is asked
	// again early only once the session has read the last (Presence.Read).
	ReaskAfter = time.Hour

	NoSessionAnswer = "no session answer"
	NotYetAnswered  = "no session answer yet" // the daemon started; nothing has answered

	SessionCheckPrefix = "SESSION CHECK "

	LogBatch = 1000 // log entries read per step; the rest the next
)

// The presence, as status and check report it.
const (
	PresenceUp   = "up"
	PresenceDown = "down"
)

// SessionCheckText is the check as the session reads it: the nonce, and the
// one line to run with nothing to fill in, sent to to (the seat, else the
// coordinator; the pong verb's own default when empty).
func SessionCheckText(nonce, pong, to string) string {
	if to != "" {
		pong += " --to " + to
	}
	return fmt.Sprintf("%s%s\nThe daemon has had no bus message from this session for a while and asks whether the session is alive; only an answer from inside this session counts, and none within %s puts you down. Answer now, before anything else, with one command, then end this turn: %s\n", SessionCheckPrefix, nonce, SessionBound, pong)
}

// PresenceStatus is the session's presence status: what check reads from the hook record.
type PresenceStatus struct {
	Friend    string    `json:"friend"`
	At        time.Time `json:"at"`
	Presence  string    `json:"presence"` // PresenceUp or PresenceDown
	Reason    string    `json:"reason,omitempty"`
	LastHeard time.Time `json:"last_heard"`
	Nonce     string    `json:"nonce,omitempty"`
	Asked     time.Time `json:"asked"`
	Checks    int       `json:"checks"`
	Answers   int       `json:"answers"`
	Answered  string    `json:"answered,omitempty"` // the nonce the session last answered
}

// StaleTurnLock is how long the turn lock may be held while a headless
// adapter's own turn record says no turn runs before the session check goes in
// by the record (docs/SPEC-FRIEND.md, presence: the headless case): one step's
// race with a turn on its way into the adapter is never read as a stale lock.
const StaleTurnLock = time.Minute

// TurnRecord is a headless adapter's own record of its turns: one that delivers
// by running a one-shot program into the session (dsh headless, gemini
// --resume), each turn a process that starts and ends. A turn runs while one has
// begun and not ended, since the latest's start.
type TurnRecord interface {
	TurnUnderWay() (running bool, since time.Time)
}

// ReadOnReturn is an adapter whose delivery returns exit 0 only once the session
// has taken the text: a headless turn that ran (dsh, gemini, opencode run), or a
// message the session marked read (antigravity's read.json). A check it delivered
// is read, and may be asked again on the check cadence.
type ReadOnReturn interface{ ReadOnReturn() }

func (*DSH) ReadOnReturn()         {}
func (*Gemini) ReadOnReturn()      {}
func (*OpenCode) ReadOnReturn()    {}
func (*Antigravity) ReadOnReturn() {}

// FreshTurn is a deliverer whose check goes in as a fresh turn of its own, a
// session of its own that no listing is read for and none is grown: the
// presence of a one-shot friend (docs/SPEC-FRIEND.md, presence: a tiny fresh
// turn per check). A one-shot friend has no long-lived session by design, so
// a session that is full or missing is never the reason she is down.
type FreshTurn interface {
	DeliverFresh(ctx context.Context, text string) (int, error)
}

// BeatWords are a beat's proof words (nova-sprint friend beat --run, --check,
// --pong): the daemon's run, the check it put into the session, and the check
// its session answered, each "" when there is none to say. The sprint server
// counts an answer only when it names a check this run asked (sprint.ProveBeat).
type BeatWords struct {
	Run, Check, Pong string
	// StopReturns is how many stop-returns the lanes owe (stop.go, OwedStopReturns): the
	// beat carries it (friend beat --stop-returns) while it is above zero.
	StopReturns int
}
