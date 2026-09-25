// Package redisq is the live-state half of docs/SPEC-STATE.md that a bench uses beside
// the directory queue: the pull queue built on Redis Streams with one consumer group per
// stream, the slot lease whose renew and release are a Lua compare-and-release, and the
// in-flight cap whose admission is one atomic Lua script. Nothing here is a second copy
// of anything: a stream is the queue, XACK on clip is the record, XAUTOCLAIM is the
// reaper, SET NX PX is the lease and Lua is the fence, and a sorted set scored by each
// call's own deadline is the counter.
//
// The directory fallback is chosen at start and never mixed with Redis: a bench that
// chose the file mode never touches a Redis key, and a bench that chose Redis never
// writes a file lease. One slot with two modes would be a fence no token can see across.
package redisq

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Group is the one consumer group per stream, shared by every bench. The group is the
// work's and the consumer name is the bench's, so a card reaches exactly one bench and a
// second group over the same stream would duplicate the whole queue.
const Group = "workers"

// ConsumerLease is how long a delivered card may sit unacknowledged before the next
// puller reclaims it with XAUTOCLAIM. It is the lease whose heartbeat lapses, with no
// reaper to write.
const ConsumerLease = time.Minute

// The key namespaces of the spec: `swarm:lease:<store>:<slot>` and
// `swarm:cap:<provider>:<model>`, every key carrying its owner prefix and a TTL.
const (
	LeasePrefix = "swarm:lease:"
	CapPrefix   = "swarm:cap:"
)

// Mode is the one mode a bench chooses at start: the Redis store or the directory
// fallback. It never runs both at once.
type Mode string

const (
	ModeRedis     Mode = "redis"
	ModeDirectory Mode = "directory"
)

// ChooseMode records the mode at start: a bench with a Redis address reads and writes
// there, and a bench with none takes the directory fallback it already has.
func ChooseMode(redisAddr string) Mode {
	if strings.TrimSpace(redisAddr) == "" {
		return ModeDirectory
	}
	return ModeRedis
}

// Card is one stream entry: its id, the stream it came from, and the fields the expander
// or `nova-pulse cut` wrote (card, repo, base, branch, budget, affinity, inputs).
type Card struct {
	ID     string
	Stream string
	Fields map[string]string
}

// Lease is one held slot: the key, the random fencing token minted at take time, and the
// moment it lapses. A stale holder can neither renew nor release because renew and release
// compare this token to the stored one and are a no-op on a mismatch.
type Lease struct {
	Key   string
	Token string
	Until time.Time
}

// Queue is the handle on one Redis instance. The clock is injected so a test can move
// "now" without waiting, and it is the only place a deadline comes from.
type Queue struct {
	rdb *redis.Client
	now func() time.Time
}

// Open dials the instance at addr (host:port) and proves the connection once.
func Open(addr string) (*Queue, error) {
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	q := &Queue{rdb: rdb, now: func() time.Time { return time.Now().UTC() }}
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis at %s: %w", addr, err)
	}
	return q, nil
}

// Close releases the connection pool.
func (q *Queue) Close() error {
	if q == nil || q.rdb == nil {
		return nil
	}
	return q.rdb.Close()
}

// SetClock installs the clock the cap's "now" and the lease's until= come from.
func (q *Queue) SetClock(now func() time.Time) {
	if now != nil {
		q.now = now
	}
}

// Now is the queue's current time.
func (q *Queue) Now() time.Time { return q.now() }

// ----------------------------------------------------------------------------- streams

// EnsureGroup creates the workers group on stream, making the stream if it is absent. A
// group that already exists is not an error: every bench shares it, and the second bench
// to start must not fail on a stream the first one made.
func (q *Queue) EnsureGroup(ctx context.Context, stream string) error {
	err := q.rdb.XGroupCreateMkStream(ctx, stream, Group, "$").Err()
	if err != nil && strings.Contains(err.Error(), "BUSYGROUP") {
		return nil
	}
	return err
}

// Add appends one card to stream and returns its id. The stream is
// nova:queue:<kind>:<lane>; the caller names it, this tool never guesses it.
func (q *Queue) Add(ctx context.Context, stream string, fields map[string]string) (string, error) {
	values := make(map[string]interface{}, len(fields))
	for k, v := range fields {
		values[k] = v
	}
	return q.rdb.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: values}).Result()
}

