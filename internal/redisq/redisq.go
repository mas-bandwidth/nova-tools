// Package redisq is the ready set of docs/SPEC-JOBS.md ("Redis ready set (the
// pull)"): one Redis stream carries the cards a fleet is expected to run, one
// consumer group lets a bench claim exactly one card, and one lease key says
// which bench is running it and for how long.
//
// Every Redis call a callerset makes goes through Client, and Client is the
// only thing the push and pull verbs talk to. The implementation on top of
// redis/go-redis is Open; the tests drive the same interface against
// github.com/alicebob/miniredis/v2, which runs in process and never opens a
// socket beyond loopback.
//
// Nothing here is a second copy of anything. The stream is the queue, XACK on
// completion is the record, XAUTOCLAIM after a lease lapses is the reaper, and
// a key with a TTL is the lease (SPEC-STATE wins for storage). No launch
// happens without a lease (SPEC-JOBS rule 2).
package redisq

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The ready set's fixed names. A caller may pass them but never spell them
// itself, so push and pull cannot drift apart.
const (
	// ReadyStream is where a card waits to be claimed.
	ReadyStream = "cards:ready"
	// DoneStream is where a completed card's outcome lands.
	DoneStream = "cards:done"
	// Group is the one consumer group every bench shares: the group is the
	// work's and the consumer name is the bench's, so a card reaches exactly
	// one bench.
	Group = "benches"
	// LeasePrefix is the one prefix a lease key carries: lease:<entry id>.
	LeasePrefix = "lease:"
)

// Entry is one stream entry: the id Redis assigned it and the fields it
// carries. Fields are strings because a stream field is bytes on the wire and
// this side never guesses a type.
type Entry struct {
	ID     string
	Fields map[string]string
}

// Field returns one field of the entry, or "" when the field is absent.
func (e *Entry) Field(name string) string {
	if e == nil {
		return ""
	}
	return e.Fields[name]
}

// Client is every Redis call this ready set makes. A verb holds a Client and
// nothing else, so the same logic runs against a live instance and against an
// in-process fake in a unit test.
type Client interface {
	// EnsureGroup makes the consumer group on a stream, creating the stream
	// when absent. A group that already exists is not an error.
	EnsureGroup(ctx context.Context, stream, group, start string) error
	// Add appends one entry to a stream and returns its id.
	Add(ctx context.Context, stream string, fields map[string]string) (string, error)
	// ReadGroup reads at most one never-delivered entry for consumer, blocking
	// up to block when the stream is empty. It returns (nil, nil) on timeout.
	ReadGroup(ctx context.Context, stream, group, consumer string, block time.Duration) (*Entry, error)
	// Ack clears an entry from the group's pending list.
	Ack(ctx context.Context, stream, group, id string) error
	// AutoClaim hands consumer at most one pending entry whose delivery is
	// older than minIdle. It returns (nil, nil) when none is claimable.
	AutoClaim(ctx context.Context, stream, group, consumer string, minIdle time.Duration) (*Entry, error)
	// AcquireLease claims key for value with a TTL, and reports false when the
	// key is already held.
	AcquireLease(ctx context.Context, key, value string, ttl time.Duration) (bool, error)
	// RenewLease extends key's TTL only while value still holds it.
	RenewLease(ctx context.Context, key, value string, ttl time.Duration) (bool, error)
	// ReleaseLease drops key only while value still holds it.
	ReleaseLease(ctx context.Context, key, value string) (bool, error)
	// LeaseValue is the value under key, or "" when the key is absent.
	LeaseValue(ctx context.Context, key string) (string, error)
	// Close releases the connection pool.
	Close() error
}

// redisClient is Client over redis/go-redis.
type redisClient struct {
	rdb *redis.Client
}

// Open dials the instance at addr (host:port) and proves the connection once,
// so a bench that cannot reach Redis refuses before it claims any card.
func Open(addr string) (Client, error) {
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		_ = rdb.Close()
		return nil, err
	}
	return &redisClient{rdb: rdb}, nil
}

func (c *redisClient) EnsureGroup(ctx context.Context, stream, group, start string) error {
	err := c.rdb.XGroupCreateMkStream(ctx, stream, group, start).Err()
	if err != nil && isBusyGroup(err) {
		return nil
	}
	return err
}

// isBusyGroup reports the one error XGROUP CREATE returns when the group
// already exists, so a second bench starting on the same stream is not an
// error.
func isBusyGroup(err error) bool {
	return err != nil && strings.Contains(err.Error(), "BUSYGROUP")
}

func (c *redisClient) Add(ctx context.Context, stream string, fields map[string]string) (string, error) {
	values := make(map[string]interface{}, len(fields))
	for k, v := range fields {
		values[k] = v
	}
	return c.rdb.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: values}).Result()
}

func (c *redisClient) ReadGroup(ctx context.Context, stream, group, consumer string, block time.Duration) (*Entry, error) {
	res, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{stream, ">"},
		Count:    1,
		Block:    block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, xs := range res {
		if len(xs.Messages) > 0 {
			m := xs.Messages[0]
			return &Entry{ID: m.ID, Fields: stringFields(m.Values)}, nil
		}
	}
	return nil, nil
}

func (c *redisClient) Ack(ctx context.Context, stream, group, id string) error {
	return c.rdb.XAck(ctx, stream, group, id).Err()
}

func (c *redisClient) AutoClaim(ctx context.Context, stream, group, consumer string, minIdle time.Duration) (*Entry, error) {
	msgs, _, err := c.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   stream,
		Group:    group,
		Consumer: consumer,
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
	return &Entry{ID: m.ID, Fields: stringFields(m.Values)}, nil
}

// renewScript extends a lease only while its stored value is still the
// caller's, so an old bench can neither revive nor drop a lease another bench
// now holds.
var renewScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0
`)

// releaseScript is the same compare, then DEL.
var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

func (c *redisClient) AcquireLease(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	return c.rdb.SetNX(ctx, key, value, ttl).Result()
}

func (c *redisClient) RenewLease(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	n, err := renewScript.Run(ctx, c.rdb, []string{key}, value, ttl.Milliseconds()).Int64()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (c *redisClient) ReleaseLease(ctx context.Context, key, value string) (bool, error) {
	n, err := releaseScript.Run(ctx, c.rdb, []string{key}, value).Int64()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (c *redisClient) LeaseValue(ctx context.Context, key string) (string, error) {
	v, err := c.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return v, err
}

func (c *redisClient) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

// LeaseKey is the one key a card's lease lives under: lease:<entry id>.
func LeaseKey(id string) string { return LeasePrefix + id }

// stringFields flattens a stream message's values to strings.
func stringFields(values map[string]interface{}) map[string]string {
	out := make(map[string]string, len(values))
	for k, v := range values {
		switch t := v.(type) {
		case string:
			out[k] = t
		case []byte:
			out[k] = string(t)
		case int64:
			out[k] = strconv.FormatInt(t, 10)
		default:
			out[k] = ""
		}
	}
	return out
}
