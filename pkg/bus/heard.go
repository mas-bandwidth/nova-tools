package bus

import (
	"context"
	"fmt"
	"slices"
	"time"
)

// The two hashes of what the bus has seen each name do (SPEC-BUS.md,
// bus-heard-activity; tla/BusHeard.tla): one field per name, its value the
// store's clock at the name's last send (HeardSendKey) or last recv or ack
// (HeardRecvKey), RFC 3339 in UTC to the millisecond. A mark is overwritten,
// never added to, so each hash holds one field per name for ever. They are
// written by the verbs themselves, in the send's own transaction or one HSET
// beside a recv or an ack, and read raw by the sprint's presence rule.
const (
	HeardSendKey = "bus2:heard:send"
	HeardRecvKey = "bus2:heard:recv"
)

// heardLayout is a mark's value: RFC 3339, UTC, to the millisecond.
const heardLayout = "2006-01-02T15:04:05.000Z"

// HeardStamp is a mark's value for the instant at.
func HeardStamp(at time.Time) string { return at.UTC().Format(heardLayout) }

// parseHeard is the instant a mark's value holds, the zero time when it holds
// none (a value that is no instant says nothing was heard, never a refusal).
func parseHeard(v string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{} // ignored: a value that is no instant is no activity
	}
	return t.UTC()
}

// heardMark is the mark of name at the instant at, on the hash key.
func heardMark(key, name string, at time.Time) Mark {
	return Mark{Key: key, Field: name, Value: HeardStamp(at)}
}

// markReceived records that as is reading at the store's time at: one HSET
// (HeardRecvKey, overwritten). It is advice for a sender and the sprint, never
// part of the delivery: a mark the store did not take goes to OnStampError and
// the verb goes on.
func (b *Bus) markReceived(ctx context.Context, as string, at time.Time) {
	if err := b.Store.AddAll(ctx, nil, nil, heardMark(HeardRecvKey, as, at)); err != nil && b.OnStampError != nil {
		b.OnStampError(fmt.Errorf("heard mark of %s not written: %w", as, err))
	}
}

// Standing is what the bus knows of one name's ears: its friend daemon's
// proof of the inbox push, and the last time the name itself sent and read.
// A zero Sent or Received is never.
type Standing struct {
	Proof          PushProof
	Sent, Received time.Time
}

// Name is the name the standing is of.
func (s Standing) Name() string { return s.Proof.Name }

// fresh is whether the activity at t is younger than PushFresh at now; a store
// clock that stepped back (t after now) is fresh too, never negative.
func fresh(t, now time.Time) bool { return !t.IsZero() && now.Sub(t) < PushFresh }

// ageWord is how long ago t was at now, "never" when there is no t.
func ageWord(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return max(now.Sub(t), 0).Truncate(time.Second).String()
}

// SentWord and RecvWord are the age of the last send and the last recv or ack
// at now, as names prints them: "never" when there is none.
func (s Standing) SentWord(now time.Time) string { return ageWord(s.Sent, now) }
func (s Standing) RecvWord(now time.Time) string { return ageWord(s.Received, now) }

// daemonSaysDown is whether the friend daemon wrote, under PushFresh ago, that
// the session did not answer its SESSION CHECK: the one thing that outweighs a
// recent recv, because the daemon's own recv loop reads for a session that is
// not there (tla/BusHeard.tla: DaemonDownOutweighsRecv).
func (s Standing) daemonSaysDown(now time.Time) bool {
	return s.Proof.State(now) == PushDown && fresh(s.Proof.At, now)
}

// Heard is whether the name is heard at the store's now: its proof is proven
// (PushProven), or it received (recv or ack) under PushFresh ago and its daemon
// does not say the session is down. A name nothing proves and nothing has
// received lately is not heard; a message to it still lands and waits.
func (s Standing) Heard(now time.Time) bool {
	if s.Proof.State(now) == PushProven {
		return true
	}
	return fresh(s.Received, now) && !s.daemonSaysDown(now)
}

// Deaf is whether the name can send but not receive at now: it sent under
// PushFresh ago and is not heard. names shows it; it is the sign of a receiver
// that is not running or not waking its session.
func (s Standing) Deaf(now time.Time) bool { return fresh(s.Sent, now) && !s.Heard(now) }

// rearm is what a deaf session does, as commands that exist (docs/SPEC-HOOK.md):
// look at the hook, hook in under the session's Monitor, and run the receiver
// under it.
func rearm(name string) string {
	return fmt.Sprintf("nova-hook status --as %s shows the hook; to re-arm the session: nova-hook hook --as %s --role <comms|friend> under the session's Monitor, then Monitor nova-bus recv --as %s --forever --exec '<deliver-into-session>'", name, name, name)
}

// what is the standing in words: when the name last received and sent, and
// what its daemon says when it says the session is down.
func (s Standing) what(now time.Time) string {
	n := s.Name()
	var w string
	if s.Received.IsZero() {
		w = n + " has never received on the bus"
	} else {
		w = fmt.Sprintf("%s last received %s ago", n, s.RecvWord(now))
	}
	if s.Sent.IsZero() {
		w += " (never sent)"
	} else {
		w += fmt.Sprintf(" (last sent %s ago)", s.SentWord(now))
	}
	if s.daemonSaysDown(now) {
		w += fmt.Sprintf(", and its friend daemon says the session did not answer its SESSION CHECK (%s)", s.Proof.Reason)
	}
	return w
}

