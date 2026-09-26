package life

import (
	"context"
	"errors"
	"fmt"
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
// pull, card work --as friend:<f>), and it stops when the friend holds none.
//
//	friend:<f>:beatloop   the loop's lease: SET NX PX by the verb that starts
//	                      the loop (the loop adopts that token), renewed by
//	                      the loop every tick, deleted by the loop when it
//	                      exits; a loop that dies lets it lapse in
//	                      BeatLoopLease, and the next verb starts another.
//
// The lease is a lock, the one key here with a PX: it says a loop is alive,
// so a record never claims one that is not.

// BeatLoopLease is how long a friend beat loop's lease outlives its last
// renewal.
const BeatLoopLease = 10 * time.Second

// BeatLoopIdleTicks is how many ticks in a row with no working copy end the
// loop.
const BeatLoopIdleTicks = 2

// BeatLoopKey is friend f's beat loop lease.
func BeatLoopKey(friend string) string { return "friend:" + strings.ToLower(friend) + ":beatloop" }

// renewScript extends the lease while it holds this loop's token, takes it
// when it lapsed, and returns 0 when another loop holds it.
const renewScript = `local v = redis.call('GET', KEYS[1])
if v == ARGV[1] then return redis.call('PEXPIRE', KEYS[1], ARGV[2]) end
if not v then redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2]); return 2 end
return 0`

// releaseScript deletes the lease only while it holds this loop's token.
const releaseScript = `if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0`

// ClaimBeatLoop takes friend's beat loop lease for token (SET NX PX): true
// when this call took it, false when a loop already holds it.
func ClaimBeatLoop(ctx context.Context, c redis.Cmdable, friend, token string) (bool, error) {
	ok, err := c.SetNX(ctx, BeatLoopKey(friend), token, BeatLoopLease).Result()
	if err != nil {
		return false, fmt.Errorf("claim %s: %w", BeatLoopKey(friend), err)
	}
	return ok, nil
}

// RenewBeatLoop extends friend's lease for token (taking it when it
// lapsed): false when another loop holds it.
func RenewBeatLoop(ctx context.Context, c redis.Cmdable, friend, token string) (bool, error) {
	n, err := c.Eval(ctx, renewScript, []string{BeatLoopKey(friend)}, token, BeatLoopLease.Milliseconds()).Int64()
	if err != nil {
		return false, fmt.Errorf("renew %s: %w", BeatLoopKey(friend), err)
	}
	return n != 0, nil
}

// ReleaseBeatLoop deletes friend's lease while token holds it.
func ReleaseBeatLoop(ctx context.Context, c redis.Cmdable, friend, token string) error {
	if err := c.Eval(ctx, releaseScript, []string{BeatLoopKey(friend)}, token).Err(); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("release %s: %w", BeatLoopKey(friend), err)
	}
	return nil
}

// BeatLoop is one friend's beat loop: each Step renews the lease, then
// ticks (FriendBeat: the beat and every held copy's lease) and counts the
// ticks in a row that found no working copy.
type BeatLoop struct {
	Client redis.Cmdable
	Friend string
	Token  string
	// Tick is one beat at now, returning how many copies the friend holds
	// in working (FriendBeatResult.Working).
	Tick func(ctx context.Context, now time.Time) (working int, err error)
	idle int
}

// Step is one tick at now. done says the loop exits, why names it: LOST
// (another loop holds the lease), IDLE (no working copy for
// BeatLoopIdleTicks ticks; the lease is released) or HANDED (a copy came in
// as it released and another loop took the lease). An error is the tick's;
// the loop backs off and steps again.
//
// The idle exit releases the lease and THEN ticks once more: a verb works
// its copies before it looks for the lease, so a copy worked before that
// last tick is seen here (the loop takes the lease back and stays), and one
// worked after it finds no lease and starts a loop of its own.
func (l *BeatLoop) Step(ctx context.Context, now time.Time) (done bool, why string, err error) {
	held, err := RenewBeatLoop(ctx, l.Client, l.Friend, l.Token)
	if err != nil {
		return false, "", err
	}
	if !held {
		return true, "LOST " + BeatLoopKey(l.Friend) + " is another loop's", nil
	}
	n, err := l.Tick(ctx, now)
	if err != nil {
		return false, "", err
	}
	if n > 0 {
		l.idle = 0
		return false, "", nil
	}
	if l.idle++; l.idle < BeatLoopIdleTicks {
		return false, "", nil
	}
	if err := ReleaseBeatLoop(ctx, l.Client, l.Friend, l.Token); err != nil {
		return false, "", err
	}
	if n, err := l.Tick(ctx, now); err == nil && n == 0 {
		return true, fmt.Sprintf("IDLE no working copy for %d ticks", l.idle), nil
	}
	took, err := ClaimBeatLoop(ctx, l.Client, l.Friend, l.Token)
	if err != nil {
		return false, "", err
	}
	if !took {
		return true, "HANDED " + BeatLoopKey(l.Friend) + " was taken by another loop", nil
	}
	l.idle = 0
	return false, "", nil
}
