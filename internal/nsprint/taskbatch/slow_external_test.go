//go:build slow

// The tests of this package that cost more than the per-commit run can pay:
// over five seconds each on the Linux bench, or a deadline, wedge or wall-clock
// bound proved by waiting it out. They are behind the `slow` build tag, so
// go-test-cmd and go-test-internal do not build them, and
// .github/workflows/nightly-slow.yml (and `make test-slow`) runs them whole,
// every night. Each carries the measurement that moved it. Nothing here is
// skipped or weakened.

package taskbatch_test

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskbatch"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// SLOW: 5.1 s on hetzner at dev 64b9bec48, over the five-second line.
// TestFCallOnRealRedis runs the loaded nova_sprint library (not the EVAL
// wrapper the miniredis tests use): 1,000 ids across 10 streams block, then
// cancel, each in one FCALL, with the invariants held after each.
func TestFCallOnRealRedis(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	rows := thousand()
	seed(t, c, rows)
	call := taskbatch.FCall(c)
	for _, r := range []taskbatch.Request{
		{Verb: taskbatch.Block, Why: "pit stop", IDs: ids(rows)},
		{Verb: taskbatch.Cancel, Why: "superseded", IDs: ids(rows)},
	} {
		r.Sprint, r.By = sprint, "rowan"
		start := time.Now()
		res, err := taskbatch.Batch(ctx, call, r)
		ms := time.Since(start).Milliseconds()
		t.Logf("%s n=%d ms=%d", r.Verb, res.N, ms)
		if err != nil || !res.OK || res.N != 1000 {
			t.Fatalf("%s = %+v %v", r.Verb, res, err)
		}
		if ms >= 1000 {
			t.Fatalf("%s of 1,000 took %d ms", r.Verb, ms)
		}
		check(t, c, ids(rows))
	}
	res, err := taskbatch.Sweep(ctx, call, taskbatch.SweepRequest{Sprint: sprint, By: "rowan", Friend: "f0"})
	if err != nil || !res.OK || res.N != 0 {
		t.Fatalf("sweep = %+v %v", res, err)
	}
}
