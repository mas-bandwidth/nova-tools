// sprint.go: the whole sprint table (#3530, part of #3662), the one screen
// Glenn watches, read from Redis once a second. It replaces the bash
// renderers of 2026-09-24 (a streams renderer that asked GitHub, an ETA
// script, sprint-table-redis for the host and friend blocks, and a shell loop
// that stitched their files together). Layout, one blank line between blocks:
//
//	SPRINT TABLE [*** PIT STOP ***]
//
//	<left>/<y> left, <z>% done -> ~<eta>m
//
//	stream | waiting | working | merging | landed   (rows in ws:order, all-zero rows hidden, total)
//
//	friend | ready | working | done | status        (status up|down)
//
//	host | ready | working | done | ok | fail | ok% | load
//
// Keys, every one read in ONE pipelined round trip per tick (a second round
// trip only on the tick a membership set changed; never KEYS, never SCAN):
//
//	ws:order                ZRANGE, the streams in rank order (the ws index, #3662)
//	ws:<s>:<state>          ZCARD for waiting, ready, working, merging, landed
//	ws:log                  XRANGE over the last hour: the landed rate for the ETA
//	ws:done0                HGETALL: each friend's done count at the last `table clear`
//	benches                 SMEMBERS, the bench list; bench:<b> HGETALL per member
//	friends                 SMEMBERS when no roster is given; friend:<f> HMGET
//	friend:<f>:down         EXISTS (a string or a hash; either means down)
//	s:<S>:pitstop           EXISTS, with the legacy sprint:<S>:pitstop
//
// The membership of ws:order, benches and friends is kept from the previous
// tick, so the tick's one pipeline reads the sets and every member's values
// together; a tick that finds a set changed reads again at once with the new
// members, so a row is never rendered from last second's membership.
package table

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// WSStates are the five per-stream sets the table counts, in reply order.
var WSStates = []string{"waiting", "ready", "working", "merging", "landed"}

// DoneBaseKey holds each friend's done count at the last `table clear`
// (#3637): the friend block shows done minus this, so a clear zeroes the
// column without touching the index sets friend-row counts.
const DoneBaseKey = "ws:done0"

// SprintConfig is what the whole table is told; everything else is read.
type SprintConfig struct {
	// Sprint names the pit stop keys s:<Sprint>:pitstop and
	// sprint:<Sprint>:pitstop; empty reads no pit stop.
	Sprint string
	// Friends is the roster in display order; empty reads the friends SET,
	// sorted by name.
	Friends []string
	// RowStale: a friend row whose at is older than this is down.
	RowStale time.Duration
	// LockKey/LockToken, when set, refresh the writer's lock in the tick's
	// own pipeline (see AcquireLock); the read reports LockLost when it is gone.
	LockKey, LockToken string
	LockTTL            time.Duration
}

// StreamRow is one stream's five counts.
type StreamRow struct {
	Name                                     string
	Waiting, Ready, Working, Merging, Landed int64
}

// Total is every task in the stream's five sets.
func (r StreamRow) Total() int64 { return r.Waiting + r.Ready + r.Working + r.Merging + r.Landed }

// SprintSnapshot is one tick's read.
type SprintSnapshot struct {
	Config     SprintConfig
	Pitstop    bool
	Streams    []StreamRow
	LandedHour int64 // ws:log moves to landed in the hour before the read
	Benches    []BenchRow
	Friends    []FriendRow
	DoneBase   map[string]string
	// RoundTrips is how many pipelines the read took: 1 in steady state.
	RoundTrips int
	// LockLost says the tick found the writer's lock held by someone else.
	LockLost bool
	// Stale is set when this tick's read failed; the rows are LastGood's.
	Stale    bool
	LastGood time.Time
}

// SprintReader reads the whole table, keeping set membership across ticks.
type SprintReader struct {
	Client  redis.UniversalClient
	Config  SprintConfig
	streams []string
	benches []string
	friends []string
	primed  bool
}

