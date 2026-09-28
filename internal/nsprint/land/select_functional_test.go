//go:build functional

package land_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestL9e: 2,001 distinct selection graphs: the oldest key is gone, the index holds 2,000,
// no key has a TTL (fails on: an unbounded or TTL'd cache).
func TestL9e(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo := "nova-tools"
	tree := "tree-l9e"
	goos := "linux"

	var oldestKey string
	var newestKey string

	// Insert 2,001 distinct selection graphs
	for i := 1; i <= 2001; i++ {
		cfg := fmt.Sprintf("cfg-%05d", i)
		key := land.SelKey(repo, tree, goos, cfg)
		if i == 1 {
			oldestKey = key
		}
		if i == 2001 {
			newestKey = key
		}

		revs := map[string][]string{
			"pkg/a": {fmt.Sprintf("pkg/b%d", i)},
		}

		ok, err := land.SelPut(ctx, rdb, repo, tree, goos, cfg, int64(i), revs)
		if err != nil {
			t.Fatalf("SelPut %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("SelPut %d returned false", i)
		}
	}

	// 1. The oldest key is gone
	oldExists, err := rdb.Exists(ctx, oldestKey).Result()
	if err != nil {
		t.Fatalf("Exists oldest: %v", err)
	}
	if oldExists != 0 {
		t.Fatalf("oldest key %s must be evicted, but still exists", oldestKey)
	}

	// 2. The newest key is present
	newExists, err := rdb.Exists(ctx, newestKey).Result()
	if err != nil {
		t.Fatalf("Exists newest: %v", err)
	}
	if newExists != 1 {
		t.Fatalf("newest key %s must exist", newestKey)
	}

	// 3. No key has a TTL
	ttl, err := rdb.TTL(ctx, newestKey).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl != -1 {
		t.Fatalf("key must have no TTL (got %v)", ttl)
	}

	// 4. The index holds exactly 2,000 keys
	idxCount, err := rdb.ZCard(ctx, land.SelIndexKey(repo)).Result()
	if err != nil {
		t.Fatalf("ZCard index: %v", err)
	}
	if idxCount != land.MaxCachedGraphs {
		t.Fatalf("index must hold %d keys, got %d", land.MaxCachedGraphs, idxCount)
	}
}
