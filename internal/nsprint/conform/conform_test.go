package conform

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

func TestConform(t *testing.T) {
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	ctx := context.Background()

	for _, k := range CodingKeys {
		t.Run(k+"/ok", func(t *testing.T) {
			client.HSet(ctx, "fleet:standard", "digest", "abc123digest")
			_ = AcquireLease(ctx, client, "b1", "inst1", "tok1")
			err := WriteConform(ctx, client, "b1", "tok1", ConformResult{
				OK:             true,
				StandardDigest: "abc123digest",
				Answers:        map[string]string{k: "ok"},
			})
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
		})
	}
}

func TestConformWholeFleetMissingIsDrift(t *testing.T) {
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	ctx := context.Background()

	client.SAdd(ctx, "benches", "b1")
	client.HSet(ctx, "fleet:standard", "digest", "abc123digest")

	ok, err := CheckAll(ctx, client)
	if err == nil || ok {
		t.Fatalf("expected error/drift for missing bench conform record, got ok=%v err=%v", ok, err)
	}
}

func TestConformSandboxNoExtraRead(t *testing.T) {
	t.Skip("sandbox control test placeholder")
}

func TestConformRowRewrittenWhole(t *testing.T) {
	t.Skip("row rewritten whole control test placeholder")
}

func TestConformLease(t *testing.T) {
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	ctx := context.Background()

	err := AcquireLease(ctx, client, "b1", "inst1", "tok1")
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}

	err = WriteConform(ctx, client, "b1", "wrong-token", ConformResult{OK: true, StandardDigest: "d"})
	if err == nil {
		t.Fatalf("expected write to fail with fenced/wrong token")
	}
}

func TestConformPoolIdentity(t *testing.T) {
	t.Skip("pool identity test placeholder")
}

func TestDealSkipsNonConformingBench(t *testing.T) {
	t.Skip("deal skips non conforming bench test placeholder")
}

func TestPreflight718BenchConform(t *testing.T) {
	t.Skip("preflight 718 test placeholder")
}

func TestStandardSetDigestStable(t *testing.T) {
	t.Skip("standard set digest stable test placeholder")
}
