package life

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The friend beat loop (nova-tools seat-keeps-beat, 2026-09-26 13:32 EDT: a
// friend held two working copies while the only thing beating for it was a
// shell loop in a session scratchpad that died with the session, so the
// table called the one worker down): while a friend holds working copies,
// ONE `nova-sprint friend beat --as friend:<f> --loop` runs for it, started
// in its own session by the verb that gave the friend the copy (friend
// pull, card work --as friend:<f>, task take), and it stops when the friend
// holds none.
//
//	friend:<f>:beatloop   the loop's lease, a hash (token, host, pid) with a
//	                      PEXPIRE: claimed by the verb that starts the loop
//	                      (the loop adopts that token), renewed by the loop
//	                      every tick, deleted by the loop when it exits; a
//	                      loop that dies lets it lapse in BeatLoopLease, or a
//	                      verb on its host finds its pid gone and takes it.
//
// The lease is a lock, the one key here with a PX: it says a loop is alive,
// so a record never claims one that is not. Every move of it is a function
// of the library (fn/lua/friend_beatloop.lua), which the friend seat's ACL
// row grants (store/acl.go): the seat has no SET and no EVAL.

// BeatLoopLease is how long a friend beat loop's lease outlives its last
// renewal.
const BeatLoopLease = 10 * time.Second

// BeatLoopIdleTicks is how many ticks in a row with no working copy end the
// loop.
const BeatLoopIdleTicks = 2

// BeatLoopMaxBackoff caps how long a loop in errors waits before it steps
// again: under BeatLoopLease, so a loop whose renewals fail and then come
// back renews before its lease lapses. A tick's error never delays the
// renewal at all (BeatLoop.Step).
const BeatLoopMaxBackoff = 4 * time.Second

// The lease's functions (fn/lua/friend_beatloop.lua).
const (
	FnLoopClaim   = "ns_friend_loop_claim"
	FnLoopRenew   = "ns_friend_loop_renew"
	FnLoopRelease = "ns_friend_loop_release"
)

// BeatLoopKey is friend f's beat loop lease.
func BeatLoopKey(friend string) string { return "friend:" + strings.ToLower(friend) + ":beatloop" }

// LoopHolder is who holds a beat loop lease: its token, and the host and
// pid of the loop (pid 0 until the loop, or the verb that started it,
// renews it).
type LoopHolder struct {
	Token, Host string
	PID         int
}

// ClaimBeatLoop takes friend's beat loop lease for me: took is true when
// this call took it; else held is the holder. dead is the token of a holder
// the caller found dead (its pid gone on the caller's host): a lease still
// holding it is taken over. "" takes only a free lease.
func ClaimBeatLoop(ctx context.Context, c redis.Cmdable, friend string, me LoopHolder, dead string) (took bool, held LoopHolder, err error) {
	out, err := c.FCall(ctx, FnLoopClaim, []string{BeatLoopKey(friend)}, me.Token, BeatLoopLease.Milliseconds(),
		me.Host, me.PID, dead).StringSlice()
	if err != nil {
		return false, LoopHolder{}, fmt.Errorf("claim %s: %w", BeatLoopKey(friend), err)
	}
	switch {
	case len(out) == 1 && out[0] == "TOOK":
		return true, LoopHolder{}, nil
	case len(out) == 4 && out[0] == "HELD":
		pid, _ := strconv.Atoi(out[3])
		return false, LoopHolder{Token: out[1], Host: out[2], PID: pid}, nil
	}
	return false, LoopHolder{}, fmt.Errorf("claim %s: unexpected reply %v", BeatLoopKey(friend), out)
}

// RenewBeatLoop extends friend's lease for me and writes its host and pid
// (taking it when it lapsed): false when another loop holds it.
func RenewBeatLoop(ctx context.Context, c redis.Cmdable, friend string, me LoopHolder) (bool, error) {
	n, err := c.FCall(ctx, FnLoopRenew, []string{BeatLoopKey(friend)}, me.Token, BeatLoopLease.Milliseconds(),
		me.Host, me.PID).Int64()
	if err != nil {
		return false, fmt.Errorf("renew %s: %w", BeatLoopKey(friend), err)
	}
	return n != 0, nil
}

// ReleaseBeatLoop deletes friend's lease while token holds it.
func ReleaseBeatLoop(ctx context.Context, c redis.Cmdable, friend, token string) error {
	if err := c.FCall(ctx, FnLoopRelease, []string{BeatLoopKey(friend)}, token).Err(); err != nil {
		return fmt.Errorf("release %s: %w", BeatLoopKey(friend), err)
	}
	return nil
}

// LoopLease is the lease one loop holds: the moves Step makes on it.
type LoopLease interface {
	// Renew extends it: false when another loop holds it.
	Renew(ctx context.Context) (bool, error)
	// Release lets go of it while this loop holds it.
	Release(ctx context.Context) error
	// Claim takes it when it is free: false when another loop holds it.
	Claim(ctx context.Context) (bool, error)
}

// StoreLease is a LoopLease on the store: friend's lease held by Me.
type StoreLease struct {
	Client redis.Cmdable
	Friend string
	Me     LoopHolder
}

// Renew is RenewBeatLoop.
func (s StoreLease) Renew(ctx context.Context) (bool, error) {
	return RenewBeatLoop(ctx, s.Client, s.Friend, s.Me)
}

// Release is ReleaseBeatLoop.
func (s StoreLease) Release(ctx context.Context) error {
	return ReleaseBeatLoop(ctx, s.Client, s.Friend, s.Me.Token)
}

// Claim is ClaimBeatLoop of a free lease.
func (s StoreLease) Claim(ctx context.Context) (bool, error) {
	took, _, err := ClaimBeatLoop(ctx, s.Client, s.Friend, s.Me, "")
	return took, err
}

// BeatLoop is one friend's beat loop: each Step renews the lease, then
// ticks (FriendBeat: the beat and every held copy's lease) and counts the
// ticks in a row that found no working copy.
type BeatLoop struct {
	Lease  LoopLease
	Friend string
	// Tick is one beat at now, returning how many copies the friend holds
	// in working (FriendBeatResult.Working).
	Tick func(ctx context.Context, now time.Time) (working int, err error)
	idle int
	// backoff is the wait after the last error, next when it ends, and
	// renewing says the error was the lease's (the renewal waits too),
	// not the tick's (the renewal goes on each step).
	backoff  time.Duration
	next     time.Time
	renewing bool
}

// RetryIn is how long the loop waits after its last error before it tries
// again (0 after a step with none).
func (l *BeatLoop) RetryIn() time.Duration { return l.backoff }

// fail backs the loop off after err: from BeatInterval, doubling, capped at
// BeatLoopMaxBackoff.
func (l *BeatLoop) fail(now time.Time, err error, renewing bool) error {
	l.backoff = min(max(2*l.backoff, BeatInterval), BeatLoopMaxBackoff)
	l.next, l.renewing = now.Add(l.backoff), renewing
	return err
}

// Step is one tick at now. done says the loop exits, why names it: LOST
// (another loop holds the lease), IDLE (no working copy for
// BeatLoopIdleTicks ticks; the lease is released) or HANDED (a copy came in
// as it released and another loop took the lease). After an error the loop
// backs off (RetryIn, capped at BeatLoopMaxBackoff) and a step before then
// does nothing, except that a tick's error leaves the lease renewed every
// step: a loop alive in errors keeps its lease, so no verb starts a second
// one beside it.
//
// The idle exit releases the lease and THEN ticks once more: a verb works
// its copies before it looks for the lease, so a copy worked before that
// last tick is seen here (the loop takes the lease back and stays), and one
// worked after it finds no lease and starts a loop of its own.
func (l *BeatLoop) Step(ctx context.Context, now time.Time) (done bool, why string, err error) {
	waiting := now.Before(l.next)
	if waiting && l.renewing {
		return false, "", nil
	}
	held, err := l.Lease.Renew(ctx)
	if err != nil {
		return false, "", l.fail(now, err, true)
	}
	if !held {
		return true, "LOST " + BeatLoopKey(l.Friend) + " is another loop's", nil
	}
	if waiting {
		return false, "", nil
	}
	n, err := l.Tick(ctx, now)
	if err != nil {
		return false, "", l.fail(now, err, false)
	}
	l.backoff, l.next = 0, time.Time{}
	if n > 0 {
		l.idle = 0
		return false, "", nil
	}
	if l.idle++; l.idle < BeatLoopIdleTicks {
		return false, "", nil
	}
	if err := l.Lease.Release(ctx); err != nil {
		return false, "", l.fail(now, err, true)
	}
	if n, err := l.Tick(ctx, now); err == nil && n == 0 {
		return true, fmt.Sprintf("IDLE no working copy for %d ticks", l.idle), nil
	}
	took, err := l.Lease.Claim(ctx)
	if err != nil {
		return false, "", l.fail(now, err, true)
	}
	if !took {
		return true, "HANDED " + BeatLoopKey(l.Friend) + " was taken by another loop", nil
	}
	l.idle = 0
	return false, "", nil
}