// Pull reads at most one new card for bench with XREADGROUP over the workers group. A
// block of zero or less is a single non-blocking read: the caller decides whether to wait.
// The card stays in the group's pending list until Ack, which is what makes a dead
// worker's card reclaimable.
func (q *Queue) Pull(ctx context.Context, stream, bench string, block time.Duration) (*Card, error) {
	blockMs := int64(-1)
	if block > 0 {
		blockMs = block.Milliseconds()
	}
	res, err := q.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    Group,
		Consumer: bench,
		Streams:  []string{stream, ">"},
		Count:    1,
		Block:    time.Duration(blockMs) * time.Millisecond,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return firstCard(stream, res), nil
}

// Claim reclaims at most one card in the workers group whose delivery is older than
// minIdle, handing it to bench. It is SPEC-JOBS's lease whose heartbeat lapsed, with no
// reaper to write: a live worker keeps reclaiming its own card, a dead one simply stops.
func (q *Queue) Claim(ctx context.Context, stream, bench string, minIdle time.Duration) (*Card, error) {
	msgs, _, err := q.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   stream,
		Group:    Group,
		Consumer: bench,
		MinIdle:  minIdle,
		Start:    "0-0",
		Count:    1,
	}).Result()
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	m := msgs[0]
	return &Card{ID: m.ID, Stream: stream, Fields: stringFields(m.Values)}, nil
}

// Ack is the clip: a card is acked only after it has landed, so an acked card is safe to
// forget and only an acked card is.
func (q *Queue) Ack(ctx context.Context, stream, id string) error {
	return q.rdb.XAck(ctx, stream, Group, id).Err()
}

// PendingIDs is the group's pending list for bench, oldest first. A card a consumer
// pulled and never acked is here until it is reclaimed and acked.
func (q *Queue) PendingIDs(ctx context.Context, stream, bench string) ([]string, error) {
	rows, err := q.rdb.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream:   stream,
		Group:    Group,
		Consumer: bench,
		Start:    "-",
		End:      "+",
		Count:    1024,
	}).Result()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids, nil
}

func firstCard(stream string, res []redis.XStream) *Card {
	for _, xs := range res {
		if len(xs.Messages) > 0 {
			m := xs.Messages[0]
			return &Card{ID: m.ID, Stream: stream, Fields: stringFields(m.Values)}
		}
	}
	return nil
}

func stringFields(values map[string]interface{}) map[string]string {
	out := make(map[string]string, len(values))
	for k, v := range values {
		out[k] = fmt.Sprint(v)
	}
	return out
}

// ------------------------------------------------------------------------------ leases

// LeaseKey is the one key a slot's lease lives under.
func (q *Queue) LeaseKey(store, slot string) string {
	return LeasePrefix + store + ":" + slot
}

// TakeLease mints a random fencing token and claims the key with SET NX PX. A lease
// appears only if it won the key; a key already held is (nil, false, nil).
func (q *Queue) TakeLease(ctx context.Context, store, slot string, ttl time.Duration) (*Lease, bool, error) {
	token, err := mintToken()
	if err != nil {
		return nil, false, err
	}
	key := q.LeaseKey(store, slot)
	ok, err := q.rdb.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	return &Lease{Key: key, Token: token, Until: q.now().Add(ttl)}, true, nil
}

