package reconcile

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestFleetRefillReturnsOnUpOnly verifies that benchReturns counts a bench as returned
// only when its state changes to UP, even when its beat was present the entire time.
func TestFleetRefillReturnsOnUpOnly(t *testing.T) {
	t.Setenv(testutil.CIEnv, "1")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}

	const bench = "bench-ret"
	c.SAdd(ctx, "benches", bench)

	// Beat is ALWAYS present in Redis throughout the test.
	c.HSet(ctx, "bench:"+bench+":beat", "host", "localhost", "user", "nova", "at", "1000")

	refill := &Refill{Client: c}

	// 1. Initial pass with state DOWN: not counted as returned.
	c.HSet(ctx, "bench:"+bench+":state", "state", "DOWN", "at", "1000")
	ret, err := refill.benchReturns(ctx, []string{bench})
	if err != nil {
		t.Fatalf("benchReturns (DOWN): %v", err)
	}
	if len(ret) != 0 {
		t.Fatalf("benchReturns on DOWN got %v; want none", ret)
	}

	// 2. Pass with state PROBING: beat is present, but state is PROBING -> not returned.
	c.HSet(ctx, "bench:"+bench+":state", "state", "PROBING", "at", "1000")
	ret, err = refill.benchReturns(ctx, []string{bench})
	if err != nil {
		t.Fatalf("benchReturns (PROBING): %v", err)
	}
	if len(ret) != 0 {
		t.Fatalf("benchReturns on PROBING got %v; want none", ret)
	}

	// 3. Pass with state HELD: beat is present, but state is HELD -> not returned.
	c.HSet(ctx, "bench:"+bench+":state", "state", "HELD", "at", "1000")
	ret, err = refill.benchReturns(ctx, []string{bench})
	if err != nil {
		t.Fatalf("benchReturns (HELD): %v", err)
	}
	if len(ret) != 0 {
		t.Fatalf("benchReturns on HELD got %v; want none", ret)
	}

	// 4. Control: state transitions to UP -> bench is returned!
	c.HSet(ctx, "bench:"+bench+":state", "state", "UP", "at", "1000")
	ret, err = refill.benchReturns(ctx, []string{bench})
	if err != nil {
		t.Fatalf("benchReturns (UP): %v", err)
	}
	if len(ret) != 1 || ret[0] != bench {
		t.Fatalf("benchReturns on UP got %v; want [%s]", ret, bench)
	}

	// 5. Subsequent pass: state is still UP -> not returned again.
	ret, err = refill.benchReturns(ctx, []string{bench})
	if err != nil {
		t.Fatalf("benchReturns (still UP): %v", err)
	}
	if len(ret) != 0 {
		t.Fatalf("benchReturns on second UP pass got %v; want none", ret)
	}
}
