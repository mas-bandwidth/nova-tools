package bus

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/redis/go-redis/v9"
)

// The sets nova-config's apply fills with the names of its friend and machine
// rows (internal/config/redis.go: FriendsKey, MachinesKey), read here by name
// so this package depends on no config code.
const (
	friendsKey  = "friends"
	machinesKey = "machines"
)

// The deadlines (SPEC-BUS.md, the deadlines). CallTimeout bounds every call
// that does not block; a blocking read gets the time it asked for and
// BlockMargin more, on top of Timeout. One retry, and only for a read that
// changes nothing.
const (
	CallTimeout = 5 * time.Second
	BlockMargin = 10 * time.Second
)

// Redis is the Store over one go-redis client. Each method is one round trip,
// under a deadline of Timeout (CallTimeout when zero); the client must honour
// a context's deadline (redisconn sets ContextTimeoutEnabled).
type Redis struct {
	C       *redis.Client
	Timeout time.Duration
	Margin  time.Duration // a blocking read's wait beyond its block (BlockMargin when zero)
}

// TimeoutError is a call the store did not answer within After. It names the
// address and never the login.
type TimeoutError struct {
	Addr  string
	After time.Duration
	cause error
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("redis did not answer within %s at %s: the host may be overloaded (load average), try again", e.After, e.Addr)
}

func (e *TimeoutError) Unwrap() error   { return e.cause }
func (e *TimeoutError) Timeout() bool   { return true }
func (e *TimeoutError) Temporary() bool { return true }

// call runs fn under the deadline: Timeout plus extra (a blocking read's own
// wait). A deadline that runs out is a *TimeoutError. With retry set, the
// call is made a second time, once, when the first ran out of time and the
// caller's own context is still live; a send, an ack and a read that hands
// out entries never set it, since a second try could act twice
// (SPEC-BUS.md, the deadlines).
func (r Redis) call(ctx context.Context, retry bool, extra time.Duration, fn func(ctx context.Context) error) error {
	d := r.Timeout
	if d <= 0 {
		d = CallTimeout
	}
	d += extra
	for attempt := 0; ; attempt++ {
		cctx, cancel := context.WithTimeout(ctx, d)
		err := fn(cctx)
		cancel()
		var netErr net.Error
		timedOut := errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
		if err == nil || ctx.Err() != nil || !timedOut {
			return err
		}
		if retry && attempt == 0 {
			continue
		}
		return &TimeoutError{Addr: r.C.Options().Addr, After: d, cause: err}
	}
}

// margin is the fixed wait past a blocking read's block (SPEC-BUS.md, the deadlines).
func (r Redis) margin() time.Duration {
	if r.Margin > 0 {
		return r.Margin
	}
	return BlockMargin
}

func (r Redis) Roster(ctx context.Context) ([]string, time.Time, error) {
	friends, machines, now, err := r.Members(ctx)
	return append(friends, machines...), now, err
}

func (r Redis) Members(ctx context.Context) (friends, machines []string, now time.Time, err error) {
	err = r.call(ctx, true, 0, func(ctx context.Context) error {
		pipe := r.C.Pipeline()
		f := pipe.SMembers(ctx, friendsKey)
		m := pipe.SMembers(ctx, machinesKey)
		t := pipe.Time(ctx)
		if err := redisconn.Exec(ctx, pipe); err != nil {
			return err
		}
		friends, machines, now = f.Val(), m.Val(), t.Val()
		return nil
	})
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	return friends, machines, now, nil
}

func (r Redis) AddAll(ctx context.Context, streams []string, fields map[string]string, marks ...Mark) error {
	return r.call(ctx, false, 0, func(ctx context.Context) error {
		pipe := r.C.TxPipeline()
		for _, s := range streams {
			pipe.XAdd(ctx, &redis.XAddArgs{Stream: s, ID: "*", Values: toValues(fields)})
		}
		for _, m := range marks {
			switch {
			case m.Clear:
				pipe.HDel(ctx, m.Key, m.Field)
			case m.Forward:
				forward.Eval(ctx, pipe, []string{m.Key}, m.Value, m.Field) // the script's text: a pipeline cannot fall back from EVALSHA
			default:
				pipe.HSet(ctx, m.Key, m.Field, m.Value)
			}
		}
		return redisconn.Exec(ctx, pipe)
	})
}

