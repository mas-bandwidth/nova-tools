package friend

import (
	"fmt"
	"time"
)

// Whether a friend is there is the nova-hook record's alone: nova-hook serve
// challenges every hooked name once a minute, only the model answers with
// nova-hook prove, and two misses unhook it (docs/SPEC-HOOK.md). What is left
// here is the delivery check's (nova-friend check --harness, Conformance): a
// SESSION CHECK carrying a nonce goes in through the harness, and the session's
// pong with that nonce must reach the bus within SessionBound.
const (
	SessionBound       = 5 * time.Minute
	SessionCheckPrefix = "SESSION CHECK "
	LogBatch           = 1000 // log entries read per step; the rest the next
)

// The presence, as check reports it from the nova-hook record (HookRecord.Up).
const (
	PresenceUp   = "up"
	PresenceDown = "down"
)

// SessionCheckText is the delivery check as the session reads it: the nonce,
// and the one line to run with nothing to fill in, sent to to (the seat, else
// the coordinator; the pong verb's own default when empty).
func SessionCheckText(nonce, pong, to string) string {
	if to != "" {
		pong += " --to " + to
	}
	return fmt.Sprintf("%s%s\nThis is nova-friend's delivery check: it proves a message reaches this session. Answer now, before anything else, with one command, then end this turn: %s\n", SessionCheckPrefix, nonce, pong)
}

// BeatWords are what a beat carries beside the friend's name: how many
// stop-returns the lanes owe (stop.go, OwedStopReturns), said while it is above
// zero (friend beat --stop-returns).
type BeatWords struct {
	StopReturns int
}
