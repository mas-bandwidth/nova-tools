package swarm

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/redis/go-redis/v9"
)

// BenchGrant is a server-clock record, independent of slot liveness leases.
// Its first passed deadline is an immutable obligation (tla/BenchGrant.tla).
type BenchGrant struct {
	ID, Bench, Grantee, By          string
	Granted, Deadline, OwedDeadline time.Time
	Ended, Told                     bool
}

const GrantPrefix = "swarm:grants:"
const grantIndex = GrantPrefix + "index"
const grantChanges = GrantPrefix + "changes"

// GrantBackstop bounds retry after a failed expiry send; idle watchers do not poll.
const GrantBackstop = time.Minute

// GrantSnapshot arms the change cursor before reading records and server time.
type GrantSnapshot struct {
	Grants []BenchGrant
	Now    time.Time
	Cursor string
}

// GrantStore supplies server-clock snapshots and deadline/change wakes.
type GrantStore interface {
	Snapshot(context.Context) (GrantSnapshot, error)
	Wait(context.Context, string, time.Time) error
}

// GrantWait is the next event; zero Until waits for a change indefinitely.
type GrantWait struct {
	Cursor   string
	Until    time.Time
	Failures []error
}

// GrantWatcher sweeps each wake and commits notifications with the bus marks.
type GrantWatcher struct {
	Store      GrantStore
	Bus        *bus.Bus
	From, Seat string
}

// expiry is the immutable first expiry obligation (tla/BenchGrant.tla, Due/Owed).
func (g BenchGrant) expiry(now time.Time) time.Time {
	if g.Told {
		return time.Time{}
	}
	if !g.OwedDeadline.IsZero() {
		return g.OwedDeadline
	}
	if !g.Ended && !now.Before(g.Deadline) {
		return g.Deadline
	}
	return time.Time{}
}

// Step implements one watcher event in tla/BenchGrant.tla, Wake/Sweep/Send.
// Send and its told mark commit atomically; a failed send stays due for the
// backstop. Once-key dedup assumes no stale sender resumes after seven days.
func (w GrantWatcher) Step(ctx context.Context) (GrantWait, error) {
	s, err := w.Store.Snapshot(ctx)
	if err != nil {
		return GrantWait{}, err
	}
	next := GrantWait{Cursor: s.Cursor}
	for _, g := range s.Grants {
		due := g.expiry(s.Now)
		if due.IsZero() {
			if !g.Ended && !g.Told {
				next.Until = earliestGrant(next.Until, g.Deadline)
			}
			continue
		}
		subject := fmt.Sprintf("BENCH GRANT EXPIRED %s grantee=%s deadline=%s", g.Bench, g.Grantee, due.UTC().Format(time.RFC3339))
		_, err := w.Bus.Send(ctx, bus.Message{From: w.From, To: []string{g.Grantee, w.Seat}, Kind: bus.KindStatus,
			Subject: subject, Body: "The recorded bench grant has expired; end it or record a new grant before further use.",
			Once: GrantPrefix + "once:" + g.ID, Marks: []bus.Mark{{Key: GrantPrefix + g.ID, Field: "told", Value: strconv.FormatInt(due.UnixMilli(), 10)}}})
		if err != nil {
			next.Failures = append(next.Failures, fmt.Errorf("grant %s: %w", g.ID, err))
			next.Until = earliestGrant(next.Until, s.Now.Add(GrantBackstop))
		}
	}
	return next, nil
}

func earliestGrant(a, b time.Time) time.Time {
	if a.IsZero() || b.Before(a) {
		return b
	}
	return a
}