// addOnce is AddOnce's one atomic step: Redis runs a script alone, so no
// other send under the key comes between its GET and its writes. KEYS are the
// record's key, the streams, then each mark's hash; ARGV are the record, its
// expiry in ms, the count of streams, the count of field pairs, the pairs,
// then each mark as "set" field value, "del" field or "fwd" field state.
var addOnce = redis.NewScript(forwardLua + `
local prior = redis.call('GET', KEYS[1])
if prior then return {1, prior} end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
local ns, nf = tonumber(ARGV[3]), tonumber(ARGV[4])
local fields = {}
for i = 1, nf * 2 do fields[i] = ARGV[4 + i] end
for i = 1, ns do redis.call('XADD', KEYS[1 + i], '*', unpack(fields)) end
local j, k = 5 + nf * 2, 2 + ns
local now = redis.call('TIME')[1]
while j <= #ARGV do
  if ARGV[j] == 'del' then
    redis.call('HDEL', KEYS[k], ARGV[j + 1])
    j = j + 2
  elseif ARGV[j] == 'fwd' then
    forward(KEYS[k], ARGV[j + 2], ARGV[j + 1], now)
    j = j + 3
  else
    redis.call('HSET', KEYS[k], ARGV[j + 1], ARGV[j + 2])
    j = j + 3
  end
  k = k + 1
end
return {0, ''}
`)

