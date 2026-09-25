package events

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore is the live stream on the fleet Redis. go-redis is already this repository's
// Redis client (internal/redisq, internal/record, internal/ci), so this adds no dependency;
// the five calls below are XADD, XREADGROUP, XAUTOCLAIM, XACK and XRANGE, which is the whole
// of what "core types only" allows.
type RedisStore struct {
	rdb    *redis.Client
	stream string
	now    func() time.Time
}

// Dial options. The password never arrives as a flag or an argument: the caller reads it
// from the environment `nova-secrets exec` put it in, and nothing here ever prints it.
type Dial struct {
	Addr     string
	Username string
	Password string
	Stream   string
}

// Open names the store and sends nothing (#3277): the first read or write dials, so a bad
// address fails the caller's first batch instead of every open paying a PING round trip.
func Open(ctx context.Context, d Dial) (*RedisStore, error) {
	stream := strings.TrimSpace(d.Stream)
	if stream == "" {
		stream = Stream
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:     d.Addr,
		Username: d.Username,
		Password: d.Password,
	})
	return &RedisStore{rdb: rdb, stream: stream, now: func() time.Time { return time.Now().UTC() }}, nil
}

// Reach sends one PING, for a caller that must refuse before costly work
// rather than find the store down after it; Open itself sends nothing (#3277).
func (s *RedisStore) Reach(ctx context.Context) error {
	if err := s.rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis at %s: %w", s.rdb.Options().Addr, err)
	}
	return nil
}

// StreamName is the stream this store reads and writes.
func (s *RedisStore) StreamName() string { return s.stream }

// SetClock installs the clock Emit stamps an unstamped event with.
func (s *RedisStore) SetClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// Close releases the pool.
func (s *RedisStore) Close() error {
	if s == nil || s.rdb == nil {
		return nil
	}
	return s.rdb.Close()
}

// Emit is the one XADD: `XADD <stream> MAXLEN ~ 1000000 * <fields>`. The event is validated
// first, so nothing but ids and counts can ever reach the store.
func (s *RedisStore) Emit(ctx context.Context, e Event) (string, error) {
	e = e.Stamp(s.now())
	if err := e.Validate(); err != nil {
		return "", err
	}
	return s.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: s.stream,
		MaxLen: MaxLen,
		Approx: true,
		Values: e.Values(),
	}).Result()
}

// EnsureGroup creates the group at entry 0, making the stream if it is absent. A group that
// already exists is not an error: every fold start runs this.
func (s *RedisStore) EnsureGroup(ctx context.Context, group string) error {
	err := s.rdb.XGroupCreateMkStream(ctx, s.stream, group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("consumer group %s on %s: %w", group, s.stream, err)
	}
	return nil
}

// Pending claims every entry this group holds unacked, from any consumer, oldest first.
// MinIdle is zero on purpose: the fold is a single consumer and a restart must take its own
// dead predecessor's work back at once, not after an idle timeout.
func (s *RedisStore) Pending(ctx context.Context, group, consumer string, count int) ([]Entry, error) {
	if count <= 0 {
		count = 100
	}
	msgs, _, err := s.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   s.stream,
		Group:    group,
		Consumer: consumer,
		MinIdle:  0,
		Start:    "0-0",
		Count:    int64(count),
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return entries(msgs), nil
}

// Next takes up to count new entries, blocking for at most block. Zero block is never
// passed to Redis, which reads BLOCK 0 as "forever".
func (s *RedisStore) Next(ctx context.Context, group, consumer string, count int, block time.Duration) ([]Entry, error) {
	if count <= 0 {
		count = 100
	}
	if block <= 0 {
		block = -1
	}
	res, err := s.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{s.stream, ">"},
		Count:    int64(count),
		Block:    block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, st := range res {
		out = append(out, entries(st.Messages)...)
	}
	return out, nil
}

// Ack commits the delivery. It runs after the fold's own transaction, never before.
func (s *RedisStore) Ack(ctx context.Context, group string, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.rdb.XAck(ctx, s.stream, group, ids...).Err()
}

// Range reads the stream itself from start, forwards, touching no group. It is the replay
// `fold --rebuild` walks.
func (s *RedisStore) Range(ctx context.Context, start string, count int) ([]Entry, error) {
	if count <= 0 {
		count = 100
	}
	if strings.TrimSpace(start) == "" {
		start = "-"
	}
	msgs, err := s.rdb.XRangeN(ctx, s.stream, start, "+", int64(count)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return entries(msgs), nil
}

func entries(msgs []redis.XMessage) []Entry {
	out := make([]Entry, 0, len(msgs))
	for _, m := range msgs {
		fields := make(map[string]string, len(m.Values))
		for k, v := range m.Values {
			if s, ok := v.(string); ok {
				fields[k] = s
			} else {
				fields[k] = fmt.Sprint(v)
			}
		}
		out = append(out, Entry{ID: m.ID, Fields: fields})
	}
	return out
}