// Unheard is the advisory line for the name at now, "" when it is heard: the
// NOTE send prints beside a message that landed. It never refuses: the message
// is queued on the name's stream and arrives when its receiver runs; the line
// says when the name last received and sent and how to re-arm a receiver that
// is not running.
func (s Standing) Unheard(now time.Time) string {
	if s.Heard(now) {
		return ""
	}
	n := s.Name()
	return fmt.Sprintf("%s: the message is queued on %s's stream and arrives when its receiver runs; if %s is deaf, its receiver is not running: %s; nova-bus names shows who is heard", s.what(now), n, n, rearm(n))
}

// DeafLine is why the name is not heard at now, "" when it is: the refusal of
// nova-bus's --require-push, with the same remedy.
func (s Standing) DeafLine(now time.Time) string {
	if s.Heard(now) {
		return ""
	}
	return fmt.Sprintf("deaf: %s: nothing was written; the remedy: %s; nova-bus names shows who is heard", s.what(now), rearm(s.Name()))
}

// Silent is the line a receiver is told at the start of its own recv when its
// receiver was not running: it last received more than PushFresh ago. "" when
// it received lately, or never (a first recv has nothing to say).
func (s Standing) Silent(now time.Time) string {
	if s.Received.IsZero() || fresh(s.Received, now) {
		return ""
	}
	n := s.Name()
	return fmt.Sprintf("%s's receiver was not running: it last received %s ago, past %s, so a sender was told %s is queued and not heard; messages sent meanwhile are delivered now; keep a receiver running: %s", n, s.RecvWord(now), PushFresh, n, rearm(n))
}

// Standings is each name's Standing, in the order asked, and the store's now
// they are read at (a roster trip, then one pipeline of three HGETALL).
func (b *Bus) Standings(ctx context.Context, names ...string) ([]Standing, time.Time, error) {
	_, now, err := b.Store.Roster(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	out, err := b.standings(ctx, names)
	return out, now, err
}

// standings is each name's Standing, in the order asked, in one trip.
func (b *Bus) standings(ctx context.Context, names []string) ([]Standing, error) {
	hashes, err := b.Store.Marks(ctx, PushKey, HeardSendKey, HeardRecvKey)
	if err != nil {
		return nil, err
	}
	proofs := proofsFrom(hashes[0], names)
	out := make([]Standing, len(names))
	for i, n := range names {
		out[i] = Standing{Proof: proofs[i], Sent: parseHeard(hashes[1][n]), Received: parseHeard(hashes[2][n])}
	}
	return out, nil
}

// Unheard is one advisory line for every name of names (deduplicated, in
// order) that is not heard at the store's now, in one trip after the roster's:
// what send prints as SEND NOTE after its message landed. It is advice, never
// a gate: a message is never refused on it (the finding of 2026-10-08, issue
// #5450: a claude friend and a machine with no daemon could never be written
// to or read as).
func (b *Bus) Unheard(ctx context.Context, names ...string) ([]string, error) {
	_, now, err := b.Store.Roster(ctx)
	if err != nil {
		return nil, err
	}
	return b.UnheardAt(ctx, now, names...)
}

// UnheardAt is Unheard judged at at, with no roster trip: send's, judged at
// the store's time its message was stamped with (one pipeline after the write).
func (b *Bus) UnheardAt(ctx context.Context, at time.Time, names ...string) ([]string, error) {
	return b.judge(ctx, at, Standing.Unheard, names...)
}

// Heard refuses every name of names (deduplicated, in order) that is not
// heard at the store's now, one deaf line each: the gate nova-bus's
// --require-push puts in front of a send, before anything is written. It is
// the exception, asked for by flag; the bus itself never refuses on it.
func (b *Bus) Heard(ctx context.Context, names ...string) error {
	_, now, err := b.Store.Roster(ctx)
	if err != nil {
		return err
	}
	problems, err := b.judge(ctx, now, Standing.DeafLine, names...)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return &Refusal{problems}
	}
	return nil
}

// HeardReading is Heard for the name that is reading: a recv is itself a
// receive, so the name counts as received at now, and only its daemon's fresh
// word that the session is down can refuse it. (A name that has never received
// could otherwise never pass the gate that its first recv is for.)
func (b *Bus) HeardReading(ctx context.Context, as string) error {
	ss, now, err := b.Standings(ctx, as)
	if err != nil {
		return err
	}
	ss[0].Received = now
	if l := ss[0].DeafLine(now); l != "" {
		return &Refusal{[]string{l}}
	}
	return nil
}

// Silent is the line Standing.Silent says for as at the store's now, "" when
// there is none: read before the recv that follows marks the name heard.
func (b *Bus) Silent(ctx context.Context, as string) (string, error) {
	ss, now, err := b.Standings(ctx, as)
	if err != nil {
		return "", err
	}
	return ss[0].Silent(now), nil
}

// judge is line(standing, at) for every name of names (deduplicated, in
// order) whose line is not empty, in one trip.
func (b *Bus) judge(ctx context.Context, at time.Time, line func(Standing, time.Time) string, names ...string) ([]string, error) {
	var uniq []string
	for _, n := range names {
		if !slices.Contains(uniq, n) {
			uniq = append(uniq, n)
		}
	}
	ss, err := b.standings(ctx, uniq)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, s := range ss {
		if l := line(s, at); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}
