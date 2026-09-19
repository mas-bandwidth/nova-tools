package record

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisConsumer is the stream side of the record: one consumer inside the `record` group,
// reading `cards:done` and acking only after the store commits.
type RedisConsumer struct {
	rdb    *redis.Client
	stream string
	group  string
	name   string
}

// NewRedisConsumer dials addr, ensures the stream and its `record` group exist (created at
// entry 0 so no result produced before the record started is skipped -- replay is safe
// because Insert is idempotent on the stream id), and returns the consumer. An empty
// stream, group or name takes the package defaults.
func NewRedisConsumer(ctx context.Context, addr, stream, group, name string) (*RedisConsumer, error) {
	if strings.TrimSpace(stream) == "" {
		stream = Stream
	}
	if strings.TrimSpace(group) == "" {
		group = Group
	}
	if strings.TrimSpace(name) == "" {
		name = consumerName()
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis %s: %w", addr, err)
	}
	err := rdb.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		_ = rdb.Close()
		return nil, fmt.Errorf("consumer group %s on %s: %w", group, stream, err)
	}
	return &RedisConsumer{rdb: rdb, stream: stream, group: group, name: name}, nil
}

// Read returns up to count new entries. block < 0 does not block; block > 0 waits that
// long. Zero is never passed, because Redis reads BLOCK 0 as "forever".
func (c *RedisConsumer) Read(ctx context.Context, count int, block time.Duration) ([]Message, error) {
	return c.read(ctx, count, block, ">")
}

// ReadPending drains this consumer's own pending entries, oldest id first. It is how a
// crash between INSERT and XACK is repaired.
func (c *RedisConsumer) ReadPending(ctx context.Context, count int) ([]Message, error) {
	return c.read(ctx, count, -1, "0")
}

func (c *RedisConsumer) read(ctx context.Context, count int, block time.Duration, id string) ([]Message, error) {
	if count <= 0 {
		count = 16
	}
	res, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    c.group,
		Consumer: c.name,
		Streams:  []string{c.stream, id},
		Count:    int64(count),
		Block:    block,
	}).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Message
	for _, s := range res {
		for _, m := range s.Messages {
			out = append(out, Message{ID: m.ID, Fields: toStrings(m.Values)})
		}
	}
	return out, nil
}

// Ack marks one entry recorded. It is called only after Insert returned, so an acked entry
// is always a committed row.
func (c *RedisConsumer) Ack(ctx context.Context, id string) error {
	return c.rdb.XAck(ctx, c.stream, c.group, id).Err()
}

// Close releases the connection.
func (c *RedisConsumer) Close() error { return c.rdb.Close() }

func toStrings(values map[string]any) map[string]string {
	out := make(map[string]string, len(values))
	for k, v := range values {
		if s, ok := v.(string); ok {
			out[k] = s
			continue
		}
		out[k] = fmt.Sprint(v)
	}
	return out
}

func consumerName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "nova-work"
	}
	return fmt.Sprintf("nova-work-%s-%d", host, os.Getpid())
}