// NewSprintReader is a reader with no membership yet: its first Read takes
// two round trips (the sets, then the values).
func NewSprintReader(client redis.UniversalClient, cfg SprintConfig) *SprintReader {
	return &SprintReader{Client: client, Config: cfg}
}

// Read is one tick at now.
func (r *SprintReader) Read(ctx context.Context, now time.Time) (*SprintSnapshot, error) {
	for trips := 1; trips <= 3; trips++ {
		snap, changed, err := r.readOnce(ctx, now)
		if err != nil {
			return nil, err
		}
		if !changed {
			snap.RoundTrips = trips
			return snap, nil
		}
	}
	return nil, errors.New("membership changed on three reads in a row")
}

func (r *SprintReader) readOnce(ctx context.Context, now time.Time) (*SprintSnapshot, bool, error) {
	cfg := r.Config
	pipe := r.Client.Pipeline()
	order := pipe.ZRange(ctx, "ws:order", 0, -1)
	benchSet := pipe.SMembers(ctx, "benches")
	var friendSet *redis.StringSliceCmd
	if len(cfg.Friends) == 0 {
		friendSet = pipe.SMembers(ctx, "friends")
	}
	var pit *redis.IntCmd
	if cfg.Sprint != "" {
		pit = pipe.Exists(ctx, "s:"+cfg.Sprint+":pitstop", "sprint:"+cfg.Sprint+":pitstop")
	}
	since := now.Add(-time.Hour).UnixMilli()
	log := pipe.XRangeN(ctx, "ws:log", strconv.FormatInt(since, 10), "+", logWindowMax)
	base := pipe.HGetAll(ctx, DoneBaseKey)
	var lock *redis.Cmd
	if cfg.LockKey != "" {
		lock = pipe.Eval(ctx, lockRefreshScript, []string{cfg.LockKey}, cfg.LockToken, lockTTL(cfg).Milliseconds())
	}
	counts := make([][]*redis.IntCmd, len(r.streams))
	for i, s := range r.streams {
		for _, state := range WSStates {
			counts[i] = append(counts[i], pipe.ZCard(ctx, "ws:"+s+":"+state))
		}
	}
	hashes := make([]*redis.MapStringStringCmd, len(r.benches))
	for i, b := range r.benches {
		hashes[i] = pipe.HGetAll(ctx, "bench:"+b)
	}
	roster := cfg.Friends
	if len(roster) == 0 {
		roster = r.friends
	}
	rows := make([]*redis.SliceCmd, len(roster))
	downs := make([]*redis.IntCmd, len(roster))
	for i, f := range roster {
		rows[i] = pipe.HMGet(ctx, "friend:"+f, "at", "up", "ready", "working", "done")
		downs[i] = pipe.Exists(ctx, "friend:"+f+":down")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !isReplyError(err) {
		return nil, false, fmt.Errorf("pipeline: %w", err)
	}
	// A dead connection fails every command; the order read standing is the
	// tick's proof that Redis answered.
	gotOrder, err := order.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, false, fmt.Errorf("zrange ws:order: %w", err)
	}
	gotBenches, _ := benchSet.Result()
	sort.Strings(gotBenches)
	var gotFriends []string
	if friendSet != nil {
		gotFriends, _ = friendSet.Result()
		sort.Strings(gotFriends)
	}
	changed := !r.primed || !slices.Equal(gotOrder, r.streams) || !slices.Equal(gotBenches, r.benches) ||
		(friendSet != nil && !slices.Equal(gotFriends, r.friends))
	r.primed = true
	if changed {
		r.streams, r.benches, r.friends = gotOrder, gotBenches, gotFriends
		return nil, true, nil
	}

	snap := &SprintSnapshot{Config: cfg, DoneBase: map[string]string{}}
	if pit != nil && pit.Val() > 0 {
		snap.Pitstop = true
	}
	if msgs, err := log.Result(); err == nil {
		for _, m := range msgs {
			if to, _ := m.Values["to"].(string); to == "landed" {
				snap.LandedHour++
			}
		}
	}
	if h, err := base.Result(); err == nil {
		snap.DoneBase = h
	}
	if lock != nil {
		if n, err := lock.Int64(); err != nil || n == 0 {
			snap.LockLost = true
		}
	}
	for i, s := range r.streams {
		row := StreamRow{Name: s}
		cells := []*int64{&row.Waiting, &row.Ready, &row.Working, &row.Merging, &row.Landed}
		for j, c := range counts[i] {
			*cells[j] = c.Val()
		}
		snap.Streams = append(snap.Streams, row)
	}
	for i, b := range r.benches {
		h, err := hashes[i].Result()
		if err != nil || len(h) == 0 {
			continue
		}
		fields := make(map[string]string, len(h))
		for f, v := range h {
			fields[f] = sanitize(v)
		}
		snap.Benches = append(snap.Benches, BenchRow{Key: b, Fields: fields})
	}
	for i, f := range roster {
		row := FriendRow{Name: f}
		if got, err := rows[i].Result(); err == nil && len(got) == 5 {
			row.At, row.Up, row.Ready, row.Working, row.Done = pipeValue(got[0]), pipeValue(got[1]), pipeValue(got[2]), pipeValue(got[3]), pipeValue(got[4])
		}
		if downs[i].Val() > 0 {
			row.Down = "down"
		}
		snap.Friends = append(snap.Friends, row)
	}
	return snap, false, nil
}

