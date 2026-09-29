//go:build functional

package fleet_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

func TestRetireFunctionalRealRedis(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()

	runner := &fakeExecRunner{
		out: map[string]string{
			"ansible": "alpha | CHANGED => { \"changed\": true }\n",
		},
	}

	var out bytes.Buffer
	r := &fleet.Retire{
		Runner:  runner,
		Client:  c,
		Bench:   "alpha",
		Unit:    "stray-unit",
		Benches: []string{"alpha"},
		Out:     &out,
	}

	ctx := context.Background()
	res, err := r.Run(ctx)
	if err != nil {
		t.Fatalf("Retire.Run failed: %v", err)
	}
	if !res.OK() {
		t.Fatalf("res.OK: got false, want true")
	}

	// Verify real Redis receipt
	hash, err := c.HGetAll(ctx, fleet.RetireKey("alpha")).Result()
	if err != nil {
		t.Fatalf("HGetAll %s: %v", fleet.RetireKey("alpha"), err)
	}
	if hash["bench"] != "alpha" || hash["unit"] != "stray-unit" || hash["status"] != "OK" {
		t.Errorf("receipt hash mismatch: %v", hash)
	}

	// Verify real Redis retired_units set
	isMember, err := c.SIsMember(ctx, fleet.RetiredUnitsKey("alpha"), "stray-unit").Result()
	if err != nil || !isMember {
		t.Errorf("stray-unit not in retired_units set: err=%v isMember=%v", err, isMember)
	}
}
