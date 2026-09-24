package land_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

type landTestFixture struct {
	t      *testing.T
	ctx    context.Context
	client *redis.Client
	sprint string
	repo   string
	base   string
	gen    int64
	lease  string
}

func newLandFixture(t *testing.T, repo, base string) *landTestFixture {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatalf("load fn library: %v", err)
	}

	f := &landTestFixture{
		t:      t,
		ctx:    ctx,
		client: client,
		sprint: "sprint-test",
		repo:   repo,
		base:   base,
	}

	// Setup writer generation and lease
	gen, err := land.CallWriter(ctx, client, repo, base, "nova-sprint", "fixture")
	if err != nil {
		t.Fatalf("init writer: %v", err)
	}
	f.gen = gen
	f.lease = fmt.Sprintf("%d:lease-token-1", gen)
	if err := client.Set(ctx, land.LeaseKey(repo, base), f.lease, 60*time.Second).Err(); err != nil {
		t.Fatalf("set lease: %v", err)
	}

	// Setup initial tip
	if err := client.HSet(ctx, land.TipKey(repo, base), "sha", "1111111111111111111111111111111111111111", "at", "1", "by", "init").Err(); err != nil {
		t.Fatalf("set tip: %v", err)
	}

	// Setup initial base policy
	if err := land.CallPolicySet(ctx, client, repo, base, "pol-1", "req-1", "runner-1"); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	// Fresh inbound consumer
	if err := client.HSet(ctx, "ev:github:consumer:land", "pending", "0", "at", strconv.FormatInt(time.Now().Unix(), 10)).Err(); err != nil {
		t.Fatalf("set consumer: %v", err)
	}

	return f
}