// logWindowMax bounds the hour of ws:log one tick reads; a sprint moving more
// than this many tasks an hour shows at least this rate.
const logWindowMax = 5000

// FailedSprint is the tick whose read failed: last's rows (none when there
// never was a good read) and a stale line.
func FailedSprint(cfg SprintConfig, last *SprintSnapshot) *SprintSnapshot {
	if last == nil {
		return &SprintSnapshot{Config: cfg, Stale: true}
	}
	snap := *last
	snap.Stale = true
	return &snap
}

const streamRule = "-------------------------------+---------+---------+---------+-------\n"

// XY is the headline's numbers: y is every task in the streams of ws:order,
// left is y minus landed, eta is left over the landed rate of the last hour
// (at least 1 an hour, so a stall shows as a big number, never infinity).
func (s *SprintSnapshot) XY() (left, y, pct, eta int64) {
	var landed int64
	for _, r := range s.Streams {
		y += r.Total()
		landed += r.Landed
	}
	left = y - landed
	if y > 0 {
		pct = landed * 100 / y
	}
	rate := s.LandedHour
	if rate < 1 {
		rate = 1
	}
	return left, y, pct, left * 60 / rate
}

// Render prints the whole table at now.
func (s *SprintSnapshot) Render(now time.Time) string {
	var b strings.Builder
	b.Grow(4096)
	if s.Pitstop {
		b.WriteString("SPRINT TABLE *** PIT STOP ***\n\n")
	} else {
		b.WriteString("SPRINT TABLE\n\n")
	}
	if s.Stale && s.LastGood.IsZero() {
		b.WriteString("stale: never read (Redis did not answer since start)\n")
		return b.String()
	}
	left, y, pct, eta := s.XY()
	fmt.Fprintf(&b, "%d/%d left, %d%% done -> ~%dm\n\n", left, y, pct, eta)

	fmt.Fprintf(&b, "%-30s | %7s | %7s | %7s | %6s\n", "stream", "waiting", "working", "merging", "landed")
	b.WriteString(streamRule)
	var tw, tk, tm, tl int64
	for _, r := range s.Streams {
		if r.Total() == 0 {
			continue
		}
		// waiting counts ready too: both are work nobody has started.
		fmt.Fprintf(&b, "%-30s | %7d | %7d | %7d | %6d\n", r.Name, r.Waiting+r.Ready, r.Working, r.Merging, r.Landed)
		tw, tk, tm, tl = tw+r.Waiting+r.Ready, tk+r.Working, tm+r.Merging, tl+r.Landed
	}
	b.WriteString(streamRule)
	fmt.Fprintf(&b, "%-30s | %7d | %7d | %7d | %6d\n\n", "total", tw, tk, tm, tl)

	fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %-10s\n", "friend", "ready", "working", "done", "status")
	b.WriteString(liveFriendRule)
	var fq, fw, fd int64
	for _, row := range s.Friends {
		q, w, d := orDash(row.Ready), orDash(row.Working), orDash(s.friendDone(row))
		fq, fw, fd = fq+digitsOnly(q), fw+digitsOnly(w), fd+digitsOnly(d)
		status := "down"
		if friendState(row, now, s.Config.RowStale) == "up" {
			status = "up"
		}
		fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %-10s\n", row.Name, q, w, d, status)
	}
	b.WriteString(liveFriendRule)
	fmt.Fprintf(&b, "%-10s | %5d | %7d | %5d |\n\n", "total", fq, fw, fd)

	fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %5s | %5s | %4s | %6s\n", "host", "ready", "working", "done", "ok", "fail", "ok%", "load")
	b.WriteString(liveBenchRule)
	var hq, hw int64
	for _, row := range s.Benches {
		c, show := row.cells(now)
		if !show {
			continue
		}
		// done/ok/fail are the swarm's counts, dashed while no swarm sprint
		// runs (sprint-table-redis HOST_COUNTS=0, Glenn 2026-09-22 7:00 PM).
		fmt.Fprintf(&b, "%-10s | %5s | %7d | %5s | %5s | %5s | %4s | %6s\n", c.host, c.queue, c.working, "-", "-", "-", "-", c.load)
		qn, _ := strconv.ParseInt(c.queue, 10, 64)
		hq, hw = hq+qn, hw+c.working
	}
	b.WriteString(liveBenchRule)
	fmt.Fprintf(&b, "%-10s | %5d | %7d | %5s | %5s | %5s | %4s |\n", "total", hq, hw, "-", "-", "-", "-")
	if s.Stale {
		fmt.Fprintf(&b, "stale: %ds (Redis did not answer; rows are the last good read)\n", now.Unix()-s.LastGood.Unix())
	}
	return b.String()
}

