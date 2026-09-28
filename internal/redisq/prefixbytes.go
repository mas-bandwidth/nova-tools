package redisq

import (
	"context"
	"errors"

	"github.com/redis/go-redis/v9"
)

// PrefixScanner is the Redis interface BytesPerPrefix needs: SCAN returning
// keys by pattern, and MEMORY USAGE returning per-key byte counts.
type PrefixScanner interface {
	Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd
	MemoryUsage(ctx context.Context, key string, samples ...int) *redis.IntCmd
}

// DeclaredPrefixes returns the known ephemeral key prefixes the store tracks.
func DeclaredPrefixes() []string {
	return []string{LeasePrefix, CapPrefix}
}

// BytesPerPrefix scans keys matching prefix and returns the sum of their
// memory usage in bytes. A key deleted between SCAN and MEMORY USAGE is
// silently skipped. Keys are deduplicated across SCAN pages, since Redis
// SCAN may return the same key more than once during a single cursor walk.
func BytesPerPrefix(ctx context.Context, s PrefixScanner, prefix string) (int64, error) {
	var total int64
	var cursor uint64
	seen := make(map[string]struct{})
	for {
		keys, next, err := s.Scan(ctx, cursor, prefix+"*", 128).Result()
		if err != nil {
			return 0, err
		}
		for _, key := range keys {
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			// No explicit sample count: some MEMORY USAGE implementations
			// (miniredis included) reject a SAMPLES argument of 0.
			usage, err := s.MemoryUsage(ctx, key).Result()
			if err != nil {
				if isKeyGone(err) {
					continue
				}
				return 0, err
			}
			total += usage
		}
		cursor = next
		if cursor == 0 {
			return total, nil
		}
	}
}

// BytesPerDeclaredPrefix returns a map of each declared prefix to the total
// bytes its keys consume in the Redis store.
func BytesPerDeclaredPrefix(ctx context.Context, s PrefixScanner) (map[string]int64, error) {
	result := make(map[string]int64, len(DeclaredPrefixes()))
	for _, p := range DeclaredPrefixes() {
		n, err := BytesPerPrefix(ctx, s, p)
		if err != nil {
			return nil, err
		}
		result[p] = n
	}
	return result, nil
}

// QueueBytesPerPrefix is BytesPerPrefix called with the Queue's client.
func (q *Queue) QueueBytesPerPrefix(ctx context.Context, prefix string) (int64, error) {
	return BytesPerPrefix(ctx, q.rdb, prefix)
}

// QueueBytesPerDeclaredPrefix is BytesPerDeclaredPrefix called with the
// Queue's client.
func (q *Queue) QueueBytesPerDeclaredPrefix(ctx context.Context) (map[string]int64, error) {
	return BytesPerDeclaredPrefix(ctx, q.rdb)
}

// isKeyGone reports whether err represents a key that no longer exists.
// Real Redis and miniredis return redis.Nil for MEMORY USAGE on a missing
// key; the string form is kept for scanner implementations (including the
// unit test's mock) that surface the raw protocol error text instead.
func isKeyGone(err error) bool {
	if errors.Is(err, redis.Nil) {
		return true
	}
	return err != nil && err.Error() == "ERR no such key"
}