// Watch sleeps on the change stream or server deadline, then sweeps again
// (tla/BenchGrant.tla, Wake); report exposes send failures while retaining retry.
func (w GrantWatcher) Watch(ctx context.Context, report func(error)) error {
	for ctx.Err() == nil {
		next, err := w.Step(ctx)
		if err != nil {
			return err
		}
		for _, failure := range next.Failures {
			report(failure)
		}
		if err := w.Store.Wait(ctx, next.Cursor, next.Until); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// RedisGrantStore keeps grants and the bus in the same Redis so expiry sends
// and told marks are one write (tla/BenchGrant.tla, Send).
type RedisGrantStore struct{ C *redis.Client }

// grantWrite is Add/Extend/End in tla/BenchGrant.tla. It captures a passed
// deadline before changing it: even an end or extension cannot erase the
// already-owed notice. TIME, the record, index and change are one atomic call.
const grantWrite = `
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local exists = redis.call('EXISTS', KEYS[1]) == 1
if ARGV[1] == 'add' then
 if exists then return redis.error_reply('grant id already exists; add a new grant') end
 redis.call('HSET', KEYS[1], 'bench', ARGV[3], 'grantee', ARGV[4], 'by', ARGV[5], 'granted', now, 'deadline', now + tonumber(ARGV[6]), 'ended', '0', 'owed', '0', 'told', '')
 redis.call('SADD', KEYS[2], ARGV[2])
else
 if not exists then return redis.error_reply('grant id is absent; list grants first') end
 local ended = redis.call('HGET', KEYS[1], 'ended')
 if ARGV[1] == 'extend' and ended == '1' then return redis.error_reply('grant has ended; add a new grant') end
 local due = tonumber(redis.call('HGET', KEYS[1], 'deadline'))
 if ended == '0' and now >= due and redis.call('HGET', KEYS[1], 'told') == '' and redis.call('HGET', KEYS[1], 'owed') == '0' then
  redis.call('HSET', KEYS[1], 'owed', due)
 end
 if ARGV[1] == 'extend' then redis.call('HSET', KEYS[1], 'deadline', now + tonumber(ARGV[6]))
 else redis.call('HSET', KEYS[1], 'ended', '1') end
end
redis.call('XADD', KEYS[3], '*', 'id', ARGV[2], 'action', ARGV[1])
return redis.call('HGETALL', KEYS[1])
`

// Change atomically creates, extends or ends one grant by the server clock
// (tla/BenchGrant.tla, Add/Extend/End). Extending uses now+duration, as add does.
func (s RedisGrantStore) Change(ctx context.Context, action string, g BenchGrant, duration time.Duration) (BenchGrant, error) {
	if action != "add" && action != "extend" && action != "end" {
		return BenchGrant{}, fmt.Errorf("grant action wants add, extend or end")
	}
	if action != "end" && duration < time.Millisecond {
		return BenchGrant{}, fmt.Errorf("grant duration wants at least 1ms")
	}
	if action == "add" {
		if strings.TrimSpace(g.Bench) == "" || strings.ContainsAny(g.Bench, " \t\r\n") || len(g.Bench) > 128 {
			return BenchGrant{}, fmt.Errorf("bench wants one nonempty name of at most 128 bytes")
		}
		if why := bus.CheckName(g.Grantee); why != "" {
			return BenchGrant{}, fmt.Errorf("grantee: %s", why)
		}
		if why := bus.CheckName(g.By); why != "" {
			return BenchGrant{}, fmt.Errorf("grant maker: %s", why)
		}
		g.ID = strings.ToLower(rand.Text())
	}
	if g.ID == "" || strings.ContainsAny(g.ID, ": \t\r\n") {
		return BenchGrant{}, fmt.Errorf("grant id wants the id returned by grant list")
	}
	call, cancel := context.WithTimeout(ctx, bus.CallTimeout)
	defer cancel()
	fields, err := s.C.Eval(call, grantWrite, []string{GrantPrefix + g.ID, grantIndex, grantChanges}, action, g.ID, g.Bench, g.Grantee, g.By, duration.Milliseconds()).StringSlice()
	if err != nil {
		return BenchGrant{}, err
	}
	values := make(map[string]string, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		values[fields[i]] = fields[i+1]
	}
	return readBenchGrant(g.ID, values)
}

// readBenchGrant refuses incomplete records instead of guessing their deadline.
func readBenchGrant(id string, v map[string]string) (BenchGrant, error) {
	g := BenchGrant{ID: id, Bench: v["bench"], Grantee: v["grantee"], By: v["by"], Ended: v["ended"] == "1", Told: v["told"] != ""}
	if g.Bench == "" || g.Grantee == "" || g.By == "" || (v["ended"] != "0" && v["ended"] != "1") {
		return g, fmt.Errorf("grant %s is incomplete; inspect its store record", id)
	}
	for _, field := range []struct {
		name   string
		target *time.Time
	}{{"granted", &g.Granted}, {"deadline", &g.Deadline}, {"owed", &g.OwedDeadline}} {
		n, err := strconv.ParseInt(v[field.name], 10, 64)
		if err != nil || n < 0 {
			return g, fmt.Errorf("grant %s has invalid %s; inspect its store record", id, field.name)
		}
		if n != 0 {
			*field.target = time.UnixMilli(n).UTC()
		}
	}
	return g, nil
}

// Snapshot uses two batched round trips, never one per row. The first arms
// the stream cursor and captures ids atomically; the second reads all hashes
// and TIME atomically. A concurrent add or edit remains past the cursor and
// wakes the next sweep (tla/BenchGrant.tla, changed and Wake).
func (s RedisGrantStore) Snapshot(ctx context.Context) (GrantSnapshot, error) {
	call, cancel := context.WithTimeout(ctx, bus.CallTimeout)
	defer cancel()
	p := s.C.TxPipeline()
	ids := p.SMembers(call, grantIndex)
	tail := p.XRevRangeN(call, grantChanges, "+", "-", 1)
	if _, err := p.Exec(call); err != nil {
		return GrantSnapshot{}, err
	}
	names := ids.Val()
	sort.Strings(names)
	out := GrantSnapshot{Cursor: "0-0"}
	if len(tail.Val()) > 0 {
		out.Cursor = tail.Val()[0].ID
	}
	p = s.C.TxPipeline()
	now := p.Time(call)
	rows := make([]*redis.MapStringStringCmd, len(names))
	for i, id := range names {
		rows[i] = p.HGetAll(call, GrantPrefix+id)
	}
	if _, err := p.Exec(call); err != nil {
		return GrantSnapshot{}, err
	}
	out.Now = now.Val().UTC()
	for i, row := range rows {
		g, err := readBenchGrant(names[i], row.Val())
		if err != nil {
			return GrantSnapshot{}, err
		}
		out.Grants = append(out.Grants, g)
	}
	return out, nil
}

// Wait uses Redis TIME immediately before XREAD BLOCK; deadline arithmetic
// never reads the client clock (tla/BenchGrant.tla, Tick/Wake). The cursor
// predates the snapshot, so a concurrent earlier deadline cannot be missed.
func (s RedisGrantStore) Wait(ctx context.Context, cursor string, until time.Time) error {
	block := time.Duration(0)
	if !until.IsZero() {
		call, cancel := context.WithTimeout(ctx, bus.CallTimeout)
		now, err := s.C.Time(call).Result()
		cancel()
		if err != nil {
			return err
		}
		block = until.Sub(now)
		if block <= 0 {
			return nil
		}
		block = max(time.Millisecond, block)
	}
	_, err := s.C.XRead(ctx, &redis.XReadArgs{Streams: []string{grantChanges, cursor}, Count: 1, Block: block}).Result()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	return err
}