// friendDone is the row's done count minus its count at the last clear. A
// count below the base means the counter itself restarted (a new sprint's
// index sets), so the base no longer applies and the count shows as is.
func (s *SprintSnapshot) friendDone(row FriendRow) string {
	done, base := row.Done, s.DoneBase[row.Name]
	if done == "" || base == "" || strings.Trim(done, "0123456789") != "" || strings.Trim(base, "0123456789") != "" {
		return done
	}
	d, _ := strconv.ParseInt(done, 10, 64)
	b, _ := strconv.ParseInt(base, 10, 64)
	if d < b {
		return done
	}
	return strconv.FormatInt(d-b, 10)
}

// The writer's lock: one table writer per key, fleet-wide. AcquireLock takes
// it with SET NX PX; every tick then runs lockRefreshScript in its own
// pipeline, which extends the lease while the key still holds this writer's
// token, re-takes it if it expired, and returns 0 when someone else holds it.
const lockRefreshScript = `local v = redis.call('GET', KEYS[1])
if v == ARGV[1] then return redis.call('PEXPIRE', KEYS[1], ARGV[2]) end
if not v then redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2]); return 2 end
return 0`

const lockReleaseScript = `if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0`

func lockTTL(cfg SprintConfig) time.Duration {
	if cfg.LockTTL > 0 {
		return cfg.LockTTL
	}
	return 5 * time.Second
}

// AcquireLock takes key for token, or returns the holder's token.
func AcquireLock(ctx context.Context, client redis.UniversalClient, key, token string, ttl time.Duration) (holder string, err error) {
	ok, err := client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return "", err
	}
	if ok {
		return token, nil
	}
	holder, err = client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return AcquireLock(ctx, client, key, token, ttl)
	}
	return holder, err
}

// ReleaseLock deletes key only while it still holds token.
func ReleaseLock(ctx context.Context, client redis.UniversalClient, key, token string) error {
	return client.Eval(ctx, lockReleaseScript, []string{key}, token).Err()
}
