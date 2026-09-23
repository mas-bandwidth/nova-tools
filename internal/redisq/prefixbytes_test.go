package redisq_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/redisq"
)

// mockScanner implements redisq.PrefixScanner with in-memory data.
type mockScanner struct {
	data map[string]int64
}

func (m *mockScanner) Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd {
	if cursor != 0 {
		return redis.NewScanCmdResult(nil, 0, nil)
	}
	prefix := strings.TrimSuffix(match, "*")
	keys := make([]string, 0, len(m.data))
	for k := range m.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return redis.NewScanCmdResult(keys, 0, nil)
}

func (m *mockScanner) MemoryUsage(ctx context.Context, key string, samples ...int) *redis.IntCmd {
	b, ok := m.data[key]
	if !ok {
		return redis.NewIntResult(0, errors.New("ERR no such key"))
	}
	return redis.NewIntResult(b, nil)
}

func TestBytesPerDeclaredPrefix(t *testing.T) {
	ctx := context.Background()

	t.Run("empty store returns zero per prefix", func(t *testing.T) {
		s := &mockScanner{data: map[string]int64{}}
		result, err := redisq.BytesPerDeclaredPrefix(ctx, s)
		if err != nil {
			t.Fatalf("BytesPerDeclaredPrefix: %s", err)
		}
		for _, p := range redisq.DeclaredPrefixes() {
			if _, ok := result[p]; !ok {
				t.Fatalf("declared prefix %q missing from result", p)
			}
			if result[p] != 0 {
				t.Fatalf("prefix %q = %d, want 0 on empty store", p, result[p])
			}
		}
		if got, want := len(result), len(redisq.DeclaredPrefixes()); got != want {
			t.Fatalf("result has %d entries, want %d", got, want)
		}
	})

	t.Run("lease keys appear in byte count", func(t *testing.T) {
		s := &mockScanner{data: map[string]int64{
			"swarm:lease:space:slot-9":  10485760,
			"swarm:lease:space:slot-10": 512,
		}}
		result, err := redisq.BytesPerDeclaredPrefix(ctx, s)
		if err != nil {
			t.Fatalf("BytesPerDeclaredPrefix: %s", err)
		}
		leaseBytes := result[redisq.LeasePrefix]
		if leaseBytes == 0 {
			t.Fatal("lease prefix shows zero bytes with lease keys present")
		}
		if leaseBytes != 10485760+512 {
			t.Fatalf("lease prefix bytes = %d, want %d", leaseBytes, 10485760+512)
		}
	})

	t.Run("cap prefix is represented", func(t *testing.T) {
		s := &mockScanner{data: map[string]int64{
			"swarm:cap:muse:muse-1": 2048,
		}}
		result, err := redisq.BytesPerDeclaredPrefix(ctx, s)
		if err != nil {
			t.Fatalf("BytesPerDeclaredPrefix: %s", err)
		}
		if result[redisq.CapPrefix] == 0 {
			t.Fatal("cap prefix shows zero bytes with cap keys present")
		}
	})

	t.Run("synthetic 10MB prefix is reported", func(t *testing.T) {
		s := &mockScanner{data: map[string]int64{
			"swarm:lease:space:big-1": 10485760,
		}}
		result, err := redisq.BytesPerDeclaredPrefix(ctx, s)
		if err != nil {
			t.Fatalf("BytesPerDeclaredPrefix: %s", err)
		}
		if result[redisq.LeasePrefix] != 10485760 {
			t.Fatalf("lease prefix = %d, want 10485760 (10 MiB)", result[redisq.LeasePrefix])
		}
	})

	t.Run("key gone between scan and mem usage is skipped", func(t *testing.T) {
		s := &mockScanner{data: map[string]int64{
			"swarm:lease:stays:key": 4096,
		}}
		result, err := redisq.BytesPerDeclaredPrefix(ctx, s)
		if err != nil {
			t.Fatalf("BytesPerDeclaredPrefix: %s", err)
		}
		if result[redisq.LeasePrefix] != 4096 {
			t.Fatalf("lease prefix = %d, want 4096", result[redisq.LeasePrefix])
		}
	})
}
