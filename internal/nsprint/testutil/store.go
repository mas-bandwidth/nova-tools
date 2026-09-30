package testutil

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

// Store is a throwaway redis-server with the nova_sprint library loaded,
// driven through the handful of calls tests seeded miniredis with before
// the reads moved into Redis Functions (2026-09-27: a fake without FCALL
// cannot serve ns_fleet_facts or ns_land_watch_read). Every method answers
// like miniredis's of the same name; a test that needs more uses Client.
type Store struct {
	addr   string
	Client *redis.Client
	ctx    context.Context
}

// StartStore starts the server, loads the library and returns the store;
// both stop with the test.
func StartStore(t *testing.T) *Store {
	t.Helper()
	addr := Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	// ignored: a test fixture's cleanup; the test's own assertions are the report
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return &Store{addr: addr, Client: c, ctx: ctx}
}

// Addr is the server's host:port.
func (s *Store) Addr() string { return s.addr }

// HSet sets fields on a hash.
func (s *Store) HSet(key string, kv ...string) {
	args := make([]any, len(kv))
	for i, v := range kv {
		args[i] = v
	}
	s.Client.HSet(s.ctx, key, args...)
}

// HGet reads one field, "" when absent.
func (s *Store) HGet(key, field string) string { return s.Client.HGet(s.ctx, key, field).Val() }

// SAdd adds members to a set.
func (s *Store) SAdd(key string, members ...string) (int, error) {
	args := make([]any, len(members))
	for i, m := range members {
		args[i] = m
	}
	n, err := s.Client.SAdd(s.ctx, key, args...).Result()
	return int(n), err
}

// Members is a set's members.
func (s *Store) Members(key string) ([]string, error) { return s.Client.SMembers(s.ctx, key).Result() }

// SetTTL gives a key a TTL.
func (s *Store) SetTTL(key string, ttl time.Duration) { s.Client.Expire(s.ctx, key, ttl) }

// Exists says whether a key exists.
func (s *Store) Exists(key string) bool { return s.Client.Exists(s.ctx, key).Val() > 0 }

// Get reads a string key.
func (s *Store) Get(key string) (string, error) { return s.Client.Get(s.ctx, key).Result() }

// Del deletes keys; true when any existed.
func (s *Store) Del(keys ...string) bool { return s.Client.Del(s.ctx, keys...).Val() > 0 }

// HDel deletes fields of a hash.
func (s *Store) HDel(key string, fields ...string) { s.Client.HDel(s.ctx, key, fields...) }

// Keys is every key, sorted (a test's whole view of a throwaway store).
func (s *Store) Keys() []string {
	keys, _ := s.Client.Keys(s.ctx, "*").Result()
	sort.Strings(keys)
	return keys
}

// TTL is a key's time to live, 0 when it has none or does not exist.
func (s *Store) TTL(key string) time.Duration {
	d, err := s.Client.TTL(s.ctx, key).Result()
	if err != nil || d < 0 {
		return 0
	}
	return d
}

// Dump is every key with its type, one per line, for a failure message.
func (s *Store) Dump() string {
	var b strings.Builder
	for _, k := range s.Keys() {
		fmt.Fprintf(&b, "%s %s\n", s.Client.Type(s.ctx, k).Val(), k)
	}
	return b.String()
}