// renewScript is the only way a lease's life moves: the stored token must match the
// caller's, or the script is a no-op. A bare EXPIRE would let an old owner revive a lease
// it no longer holds.
var renewScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0
`)

// releaseScript is the same token check, then DEL.
var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

// RenewLease extends a lease only while its token still holds the key.
func (q *Queue) RenewLease(ctx context.Context, l *Lease, ttl time.Duration) (bool, error) {
	if l == nil {
		return false, nil
	}
	n, err := renewScript.Run(ctx, q.rdb, []string{l.Key}, l.Token, ttl.Milliseconds()).Int64()
	if err != nil {
		return false, err
	}
	if n == 1 {
		l.Until = q.now().Add(ttl)
	}
	return n == 1, nil
}

// ReleaseLease drops a lease only while its token still holds the key.
func (q *Queue) ReleaseLease(ctx context.Context, l *Lease) (bool, error) {
	if l == nil {
		return false, nil
	}
	n, err := releaseScript.Run(ctx, q.rdb, []string{l.Key}, l.Token).Int64()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// LeaseToken is the token currently stored under key, or the empty string.
func (q *Queue) LeaseToken(ctx context.Context, key string) (string, error) {
	token, err := q.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return token, err
}

// LeaseCount is the number of leases in the Redis store, by SCAN over the prefix (never
// KEYS: an instance is shared by every bench).
func (q *Queue) LeaseCount(ctx context.Context) (int, error) {
	var n int
	var cursor uint64
	for {
		keys, next, err := q.rdb.Scan(ctx, cursor, LeasePrefix+"*", 128).Result()
		if err != nil {
			return 0, err
		}
		n += len(keys)
		cursor = next
		if cursor == 0 {
			return n, nil
		}
	}
}

func mintToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// SlotLeases is the slot store a bench uses, in the one mode it chose at start. It is
// deliberately thin: the mode is fixed when it is made, so no call can mix the two.
type SlotLeases struct {
	mode Mode
	q    *Queue
	dir  string
}

// NewSlotLeases fixes the mode. Redis mode wants a Queue; directory mode wants the root
// the lease files live under.
func NewSlotLeases(mode Mode, q *Queue, dir string) *SlotLeases {
	return &SlotLeases{mode: mode, q: q, dir: dir}
}

// Mode is the mode this store was made in.
func (s *SlotLeases) Mode() Mode { return s.mode }

// Take claims one slot for ttl in the chosen mode. A slot already held is (nil, false, nil)
// in either mode: os.OpenFile with O_EXCL is Redis's SET NX on the file side.
func (s *SlotLeases) Take(ctx context.Context, store, slot string, ttl time.Duration) (*Lease, bool, error) {
	if s.mode == ModeRedis {
		if s.q == nil {
			return nil, false, errors.New("the redis lease mode has no queue")
		}
		return s.q.TakeLease(ctx, store, slot, ttl)
	}
	token, err := mintToken()
	if err != nil {
		return nil, false, err
	}
	path := filepath.Join(s.dir, store, slot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		f.Close()
		return nil, false, err
	}
	if err := f.Close(); err != nil {
		return nil, false, err
	}
	return &Lease{Key: path, Token: token, Until: time.Now().Add(ttl)}, true, nil
}

// Renew extends a lease only while its token holds it, in the chosen mode.
func (s *SlotLeases) Renew(ctx context.Context, l *Lease, ttl time.Duration) (bool, error) {
	if s.mode == ModeRedis {
		return s.q.RenewLease(ctx, l, ttl)
	}
	token, err := readToken(l.Key)
	if err != nil || token != l.Token {
		return false, nil
	}
	if err := os.WriteFile(l.Key, []byte(l.Token+"\n"), 0o644); err != nil {
		return false, err
	}
	l.Until = time.Now().Add(ttl)
	return true, nil
}

// Release drops a lease only while its token holds it, in the chosen mode.
func (s *SlotLeases) Release(ctx context.Context, l *Lease) (bool, error) {
	if s.mode == ModeRedis {
		return s.q.ReleaseLease(ctx, l)
	}
	token, err := readToken(l.Key)
	if err != nil || token != l.Token {
		return false, nil
	}
	if err := os.Remove(l.Key); err != nil {
		return false, err
	}
	return true, nil
}

func readToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// --------------------------------------------------------------------------------- caps

// CapKey is the sorted set one (provider, model) calls against, scored by each call's own
// deadline. The same script serves the per-provider and the per-key cap by naming them.
func (q *Queue) CapKey(provider, model string) string {
	return CapPrefix + provider + ":" + model
}

// admitScript is one atomic INCR-with-limit: a lapsed call frees its seat, the count is
// taken and the seat added inside the one script, so no two admissions interleave between
// the count and the add and a check-then-add split over two round trips cannot admit a
// call past the cap.
var admitScript = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', '(' .. ARGV[4])
if redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[1]) then
  return 0
end
redis.call('ZADD', KEYS[1], tonumber(ARGV[2]), ARGV[3])
return 1
`)

// Admit asks the cap for one seat for callID, whose own deadline is when the seat lapses.
// It returns true when the call is admitted and false when the cap is full.
func (q *Queue) Admit(ctx context.Context, provider, model string, capN int, deadline time.Time, callID string) (bool, error) {
	n, err := admitScript.Run(ctx, q.rdb, []string{q.CapKey(provider, model)},
		capN, deadline.UnixMilli(), callID, q.now().UnixMilli()).Int64()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// Inflight is the number of calls currently holding a seat, lapsed ones included until the
// next admission reaps them in the script.
func (q *Queue) Inflight(ctx context.Context, provider, model string) (int64, error) {
	return q.rdb.ZCard(ctx, q.CapKey(provider, model)).Result()
}