func (r Redis) AddOnce(ctx context.Context, key, record string, keep time.Duration, streams []string, fields map[string]string, marks ...Mark) (prior string, found bool, err error) {
	err = r.call(ctx, false, 0, func(ctx context.Context) error {
		keys := append([]string{key}, streams...)
		names := slices.Sorted(maps.Keys(fields))
		args := []any{record, keep.Milliseconds(), len(streams), len(names)}
		for _, n := range names {
			args = append(args, n, fields[n])
		}
		for _, m := range marks {
			keys = append(keys, m.Key)
			switch {
			case m.Clear:
				args = append(args, "del", m.Field)
			case m.Forward:
				args = append(args, "fwd", m.Field, m.Value)
			default:
				args = append(args, "set", m.Field, m.Value)
			}
		}
		res, err := addOnce.Run(ctx, r.C, keys, args...).Slice()
		if err != nil {
			return err
		}
		if len(res) != 2 {
			return fmt.Errorf("the send-once script answered %d values, not 2", len(res))
		}
		n, _ := res[0].(int64)     // ignored: anything but 1 is a write the script made
		prior, _ = res[1].(string) // ignored: as above, empty when it wrote
		found = n == 1
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return prior, found, nil
}

func (r Redis) Sent(ctx context.Context, key string) (v string, ok bool, err error) {
	err = r.call(ctx, true, 0, func(ctx context.Context) error {
		var err error
		v, err = r.C.Get(ctx, key).Result()
		if errors.Is(err, redis.Nil) {
			return nil
		}
		if err != nil {
			return err
		}
		ok = true
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return v, ok, nil
}

func (r Redis) Unmark(ctx context.Context, key string, fields ...string) (n int64, err error) {
	err = r.call(ctx, false, 0, func(ctx context.Context) (err error) {
		n, err = r.C.HDel(ctx, key, fields...).Result()
		return err
	})
	return n, err
}

func (r Redis) Marks(ctx context.Context, keys ...string) ([]map[string]string, error) {
	var out []map[string]string
	err := r.call(ctx, true, 0, func(ctx context.Context) error {
		pipe := r.C.Pipeline()
		cmds := make([]*redis.MapStringStringCmd, len(keys))
		for i, k := range keys {
			cmds[i] = pipe.HGetAll(ctx, k)
		}
		if err := redisconn.Exec(ctx, pipe); err != nil {
			return err
		}
		out = make([]map[string]string, len(keys))
		for i, c := range cmds {
			out[i] = c.Val()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// putHashExisting sets fields on key only when the key is already there, and
// does not touch its expiry. A missing key stays missing.
var putHashExisting = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then return 0 end
if #ARGV > 0 then redis.call('HSET', KEYS[1], unpack(ARGV)) end
return 1
`)

// PutHash is Store.PutHash: one pipelined HSET and PEXPIRE when ttl is above
// zero, else HSET of an existing key only.
func (r Redis) PutHash(ctx context.Context, key string, fields map[string]string, ttl time.Duration) error {
	return r.call(ctx, false, 0, func(ctx context.Context) error {
		if ttl <= 0 {
			args := make([]interface{}, 0, len(fields)*2)
			for k, v := range fields {
				args = append(args, k, v)
			}
			return putHashExisting.Run(ctx, r.C, []string{key}, args...).Err()
		}
		args := make([]interface{}, 0, len(fields)*2)
		for k, v := range fields {
			args = append(args, k, v)
		}
		pipe := r.C.TxPipeline()
		pipe.HSet(ctx, key, args...)
		pipe.PExpire(ctx, key, ttl)
		return redisconn.Exec(ctx, pipe)
	})
}

// forwardLua is the receipt rule (stages.go), as the store's scripts run
// it: the receipt of id on the hash key moves to state at now
// (the store's TIME, in seconds) only forward, and only delivered starts one;
// it answers the state before ("" none).
const forwardLua = `
local rank = {delivered = 1, read = 2, acted = 3}
local function forward(key, state, id, now)
  local cur = redis.call('HGET', key, id)
  local prior = ''
  if cur then prior = string.match(cur, '^[^ ]*') end
  local have, want = rank[prior] or 0, rank[state] or 0
  if want > have and (have > 0 or want == 1) then
    redis.call('HSET', key, id, state .. ' ' .. now)
  end
  return prior
end
`

// forward is Store.Forward's one atomic step: KEYS[1] the receipts hash,
// ARGV[1] the state, the rest the ids, each moved by forwardLua; the answer
// is each id's state before. A call carries the ids of one turn, never a table.
var forward = redis.NewScript(forwardLua + `
local now = redis.call('TIME')[1]
local out = {}
for i = 2, #ARGV do out[#out + 1] = forward(KEYS[1], ARGV[1], ARGV[i], now) end
return out
`)

func (r Redis) Forward(ctx context.Context, key, state string, ids ...string) ([]string, error) {
	var res []string
	err := r.call(ctx, false, 0, func(ctx context.Context) error {
		args := []any{state}
		for _, id := range ids {
			args = append(args, id)
		}
		var err error
		res, err = forward.Run(ctx, r.C, []string{key}, args...).StringSlice()
		if err != nil {
			return err
		}
		if len(res) != len(ids) {
			return fmt.Errorf("the receipt script answered %d states for %d ids", len(res), len(ids))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (r Redis) EnsureGroup(ctx context.Context, stream, group string) error {
	return r.call(ctx, false, 0, func(ctx context.Context) error {
		err := r.C.XGroupCreateMkStream(ctx, stream, group, "0").Err()
		if err != nil && strings.HasPrefix(err.Error(), "BUSYGROUP") {
			return nil // the group is there: what was asked for
		}
		return err
	})
}

func (r Redis) Claim(ctx context.Context, stream, group, consumer string, minIdle time.Duration, count int) ([]Entry, error) {
	var msgs []redis.XMessage
	err := r.call(ctx, false, 0, func(ctx context.Context) (err error) {
		msgs, _, err = r.C.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream: stream, Group: group, Consumer: consumer, MinIdle: minIdle, Start: "0-0", Count: int64(count),
		}).Result()
		return err
	})
	if err != nil {
		return nil, err
	}
	return entries(stream, msgs), nil
}

func (r Redis) Read(ctx context.Context, stream, group, consumer string, block time.Duration, count int) ([]Entry, error) {
	args := &redis.XReadGroupArgs{Group: group, Consumer: consumer, Streams: []string{stream, ">"}, Count: int64(count), Block: -1}
	var extra time.Duration
	if block > 0 {
		args.Block = block
		extra = block + r.margin() // SPEC-BUS.md, the deadlines: the block, and the margin more
	}
	var res []redis.XStream
	err := r.call(ctx, false, extra, func(ctx context.Context) (err error) {
		res, err = r.C.XReadGroup(ctx, args).Result()
		return err
	})
	if errors.Is(err, redis.Nil) {
		return nil, nil // the block ran out with nothing: not an error
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, s := range res {
		out = append(out, entries(s.Stream, s.Messages)...)
	}
	return out, nil
}

func (r Redis) Release(ctx context.Context, stream, group string, ids ...string) error {
	args := []any{"XCLAIM", stream, group, Consumer, 0}
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, "IDLE", ClaimAfter.Milliseconds(), "JUSTID")
	return r.call(ctx, false, 0, func(ctx context.Context) error { return r.C.Do(ctx, args...).Err() })
}

func (r Redis) Ack(ctx context.Context, stream, group string, ids ...string) (n int64, err error) {
	err = r.call(ctx, false, 0, func(ctx context.Context) (err error) {
		n, err = r.C.XAck(ctx, stream, group, ids...).Result()
		return err
	})
	return n, err
}

func (r Redis) Pending(ctx context.Context, stream, group string, count int) ([]string, error) {
	var rows []redis.XPendingExt
	err := r.call(ctx, true, 0, func(ctx context.Context) (err error) {
		rows, err = r.C.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: stream, Group: group, Start: "-", End: "+", Count: int64(count)}).Result()
		return err
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(rows))
	for i, p := range rows {
		ids[i] = p.ID
	}
	return ids, nil
}

func (r Redis) Group(ctx context.Context, stream, group string) (string, bool, error) {
	var groups []redis.XInfoGroup
	err := r.call(ctx, true, 0, func(ctx context.Context) (err error) {
		groups, err = r.C.XInfoGroups(ctx, stream).Result()
		return err
	})
	if err != nil {
		if strings.HasPrefix(err.Error(), "ERR no such key") {
			return "", false, nil
		}
		return "", false, err
	}
	for _, g := range groups {
		if g.Name == group {
			return g.LastDeliveredID, true, nil
		}
	}
	return "", false, nil
}

func (r Redis) Range(ctx context.Context, stream, from, to string, count int) ([]Entry, error) {
	var msgs []redis.XMessage
	err := r.call(ctx, true, 0, func(ctx context.Context) (err error) {
		if count > 0 {
			msgs, err = r.C.XRangeN(ctx, stream, from, to, int64(count)).Result()
		} else {
			msgs, err = r.C.XRange(ctx, stream, from, to).Result()
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return entries(stream, msgs), nil
}

func (r Redis) Get(ctx context.Context, stream string, ids []string) ([]Entry, error) {
	var out []Entry
	err := r.call(ctx, true, 0, func(ctx context.Context) error {
		pipe := r.C.Pipeline()
		cmds := make([]*redis.XMessageSliceCmd, len(ids))
		for i, id := range ids {
			cmds[i] = pipe.XRange(ctx, stream, id, id)
		}
		if err := redisconn.Exec(ctx, pipe); err != nil {
			return err
		}
		out = nil
		for _, c := range cmds {
			out = append(out, entries(stream, c.Val())...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Tail is the stream's last entry id (XINFO STREAM's last-generated-id);
// a stream that is not there answers so, and the caller arms at "0-0"
// (SPEC-BUS.md, the verbs: wait). The read changes nothing, so a stall is
// tried once more (SPEC-BUS.md, the deadlines).
func (r Redis) Tail(ctx context.Context, stream string) (id string, ok bool, err error) {
	err = r.call(ctx, true, 0, func(ctx context.Context) error {
		info, err := r.C.XInfoStream(ctx, stream).Result()
		if err != nil {
			return err
		}
		id, ok = info.LastGeneratedID, true
		return nil
	})
	if err != nil {
		if strings.HasPrefix(err.Error(), "ERR no such key") {
			return "", false, nil
		}
		return "", false, err
	}
	return id, ok, nil
}

// blockArg is the BLOCK a wait's read sends: 0 is for ever (the store holds
// the read until an entry is there), a positive duration is that long, and
// -1 is never sent -- that is recv's "do not block", not the wait's. A
// positive block is at least a millisecond: the client sends whole
// milliseconds, and a shorter one would reach the store as BLOCK 0, for ever
// (SPEC-BUS.md, the verbs: wait).
func blockArg(block time.Duration) time.Duration {
	if block <= 0 {
		return 0
	}
	return max(block, time.Millisecond)
}

// BlockRead is XREAD past the id after, waiting up to block (0 is for ever:
// one read the store holds until an entry is there), never the consumer
// group, so a later recv still delivers and acks what it handed out
// (SPEC-BUS.md, the verbs: wait). A positive block is bounded by Timeout plus
// the block plus the margin, and is not retried; block 0 keeps the caller's
// context, because a bus deadline would end a wait that asked to park
// (SPEC-BUS.md, the deadlines).
func (r Redis) BlockRead(ctx context.Context, stream, after string, block time.Duration, count int) ([]Entry, error) {
	if block <= 0 {
		return r.readBlock(ctx, stream, after, block, count)
	}
	var out []Entry
	err := r.call(ctx, false, block+r.margin(), func(ctx context.Context) error {
		var err error
		out, err = r.readBlock(ctx, stream, after, block, count)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r Redis) readBlock(ctx context.Context, stream, after string, block time.Duration, count int) ([]Entry, error) {
	args := &redis.XReadArgs{Streams: []string{stream, after}, Count: int64(count), Block: blockArg(block)}
	res, err := r.C.XRead(ctx, args).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil // the block ran out with nothing: not an error
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, s := range res {
		out = append(out, entries(s.Stream, s.Messages)...)
	}
	return out, nil
}

func toValues(fields map[string]string) map[string]any {
	v := make(map[string]any, len(fields))
	for k, s := range fields {
		v[k] = s
	}
	return v
}

func entries(stream string, msgs []redis.XMessage) []Entry {
	out := make([]Entry, 0, len(msgs))
	for _, m := range msgs {
		fields := make(map[string]string, len(m.Values))
		for k, v := range m.Values {
			fields[k] = fmt.Sprint(v)
		}
		out = append(out, Entry{Stream: stream, Entry: m.ID, Fields: fields})
	}
	return out
}
