package life

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// A friend's beat (nova-tools #4233; Glenn 2026-09-26 8:58 AM ET: friends
// may run on any bench, 9:03 AM: "friends are running themselves"): the
// friend's own harness ticks `nova-sprint friend beat --as friend:<f>` at
// zero tokens, and that one tick is the friend's presence on every table:
//
//	friend:<f>:beat   host, at (ms), load1, ncpu, cpu: the consumer table's
//	                  status (up under a minute old) and load, measured on
//	                  the machine the friend's session runs on, so no
//	                  friend load is read from a hardcoded bench beat
//	friend:<f>        at (RFC 3339), up, host: the row the deal duty reads
//	                  (taskcard.Consumer.BeatKey; live under 90 s)
//
// and the verb then renews the leases of the copies the friend holds
// (taskcard.BeatCopies over Working). No TTL is set and any TTL a `friend
// hello` loop left is removed: keys do not expire, at is reader-judged
// (Glenn 2026-09-23). The session field of the beat is left alone, so a
// hello loop of the same friend keeps beating beside this one.

// FriendBeatRequest is one friend beat.
type FriendBeatRequest struct {
	Friend string
	Host   string
	// Load1, NCPU and CPU are the machine's measurements (Load1Now,
	// runtime.NumCPU, CPUBusyNow); an empty one is not written.
	Load1 string
	NCPU  int
	CPU   string
	// At is the beat's clock; zero means now.
	At time.Time
}

// FriendBeatResult is what one beat wrote and found.
type FriendBeatResult struct {
	Friend string
	AtMS   int64
	// Working is the friend's working copies at the beat, whose leases the
	// verb renews next.
	Working []string
}

// FriendBeatHarness is the harness field a friend beat writes, naming its
// producer beside a hello loop's harness.
const FriendBeatHarness = "friend beat"

// FriendBeat writes the friend's beat and row in one pipeline and reads
// its working copies in the same round trip.
func FriendBeat(ctx context.Context, st *store.Store, req FriendBeatRequest) (FriendBeatResult, error) {
	friend := strings.ToLower(strings.TrimSpace(req.Friend))
	host := strings.TrimSpace(req.Host)
	if st == nil || st.Client() == nil || friend == "" || host == "" || strings.ContainsAny(friend+host, " \t\r\n:") {
		return FriendBeatResult{}, fmt.Errorf("friend beat: store, friend and host are required")
	}
	at := req.At
	if at.IsZero() {
		at = time.Now()
	}
	ms := at.UnixMilli()
	fields := []any{"host", host, "at", strconv.FormatInt(ms, 10), "harness", FriendBeatHarness}
	if v := strings.TrimSpace(req.Load1); v != "" {
		fields = append(fields, "load1", v)
	}
	if req.NCPU > 0 {
		fields = append(fields, "ncpu", strconv.Itoa(req.NCPU))
	}
	if v := strings.TrimSpace(req.CPU); v != "" {
		fields = append(fields, "cpu", v)
	}
	beat := "friend:" + friend + ":beat"
	row := "friend:" + friend
	pipe := st.Client().Pipeline()
	pipe.HSet(ctx, beat, fields...)
	pipe.Persist(ctx, beat)
	pipe.HSet(ctx, row, "at", RowStamp(at), "up", "1", "host", host)
	working := pipe.ZRange(ctx, row+":cards:working", 0, -1)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return FriendBeatResult{}, fmt.Errorf("friend beat %s: %w", friend, err)
	}
	if err := working.Err(); err != nil && !errors.Is(err, redis.Nil) {
		return FriendBeatResult{}, fmt.Errorf("friend beat %s: working copies: %w", friend, err)
	}
	return FriendBeatResult{Friend: friend, AtMS: ms, Working: working.Val()}, nil
}
